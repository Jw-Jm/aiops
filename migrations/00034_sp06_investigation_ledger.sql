-- +goose Up
SET ROLE schema_owner;

ALTER TABLE investigation.jobs DROP CONSTRAINT jobs_tenant_id_incident_id_trigger_revision_key;
ALTER TABLE investigation.jobs DROP CONSTRAINT jobs_state_check;
ALTER TABLE investigation.jobs ADD CONSTRAINT jobs_state_check CHECK(state IN('queued','running','succeeded','partial','failed','cancelled','expired'));
ALTER TABLE investigation.jobs
 ADD COLUMN schema_version text NOT NULL DEFAULT 'investigation-job/v1',
 ADD COLUMN requesting_subject text NOT NULL DEFAULT '',
 ADD COLUMN trigger_kind text NOT NULL DEFAULT 'legacy',
 ADD COLUMN policy_version text NOT NULL DEFAULT '',
 ADD COLUMN effective_scope jsonb NOT NULL DEFAULT '{}',
 ADD COLUMN effective_scope_digest text NOT NULL DEFAULT '',
 ADD COLUMN tool_catalog_digest text NOT NULL DEFAULT '',
 ADD COLUMN budget jsonb NOT NULL DEFAULT '{}',
 ADD COLUMN budget_reserved jsonb NOT NULL DEFAULT '{}',
 ADD COLUMN budget_consumed jsonb NOT NULL DEFAULT '{}',
 ADD COLUMN expires_at timestamptz,
 ADD COLUMN next_call_seq bigint NOT NULL DEFAULT 0 CHECK(next_call_seq>=0),
 ADD COLUMN event_seq bigint NOT NULL DEFAULT 0 CHECK(event_seq>=0),
 ADD COLUMN result jsonb,
 ADD COLUMN error_code text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX investigation_job_identity ON investigation.jobs
 (tenant_id,incident_id,policy_version,trigger_revision,trigger_kind,requesting_subject,effective_scope_digest);

CREATE TABLE investigation.steps (
 tenant_id uuid NOT NULL, job_id uuid NOT NULL, step_id uuid NOT NULL,
 tool_name text NOT NULL, args_digest text NOT NULL,
 state text NOT NULL CHECK(state IN('planned','running','succeeded','failed','skipped','cancelled')),
 lease_token uuid NOT NULL, lease_generation bigint NOT NULL CHECK(lease_generation>0),
 retry_count integer NOT NULL DEFAULT 0,
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(), completed_at timestamptz,
 result jsonb, result_digest text, error_code text NOT NULL DEFAULT '',
 retain_until timestamptz NOT NULL DEFAULT clock_timestamp()+interval '365 days',
 PRIMARY KEY(tenant_id,job_id,step_id),
 FOREIGN KEY(tenant_id,job_id) REFERENCES investigation.jobs(tenant_id,job_id)
);
CREATE TABLE investigation.admissions (
 tenant_id uuid NOT NULL, job_id uuid NOT NULL, admission_id uuid NOT NULL,
 step_id uuid NOT NULL, context_id uuid NOT NULL,
 call_seq bigint CHECK(call_seq>0), call_jti text CHECK(length(call_jti) BETWEEN 1 AND 128),
 lease_generation bigint NOT NULL CHECK(lease_generation>0),
 reservation jsonb NOT NULL, consumed jsonb,
 state text NOT NULL CHECK(state IN('reserved','settled','unknown')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,job_id,admission_id),
 CHECK((call_seq IS NULL)<>(call_jti IS NULL)),
 UNIQUE(tenant_id,job_id,call_seq), UNIQUE(tenant_id,job_id,call_jti),
 FOREIGN KEY(tenant_id,job_id,step_id) REFERENCES investigation.steps(tenant_id,job_id,step_id)
);
CREATE TABLE investigation.replay_ledger (
 tenant_id uuid NOT NULL, context_id uuid NOT NULL, nonce_kind text NOT NULL CHECK(nonce_kind IN('seq','jti')),
 nonce_value text NOT NULL, job_id uuid NOT NULL, step_id uuid NOT NULL, args_digest text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,context_id,nonce_kind,nonce_value),
 FOREIGN KEY(tenant_id,job_id,step_id) REFERENCES investigation.steps(tenant_id,job_id,step_id)
);
CREATE TABLE investigation.events (
 tenant_id uuid NOT NULL,job_id uuid NOT NULL,event_seq bigint NOT NULL CHECK(event_seq>0),
 event_type text NOT NULL,payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,job_id,event_seq),
 FOREIGN KEY(tenant_id,job_id) REFERENCES investigation.jobs(tenant_id,job_id)
);
CREATE TABLE investigation.call_allocations (
 tenant_id uuid NOT NULL,job_id uuid NOT NULL,step_id uuid NOT NULL,jti text NOT NULL,
 tool_name text NOT NULL,args_digest text NOT NULL,lease_generation bigint NOT NULL,
 model boolean NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,job_id,jti),
 FOREIGN KEY(tenant_id,job_id) REFERENCES investigation.jobs(tenant_id,job_id)
);
CREATE TABLE investigation.request_keys (
 tenant_id uuid NOT NULL,subject text NOT NULL,idempotency_key text NOT NULL,request_digest text NOT NULL,job_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,subject,idempotency_key),FOREIGN KEY(tenant_id,job_id) REFERENCES investigation.jobs(tenant_id,job_id)
);
CREATE TABLE investigation.contexts (
 tenant_id uuid NOT NULL,job_id uuid NOT NULL,context_id uuid NOT NULL,
 audience text NOT NULL,session_nonce text NOT NULL,claims_digest text NOT NULL,
 lease_generation bigint NOT NULL,expires_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,context_id),
 FOREIGN KEY(tenant_id,job_id) REFERENCES investigation.jobs(tenant_id,job_id)
);

