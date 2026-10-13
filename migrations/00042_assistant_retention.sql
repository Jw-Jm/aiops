-- +goose Up
SET ROLE schema_owner;
CREATE TABLE investigation.assistant_legal_holds (
 tenant_id uuid NOT NULL,turn_id uuid NOT NULL,held boolean NOT NULL,
 reason text NOT NULL,subject text NOT NULL,updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,turn_id),FOREIGN KEY(tenant_id,turn_id) REFERENCES investigation.assistant_turns(tenant_id,turn_id)
);
ALTER TABLE investigation.assistant_legal_holds ENABLE ROW LEVEL SECURITY;
ALTER TABLE investigation.assistant_legal_holds FORCE ROW LEVEL SECURITY;
CREATE POLICY assistant_hold_tenant ON investigation.assistant_legal_holds USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT ON investigation.assistant_legal_holds TO api_runtime_role,worker_runtime_role;
-- Completed contents remain immutable. The owner may shorten only TTL metadata
-- during this migration; physical deletion is limited to the fenced definer.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION investigation.protect_success() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF current_user='schema_owner' AND current_setting('app.assistant_cleanup',true)=OLD.tenant_id::text
   AND EXISTS(SELECT 1 FROM investigation.jobs WHERE tenant_id=OLD.tenant_id AND job_id=OLD.job_id AND schema_version='investigation-job/v3') THEN RETURN OLD;END IF;
  IF OLD.state='succeeded' THEN RAISE EXCEPTION 'committed investigation step is immutable';END IF;
  RETURN NULL;
 END IF;
 IF OLD.state='succeeded' THEN
  IF current_user='schema_owner' AND NEW.retain_until<=OLD.retain_until
   AND (to_jsonb(NEW)-'retain_until')=(to_jsonb(OLD)-'retain_until')
   AND EXISTS(SELECT 1 FROM investigation.jobs WHERE tenant_id=OLD.tenant_id AND job_id=OLD.job_id AND schema_version='investigation-job/v3') THEN RETURN NEW;END IF;
  RAISE EXCEPTION 'committed investigation step is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- Ordinary v3 tool contents follow the assistant TTL. Old v2 ledgers remain 365d.
