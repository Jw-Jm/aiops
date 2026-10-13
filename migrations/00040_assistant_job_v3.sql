-- +goose Up
SET ROLE schema_owner;
ALTER TABLE investigation.jobs ALTER COLUMN incident_id DROP NOT NULL;
ALTER TABLE investigation.jobs ALTER COLUMN trigger_revision DROP NOT NULL;
ALTER TABLE investigation.jobs ADD COLUMN assistant_binding jsonb;
ALTER TABLE investigation.jobs ADD CONSTRAINT assistant_job_version CHECK ((
 (schema_version IN('investigation-job/v1','investigation-job/v2') AND incident_id IS NOT NULL AND trigger_revision>0 AND assistant_binding IS NULL)
 OR (schema_version='investigation-job/v3' AND assistant_binding IS NOT NULL AND assistant_binding->>'taskKind' IN('data_query','incident_followup','resource_troubleshooting')
 AND ((assistant_binding->>'taskKind'='incident_followup' AND incident_id IS NOT NULL AND trigger_revision>0) OR (assistant_binding->>'taskKind'<>'incident_followup' AND incident_id IS NULL AND trigger_revision IS NULL)))
) IS TRUE);
DROP INDEX investigation.investigation_job_identity;
CREATE UNIQUE INDEX investigation_job_identity ON investigation.jobs(tenant_id,incident_id,policy_version,trigger_revision,trigger_kind,requesting_subject,effective_scope_digest) WHERE schema_version IN('investigation-job/v1','investigation-job/v2');
CREATE UNIQUE INDEX assistant_job_turn ON investigation.jobs(tenant_id,(assistant_binding->>'conversationId'),(assistant_binding->>'turnId')) WHERE schema_version='investigation-job/v3';
ALTER TABLE investigation.jobs ADD COLUMN assistant_conversation_id uuid GENERATED ALWAYS AS ((assistant_binding->>'conversationId')::uuid) STORED;
ALTER TABLE investigation.jobs ADD COLUMN assistant_turn_id uuid GENERATED ALWAYS AS ((assistant_binding->>'turnId')::uuid) STORED;
ALTER TABLE investigation.jobs ADD CONSTRAINT assistant_job_conversation_fk FOREIGN KEY(tenant_id,assistant_conversation_id) REFERENCES investigation.assistant_conversations(tenant_id,conversation_id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE investigation.jobs ADD CONSTRAINT assistant_job_turn_fk FOREIGN KEY(tenant_id,assistant_conversation_id,assistant_turn_id) REFERENCES investigation.assistant_turns(tenant_id,conversation_id,turn_id) DEFERRABLE INITIALLY DEFERRED;
-- +goose StatementBegin
CREATE FUNCTION investigation.assistant_sync_job() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cid uuid;tid uuid;next_state text;seq bigint;
BEGIN
 IF NEW.schema_version<>'investigation-job/v3' THEN RETURN NEW; END IF;
 cid:=(NEW.assistant_binding->>'conversationId')::uuid;tid:=(NEW.assistant_binding->>'turnId')::uuid;
 next_state:=CASE NEW.state WHEN 'succeeded' THEN CASE WHEN NEW.result->>'answerKind'='clarification' THEN 'awaiting_user' ELSE 'completed' END ELSE NEW.state END;
 UPDATE investigation.assistant_turns SET state=next_state,error_code=NEW.error_code,answer=NEW.result WHERE tenant_id=NEW.tenant_id AND turn_id=tid;
 IF NEW.result IS NOT NULL AND NEW.state IN('succeeded','partial') THEN
  PERFORM 1 FROM investigation.assistant_conversations WHERE tenant_id=NEW.tenant_id AND conversation_id=cid FOR UPDATE;
  SELECT COALESCE(max(sequence),0)+1 INTO seq FROM investigation.assistant_messages WHERE tenant_id=NEW.tenant_id AND conversation_id=cid;
  INSERT INTO investigation.assistant_messages(tenant_id,message_id,conversation_id,turn_id,sequence,role,content) VALUES(NEW.tenant_id,tid,cid,tid,seq,'assistant',NEW.result) ON CONFLICT(tenant_id,turn_id,role) DO NOTHING;
 END IF;
 UPDATE investigation.assistant_conversations SET updated_at=clock_timestamp() WHERE tenant_id=NEW.tenant_id AND conversation_id=cid;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER assistant_sync_job AFTER UPDATE OF state,result ON investigation.jobs FOR EACH ROW EXECUTE FUNCTION investigation.assistant_sync_job();
-- +goose StatementBegin
CREATE FUNCTION investigation.assistant_relay_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b jsonb;
BEGIN
 SELECT assistant_binding INTO b FROM investigation.jobs WHERE tenant_id=NEW.tenant_id AND job_id=NEW.job_id AND schema_version='investigation-job/v3';
 IF b IS NOT NULL THEN
  INSERT INTO investigation.assistant_events(tenant_id,conversation_id,turn_id,type,payload,job_event_seq)
  VALUES(NEW.tenant_id,(b->>'conversationId')::uuid,(b->>'turnId')::uuid,
   CASE NEW.event_type WHEN 'result' THEN 'answer.ready' ELSE 'turn.'||NEW.event_type END,NEW.payload,NEW.event_seq) ON CONFLICT DO NOTHING;
 END IF; RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER assistant_relay AFTER INSERT ON investigation.events FOR EACH ROW EXECUTE FUNCTION investigation.assistant_relay_event();
RESET ROLE;