-- +goose StatementBegin
DO $$ DECLARE name text; BEGIN
 FOREACH name IN ARRAY ARRAY['steps','admissions','replay_ledger','events','call_allocations','contexts','request_keys'] LOOP
  EXECUTE format('ALTER TABLE investigation.%I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE investigation.%I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY tenant_isolation ON investigation.%I USING(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',name);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION investigation.protect_success() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF OLD.state='succeeded' THEN RAISE EXCEPTION 'committed investigation step is immutable'; END IF; RETURN NEW; END $$;
-- +goose StatementEnd
CREATE TRIGGER investigation_success_immutable BEFORE UPDATE OR DELETE ON investigation.steps
 FOR EACH ROW EXECUTE FUNCTION investigation.protect_success();

GRANT SELECT,INSERT,UPDATE ON investigation.jobs,
 investigation.steps,investigation.admissions TO api_runtime_role,worker_runtime_role;
GRANT INSERT ON investigation.worker_queue TO api_runtime_role;
GRANT SELECT,INSERT,UPDATE ON investigation.worker_queue TO worker_runtime_role;
GRANT SELECT,INSERT ON investigation.replay_ledger,investigation.events TO api_runtime_role,worker_runtime_role;
GRANT SELECT,INSERT ON investigation.call_allocations,investigation.contexts,investigation.request_keys TO api_runtime_role,worker_runtime_role;

