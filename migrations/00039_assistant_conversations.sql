-- +goose Up
SET ROLE schema_owner;
-- Reuse the bootstrapped investigation schema: migration_role has no database CREATE privilege.
CREATE TABLE investigation.assistant_conversations (
 tenant_id uuid NOT NULL REFERENCES platform.tenants(tenant_id), conversation_id uuid NOT NULL,
 owner_subject text NOT NULL,title text NOT NULL, mode text NOT NULL CHECK(mode IN('data_query','troubleshoot')),
 authorization_domain text NOT NULL CHECK(authorization_domain IN('operational','audit')),
 cluster_uid text NOT NULL DEFAULT '', binding jsonb NOT NULL, revision bigint NOT NULL DEFAULT 1,
 archived_at timestamptz, created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,conversation_id), CHECK(authorization_domain<>'audit' OR (mode='data_query' AND cluster_uid=''))
);
CREATE INDEX assistant_owner_list ON investigation.assistant_conversations(tenant_id,owner_subject,updated_at DESC,conversation_id);
CREATE TABLE investigation.assistant_creation_keys (
 tenant_id uuid NOT NULL,owner_subject text NOT NULL,idempotency_key text NOT NULL,request_digest text NOT NULL,conversation_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,owner_subject,idempotency_key),FOREIGN KEY(tenant_id,conversation_id) REFERENCES investigation.assistant_conversations(tenant_id,conversation_id)
);
CREATE TABLE investigation.assistant_turns (
 tenant_id uuid NOT NULL,turn_id uuid NOT NULL,conversation_id uuid NOT NULL,job_id uuid NOT NULL,
 parent_turn_id uuid, user_message_id uuid NOT NULL, input jsonb NOT NULL,input_digest text NOT NULL,context_digest text NOT NULL,
 state text NOT NULL CHECK(state IN('queued','running','awaiting_user','completed','partial','failed','cancelled','expired')),
 base_conversation_revision bigint NOT NULL CHECK(base_conversation_revision>0),
 error_code text NOT NULL DEFAULT '',answer jsonb,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,turn_id),UNIQUE(tenant_id,job_id),UNIQUE(tenant_id,conversation_id,turn_id),
 FOREIGN KEY(tenant_id,conversation_id) REFERENCES investigation.assistant_conversations(tenant_id,conversation_id),
 FOREIGN KEY(tenant_id,job_id) REFERENCES investigation.jobs(tenant_id,job_id),
 FOREIGN KEY(tenant_id,conversation_id,parent_turn_id) REFERENCES investigation.assistant_turns(tenant_id,conversation_id,turn_id)
);
CREATE UNIQUE INDEX assistant_one_active_turn ON investigation.assistant_turns(tenant_id,conversation_id) WHERE state IN('queued','running');
CREATE TABLE investigation.assistant_messages (
 tenant_id uuid NOT NULL,message_id uuid NOT NULL,conversation_id uuid NOT NULL,turn_id uuid,
 sequence bigint NOT NULL CHECK(sequence>0),role text NOT NULL CHECK(role IN('user','assistant')),content jsonb NOT NULL,
 retain_until timestamptz NOT NULL DEFAULT clock_timestamp()+interval '30 days',legal_hold boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(tenant_id,message_id),
 UNIQUE(tenant_id,conversation_id,sequence),UNIQUE(tenant_id,conversation_id,message_id),UNIQUE(tenant_id,turn_id,role),
 FOREIGN KEY(tenant_id,conversation_id) REFERENCES investigation.assistant_conversations(tenant_id,conversation_id),
 FOREIGN KEY(tenant_id,conversation_id,turn_id) REFERENCES investigation.assistant_turns(tenant_id,conversation_id,turn_id)
);
ALTER TABLE investigation.assistant_turns ADD CONSTRAINT assistant_turn_message_fk FOREIGN KEY(tenant_id,conversation_id,user_message_id) REFERENCES investigation.assistant_messages(tenant_id,conversation_id,message_id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE investigation.assistant_request_keys (
 tenant_id uuid NOT NULL,owner_subject text NOT NULL,idempotency_key text NOT NULL,request_digest text NOT NULL,
 conversation_id uuid NOT NULL,turn_id uuid NOT NULL,PRIMARY KEY(tenant_id,owner_subject,idempotency_key),
 FOREIGN KEY(tenant_id,conversation_id,turn_id) REFERENCES investigation.assistant_turns(tenant_id,conversation_id,turn_id)
);
CREATE TABLE investigation.assistant_query_results (
 tenant_id uuid NOT NULL,query_result_id uuid NOT NULL,turn_id uuid NOT NULL,owner_subject text NOT NULL,
 plan_digest text NOT NULL,result jsonb NOT NULL,scope jsonb NOT NULL,source_manifest jsonb NOT NULL DEFAULT '{}',
 retain_until timestamptz NOT NULL DEFAULT clock_timestamp()+interval '30 days',legal_hold boolean NOT NULL DEFAULT false,
 PRIMARY KEY(tenant_id,query_result_id),UNIQUE(tenant_id,turn_id,plan_digest),
 FOREIGN KEY(tenant_id,turn_id) REFERENCES investigation.assistant_turns(tenant_id,turn_id)
);
CREATE TABLE investigation.assistant_events (
 tenant_id uuid NOT NULL,conversation_id uuid NOT NULL,event_seq bigint GENERATED ALWAYS AS IDENTITY,
 turn_id uuid NOT NULL,type text NOT NULL,payload jsonb NOT NULL,job_event_seq bigint,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(tenant_id,conversation_id,event_seq),
 UNIQUE(tenant_id,turn_id,job_event_seq),FOREIGN KEY(tenant_id,conversation_id,turn_id) REFERENCES investigation.assistant_turns(tenant_id,conversation_id,turn_id)
);
-- +goose StatementBegin
DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['conversations','creation_keys','turns','messages','request_keys','query_results','events'] LOOP
  EXECUTE format('ALTER TABLE investigation.assistant_%I ENABLE ROW LEVEL SECURITY',n);
  EXECUTE format('ALTER TABLE investigation.assistant_%I FORCE ROW LEVEL SECURITY',n);
  EXECUTE format('CREATE POLICY tenant_isolation ON investigation.assistant_%I USING(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',n);
 END LOOP;
END $$;
-- +goose StatementEnd
CREATE TABLE investigation.assistant_worker_capabilities (
 tenant_id uuid NOT NULL REFERENCES platform.tenants(tenant_id),worker_id text NOT NULL,
 model_profile_version text NOT NULL,catalog_version text NOT NULL,expires_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,worker_id)
);
ALTER TABLE investigation.assistant_worker_capabilities ENABLE ROW LEVEL SECURITY;
ALTER TABLE investigation.assistant_worker_capabilities FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON investigation.assistant_worker_capabilities USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT ON investigation.assistant_worker_capabilities TO api_runtime_role,worker_runtime_role;
GRANT INSERT,UPDATE ON investigation.assistant_worker_capabilities TO worker_runtime_role;
GRANT SELECT,INSERT,UPDATE ON investigation.assistant_conversations,investigation.assistant_turns TO api_runtime_role,worker_runtime_role;
GRANT SELECT,INSERT ON investigation.assistant_messages,investigation.assistant_request_keys,investigation.assistant_events TO api_runtime_role,worker_runtime_role;
GRANT SELECT,INSERT ON investigation.assistant_creation_keys TO api_runtime_role;
GRANT SELECT,INSERT ON investigation.assistant_query_results TO api_runtime_role,worker_runtime_role;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA investigation TO api_runtime_role,worker_runtime_role;
-- +goose StatementBegin
CREATE FUNCTION investigation.assistant_immutable_message() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.tenant_id,NEW.message_id,NEW.conversation_id,NEW.turn_id,NEW.sequence,NEW.role,NEW.content,NEW.created_at) IS DISTINCT FROM (OLD.tenant_id,OLD.message_id,OLD.conversation_id,OLD.turn_id,OLD.sequence,OLD.role,OLD.content,OLD.created_at) THEN
  RAISE EXCEPTION 'completed message is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER assistant_message_immutable BEFORE UPDATE ON investigation.assistant_messages FOR EACH ROW EXECUTE FUNCTION investigation.assistant_immutable_message();
-- Lock admin grants without giving either runtime role tenant mutation rights.
-- +goose StatementBegin
CREATE FUNCTION platform.assistant_lock_audit_authority(tid uuid,sub text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
DECLARE active boolean:=false;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid OR sub='' THEN RETURN false; END IF;
 PERFORM 1 FROM platform.tenants WHERE tenant_id=tid AND status='active' FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM 1 FROM platform.role_bindings WHERE tenant_id=tid AND subject=sub AND role_name='platform_admin' ORDER BY binding_id FOR SHARE;
 SELECT EXISTS(SELECT 1 FROM platform.role_bindings WHERE tenant_id=tid AND subject=sub AND role_name='platform_admin' AND status='active') INTO active;
 RETURN active;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.assistant_lock_audit_authority(uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.assistant_lock_audit_authority(uuid,text) TO api_runtime_role,worker_runtime_role;
RESET ROLE;