-- Backfill per tenant without bypassing forced RLS.
-- +goose StatementBegin
DO $$ DECLARE tid uuid; prior text:=current_setting('app.tenant_id',true); BEGIN
 FOR tid IN SELECT tenant_id FROM platform.tenants LOOP
  PERFORM set_config('app.tenant_id',tid::text,true);
  UPDATE investigation.steps s SET retain_until=LEAST(s.retain_until,s.started_at+interval '30 days')
   FROM investigation.jobs j WHERE j.tenant_id=tid AND s.tenant_id=tid AND j.job_id=s.job_id AND j.schema_version='investigation-job/v3';
 END LOOP;
 PERFORM set_config('app.tenant_id',COALESCE(prior,''),true);
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION investigation.assistant_step_retention() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM investigation.jobs WHERE tenant_id=NEW.tenant_id AND job_id=NEW.job_id AND schema_version='investigation-job/v3') THEN
  NEW.retain_until:=LEAST(NEW.retain_until,NEW.started_at+interval '30 days');
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER assistant_step_ttl BEFORE INSERT ON investigation.steps FOR EACH ROW EXECUTE FUNCTION investigation.assistant_step_retention();
-- +goose StatementBegin
CREATE FUNCTION investigation.assistant_set_hold(tid uuid,turn uuid,sub text,hold boolean,why text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,investigation,platform AS $$
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid OR sub='' OR length(why) NOT BETWEEN 1 AND 1024 THEN RETURN false;END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(tid::text,180));
 IF NOT platform.assistant_lock_audit_authority(tid,sub) THEN RETURN false;END IF;
 PERFORM 1 FROM investigation.assistant_turns WHERE tenant_id=tid AND turn_id=turn FOR UPDATE;
 IF NOT FOUND THEN RETURN false;END IF;
 INSERT INTO investigation.assistant_legal_holds(tenant_id,turn_id,held,reason,subject) VALUES(tid,turn,hold,why,sub)
 ON CONFLICT(tenant_id,turn_id) DO UPDATE SET held=EXCLUDED.held,reason=EXCLUDED.reason,subject=EXCLUDED.subject,updated_at=clock_timestamp();
 UPDATE investigation.assistant_messages SET legal_hold=hold WHERE tenant_id=tid AND turn_id=turn;
 UPDATE investigation.assistant_query_results SET legal_hold=hold WHERE tenant_id=tid AND turn_id=turn;
 RETURN true;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION investigation.assistant_set_hold(uuid,uuid,text,boolean,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION investigation.assistant_set_hold(uuid,uuid,text,boolean,text) TO api_runtime_role;
-- +goose StatementBegin
CREATE FUNCTION investigation.assistant_cleanup(tid uuid,batch_limit integer)
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,investigation,platform AS $$
DECLARE doomed uuid[];jobs_to_delete uuid[];conversations_to_delete uuid[];deleted integer:=0;empty_deleted integer:=0;prior_cleanup text:=current_setting('app.assistant_cleanup',true);
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid OR batch_limit NOT BETWEEN 1 AND 100 THEN RAISE EXCEPTION 'invalid assistant cleanup scope';END IF;
 -- Serialize holds, official evidence references and dependency mutations.
 PERFORM pg_advisory_xact_lock(hashtextextended(tid::text,180));
 -- Conversation locks fence submission/rebinding while closure is computed.
 PERFORM 1 FROM investigation.assistant_conversations WHERE tenant_id=tid ORDER BY conversation_id FOR UPDATE;
 WITH RECURSIVE protected_evidence(id) AS (
  SELECT evidence_id FROM platform.evidence_metadata WHERE tenant_id=tid AND (legal_hold OR retain_until>clock_timestamp() OR replay_state='archive_pending')
  UNION SELECT evidence_id FROM platform.evidence_retention_references WHERE tenant_id=tid AND (active OR retain_until>clock_timestamp())
  UNION SELECT d.dependency_id FROM platform.evidence_dependencies d JOIN protected_evidence e ON e.id=d.referrer_id WHERE d.tenant_id=tid
 ), dependencies(turn_id,dependency_id) AS (
  SELECT turn_id,parent_turn_id FROM investigation.assistant_turns WHERE tenant_id=tid AND parent_turn_id IS NOT NULL
  UNION SELECT t.turn_id,(h->>'turnId')::uuid FROM investigation.assistant_turns t CROSS JOIN LATERAL jsonb_array_elements(COALESCE(t.input->'history','[]'::jsonb)) h WHERE t.tenant_id=tid AND h->>'turnId' IS NOT NULL
  UNION SELECT t.turn_id,q.turn_id FROM investigation.assistant_turns t CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(t.answer->'queryResultRefs','[]'::jsonb)) r JOIN investigation.assistant_query_results q ON q.tenant_id=tid AND q.query_result_id=r::uuid WHERE t.tenant_id=tid
 ), protected_turns(id) AS (
  SELECT t.turn_id FROM investigation.assistant_turns t WHERE t.tenant_id=tid AND (
   t.created_at>clock_timestamp()-interval '30 days' OR t.state IN('queued','running')
   OR EXISTS(SELECT 1 FROM investigation.assistant_messages m WHERE m.tenant_id=tid AND m.turn_id=t.turn_id AND (m.legal_hold OR m.retain_until>clock_timestamp()))
   OR EXISTS(SELECT 1 FROM investigation.assistant_query_results q WHERE q.tenant_id=tid AND q.turn_id=t.turn_id AND (q.legal_hold OR q.retain_until>clock_timestamp()))
   OR EXISTS(SELECT 1 FROM investigation.assistant_legal_holds h WHERE h.tenant_id=tid AND h.turn_id=t.turn_id AND h.held)
   OR EXISTS(SELECT 1 FROM investigation.steps s WHERE s.tenant_id=tid AND s.job_id=t.job_id AND s.retain_until>clock_timestamp())
   OR EXISTS(SELECT 1 FROM platform.evidence_retention_references r WHERE r.tenant_id=tid AND r.reference_kind='investigation' AND r.reference_id=t.job_id AND (r.active OR r.retain_until>clock_timestamp()))
   OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(t.answer->'evidenceRefs','[]'::jsonb)) r JOIN protected_evidence e ON e.id=r::uuid)
   OR EXISTS(SELECT 1 FROM investigation.steps s CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(s.result->'evidenceRefs','[]'::jsonb)) r JOIN protected_evidence e ON e.id=r::uuid WHERE s.tenant_id=tid AND s.job_id=t.job_id)
  )
  UNION SELECT d.dependency_id FROM dependencies d JOIN protected_turns p ON p.id=d.turn_id
 )
 SELECT array_agg(turn_id),array_agg(job_id) INTO doomed,jobs_to_delete FROM (
  SELECT t.turn_id,t.job_id FROM investigation.assistant_turns t JOIN investigation.jobs j USING(tenant_id,job_id)
  WHERE t.tenant_id=tid AND j.schema_version='investigation-job/v3' AND j.state NOT IN('queued','running')
   AND NOT EXISTS(SELECT 1 FROM protected_turns p WHERE p.id=t.turn_id)
   -- Delete leaves first so retained/deferred descendants cannot lose parents.
   AND NOT EXISTS(SELECT 1 FROM dependencies d WHERE d.dependency_id=t.turn_id AND d.turn_id<>t.turn_id)
  ORDER BY t.created_at,t.turn_id LIMIT batch_limit
 ) candidates;
 IF doomed IS NOT NULL THEN
 DELETE FROM investigation.assistant_events WHERE tenant_id=tid AND turn_id=ANY(doomed);
 DELETE FROM investigation.assistant_request_keys WHERE tenant_id=tid AND turn_id=ANY(doomed);
 DELETE FROM investigation.assistant_query_results WHERE tenant_id=tid AND turn_id=ANY(doomed);
 DELETE FROM investigation.assistant_legal_holds WHERE tenant_id=tid AND turn_id=ANY(doomed);
 DELETE FROM investigation.assistant_messages WHERE tenant_id=tid AND turn_id=ANY(doomed);
 DELETE FROM investigation.admissions WHERE tenant_id=tid AND job_id=ANY(jobs_to_delete);
 DELETE FROM investigation.replay_ledger WHERE tenant_id=tid AND job_id=ANY(jobs_to_delete);
 DELETE FROM investigation.call_allocations WHERE tenant_id=tid AND job_id=ANY(jobs_to_delete);
 DELETE FROM investigation.contexts WHERE tenant_id=tid AND job_id=ANY(jobs_to_delete);
 DELETE FROM investigation.events WHERE tenant_id=tid AND job_id=ANY(jobs_to_delete);
 DELETE FROM investigation.request_keys WHERE tenant_id=tid AND job_id=ANY(jobs_to_delete);
 PERFORM set_config('app.assistant_cleanup',tid::text,true);
 DELETE FROM investigation.steps WHERE tenant_id=tid AND job_id=ANY(jobs_to_delete);
 PERFORM set_config('app.assistant_cleanup',COALESCE(prior_cleanup,''),true);
 DELETE FROM investigation.worker_queue WHERE tenant_id=tid AND job_id=ANY(jobs_to_delete);
 DELETE FROM investigation.assistant_turns WHERE tenant_id=tid AND turn_id=ANY(doomed);
 GET DIAGNOSTICS deleted=ROW_COUNT;
 DELETE FROM investigation.jobs WHERE tenant_id=tid AND job_id=ANY(jobs_to_delete);
 END IF;
 -- Retained turns protect the whole conversation binding. Once the last turn
 -- expires, remove old title/symptom/context and request keys as well.
 SELECT array_agg(conversation_id) INTO conversations_to_delete FROM (
  SELECT c.conversation_id FROM investigation.assistant_conversations c
  WHERE c.tenant_id=tid AND c.updated_at<=clock_timestamp()-interval '30 days'
   AND NOT EXISTS(SELECT 1 FROM investigation.assistant_turns t WHERE t.tenant_id=tid AND t.conversation_id=c.conversation_id)
  ORDER BY c.updated_at,c.conversation_id LIMIT batch_limit
 ) empty_conversations;
 IF conversations_to_delete IS NOT NULL THEN
  DELETE FROM investigation.assistant_events WHERE tenant_id=tid AND conversation_id=ANY(conversations_to_delete);
  DELETE FROM investigation.assistant_creation_keys WHERE tenant_id=tid AND conversation_id=ANY(conversations_to_delete);
  DELETE FROM investigation.assistant_binding_requests WHERE tenant_id=tid AND conversation_id=ANY(conversations_to_delete);
  DELETE FROM investigation.assistant_messages WHERE tenant_id=tid AND conversation_id=ANY(conversations_to_delete);
  DELETE FROM investigation.assistant_conversations WHERE tenant_id=tid AND conversation_id=ANY(conversations_to_delete);
  GET DIAGNOSTICS empty_deleted=ROW_COUNT;
 END IF;
 RETURN deleted+empty_deleted;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION investigation.assistant_cleanup(uuid,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION investigation.assistant_cleanup(uuid,integer) TO worker_runtime_role;
RESET ROLE;