-- Narrow read-only definer capability: a runtime may fence authority without
-- gaining UPDATE on grants, tenant identity or source registrations.
-- +goose StatementBegin
CREATE FUNCTION platform.sp06_lock_authority(tid uuid,sub text,cuid text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
DECLARE cid uuid;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false; END IF;
 PERFORM 1 FROM platform.tenants WHERE tenant_id=tid FOR SHARE;
 SELECT cluster_id INTO cid FROM platform.cluster_registrations WHERE tenant_id=tid AND cluster_uid=cuid FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM 1 FROM platform.role_bindings WHERE tenant_id=tid AND subject=sub AND role_name='operator' ORDER BY binding_id FOR SHARE;
 PERFORM 1 FROM platform.source_registrations WHERE tenant_id=tid AND(cluster_id=cid OR data_scope_mapping->'scopes'->'cluster' ? cuid) ORDER BY source_id FOR SHARE;
 RETURN true;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.sp06_lock_authority(uuid,text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.sp06_lock_authority(uuid,text,text) TO api_runtime_role,worker_runtime_role;
ALTER TABLE platform.evidence_retention_references DROP CONSTRAINT evidence_retention_references_reference_kind_check;
ALTER TABLE platform.evidence_retention_references ADD CONSTRAINT evidence_retention_references_reference_kind_check CHECK(reference_kind IN('incident','rca','action','audit','archive','finding','investigation'));
-- +goose StatementBegin
CREATE FUNCTION platform.sp06_lock_policy(tid uuid,vid uuid,cuid text,namespaces text[]) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
DECLARE name text;cid uuid;active uuid;ns text;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false;END IF;
 SELECT logical_name INTO name FROM platform.registry_versions WHERE tenant_id=tid AND version_id=vid AND kind='policy' AND retired_at IS NULL FOR SHARE;
 IF NOT FOUND THEN RETURN false;END IF;
 SELECT cluster_id INTO cid FROM platform.cluster_registrations WHERE tenant_id=tid AND cluster_uid=cuid;
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(tid::text||'|policy|'||name||'|tenant|00000000-0000-0000-0000-000000000000|',0));
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(tid::text||'|policy|'||name||'|cluster|'||cid::text||'|',0));
 SELECT h.version_id INTO active FROM platform.registry_activation_history h JOIN platform.registry_versions v ON v.tenant_id=h.tenant_id AND v.version_id=h.version_id WHERE h.tenant_id=tid AND h.kind='policy' AND h.logical_name=name AND h.activated_at<=clock_timestamp() AND v.retired_at IS NULL AND(h.scope_type='tenant' OR(h.scope_type='cluster' AND h.cluster_id=cid)) ORDER BY CASE h.scope_type WHEN 'cluster' THEN 2 ELSE 1 END DESC,h.activated_at DESC,h.revision DESC LIMIT 1;
 IF active IS DISTINCT FROM vid THEN RETURN false; END IF;
 FOREACH ns IN ARRAY COALESCE(namespaces,ARRAY[]::text[]) LOOP
  PERFORM pg_advisory_xact_lock_shared(hashtextextended(tid::text||'|policy|'||name||'|namespace|'||cid::text||'|'||ns,0));
  SELECT h.version_id INTO active FROM platform.registry_activation_history h JOIN platform.registry_versions v ON v.tenant_id=h.tenant_id AND v.version_id=h.version_id WHERE h.tenant_id=tid AND h.kind='policy' AND h.logical_name=name AND h.activated_at<=clock_timestamp() AND v.retired_at IS NULL AND(h.scope_type='tenant' OR(h.cluster_id=cid AND (h.scope_type='cluster' OR(h.scope_type='namespace' AND h.namespace=ns)))) ORDER BY CASE h.scope_type WHEN 'namespace' THEN 3 WHEN 'cluster' THEN 2 ELSE 1 END DESC,h.activated_at DESC,h.revision DESC LIMIT 1;
  IF active IS DISTINCT FROM vid THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.sp06_lock_policy(uuid,uuid,text,text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.sp06_lock_policy(uuid,uuid,text,text[]) TO api_runtime_role,worker_runtime_role;
GRANT EXECUTE ON FUNCTION platform.sp05_lock_recipe(uuid,uuid) TO api_runtime_role;
RESET ROLE;

SET ROLE schema_owner;
-- +goose StatementBegin
CREATE FUNCTION platform.sp06_read_fence(tid uuid,jid uuid)
RETURNS TABLE(lease_token uuid,fencing_epoch bigint,lease_expires_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,investigation AS $$
 SELECT q.lease_token,q.fencing_epoch,q.lease_expires_at FROM investigation.worker_queue q
 WHERE tid=NULLIF(current_setting('app.tenant_id',true),'')::uuid AND q.tenant_id=tid AND q.job_id=jid
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION platform.sp06_rotate_fence(tid uuid,jid uuid,token uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,investigation AS $$
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid OR token IS NULL THEN RETURN false; END IF;
 PERFORM 1 FROM investigation.jobs WHERE tenant_id=tid AND job_id=jid AND state IN('queued','running') FOR UPDATE;
 IF NOT FOUND THEN RETURN false;END IF;
 UPDATE investigation.worker_queue SET fencing_epoch=fencing_epoch+1,lease_token=token,lease_expires_at=clock_timestamp() WHERE tenant_id=tid AND job_id=jid;
 RETURN FOUND;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.sp06_read_fence(uuid,uuid),platform.sp06_rotate_fence(uuid,uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.sp06_read_fence(uuid,uuid),platform.sp06_rotate_fence(uuid,uuid,uuid) TO api_runtime_role,worker_runtime_role;
RESET ROLE;
