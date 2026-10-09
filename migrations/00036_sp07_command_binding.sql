-- +goose Up
SET ROLE schema_owner;
CREATE TABLE action.plans (
 tenant_id uuid NOT NULL,action_plan_id uuid NOT NULL,incident_id uuid NOT NULL,
 content jsonb NOT NULL,revision bigint NOT NULL DEFAULT 1,
 state text NOT NULL CHECK(state IN('suggested','accepted','dismissed','superseded')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,action_plan_id),
 FOREIGN KEY(tenant_id,incident_id) REFERENCES incident.records(tenant_id,incident_id)
);
CREATE TABLE action.assessments (
 tenant_id uuid NOT NULL,assessment_id uuid NOT NULL,subject text NOT NULL,
 binding jsonb NOT NULL,request_digest text NOT NULL CHECK(request_digest ~ '^sha256:[0-9a-f]{64}$'),risk jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '5 minutes',
 PRIMARY KEY(tenant_id,assessment_id),CHECK(expires_at>created_at AND expires_at<=created_at+interval '5 minutes')
);
ALTER TABLE action.risk_acknowledgements
 ADD COLUMN assessment_id uuid,
 ADD COLUMN binding jsonb,
 ADD COLUMN request_digest text,
 ADD CONSTRAINT acknowledgement_assessment FOREIGN KEY(tenant_id,assessment_id) REFERENCES action.assessments(tenant_id,assessment_id),
 ADD CONSTRAINT acknowledgement_single_assessment UNIQUE(tenant_id,assessment_id);
ALTER TABLE action.executions DROP CONSTRAINT executions_state_check;
ALTER TABLE action.executions ADD CONSTRAINT executions_state_check CHECK(state IN('queued','prepared','dispatching','running','succeeded','failed','cancelled','execution_unknown'));
ALTER TABLE action.executions
 ADD COLUMN binding jsonb,
 ADD COLUMN request_digest text,
 ADD COLUMN command_envelope jsonb,
 ADD COLUMN suggestion_match text CHECK(suggestion_match IN('exact','modified','unrelated','not_applicable')),
 ADD COLUMN comparator_version text,
 ADD COLUMN policy_decision_id text,
 ADD COLUMN actor_context jsonb,
 ADD COLUMN dispatch_token_digest text,
 ADD COLUMN claimed_at timestamptz,
 ADD COLUMN dispatch_deadline timestamptz,
 ADD COLUMN runner_ref text,
 ADD COLUMN event_seq bigint NOT NULL DEFAULT 0,
 ADD COLUMN output_seq bigint NOT NULL DEFAULT 0,
 ADD COLUMN output_bytes bigint NOT NULL DEFAULT 0 CHECK(output_bytes>=0),
 ADD COLUMN output_truncated boolean NOT NULL DEFAULT false,
 ADD COLUMN completed_at timestamptz,
 ADD COLUMN output_digest text,
 ADD COLUMN archived_at timestamptz,
 ADD COLUMN post_checked_at timestamptz,
 ADD COLUMN post_check text NOT NULL DEFAULT 'inconclusive' CHECK(post_check IN('resolved','not_resolved','inconclusive')),
 ADD COLUMN runner_termination_requested_at timestamptz,
 ADD COLUMN output_previews jsonb,
 ADD COLUMN cancellation_requested_at timestamptz;
CREATE UNIQUE INDEX execution_single_ack ON action.executions(tenant_id,risk_acknowledgement_id);
CREATE TABLE action.attempts (
 tenant_id uuid NOT NULL,execution_id uuid NOT NULL,attempt_no int NOT NULL CHECK(attempt_no=1),
 dispatch_token_digest text NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,execution_id,attempt_no),FOREIGN KEY(tenant_id,execution_id) REFERENCES action.executions(tenant_id,execution_id)
);
CREATE TABLE action.output_chunks (
 tenant_id uuid NOT NULL,execution_id uuid NOT NULL,seq bigint NOT NULL CHECK(seq>0),
 stream text NOT NULL CHECK(stream IN('stdout','stderr')),bytes bytea NOT NULL CHECK(octet_length(bytes)<=16384),
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,execution_id,seq),FOREIGN KEY(tenant_id,execution_id) REFERENCES action.executions(tenant_id,execution_id)
);
CREATE TABLE action.events (
 tenant_id uuid NOT NULL,execution_id uuid NOT NULL,event_seq bigint NOT NULL CHECK(event_seq>0),
 event_type text NOT NULL,payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,execution_id,event_seq),FOREIGN KEY(tenant_id,execution_id) REFERENCES action.executions(tenant_id,execution_id)
);
-- +goose StatementBegin
DO $$ DECLARE name text; BEGIN
 FOREACH name IN ARRAY ARRAY['plans','assessments','attempts','output_chunks','events'] LOOP
 EXECUTE format('ALTER TABLE action.%I ENABLE ROW LEVEL SECURITY',name);
 EXECUTE format('ALTER TABLE action.%I FORCE ROW LEVEL SECURITY',name);
 EXECUTE format('CREATE POLICY tenant_isolation ON action.%I USING(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',name);
 END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT,INSERT ON action.plans,action.assessments,action.attempts,action.output_chunks,action.events TO api_runtime_role,worker_runtime_role;
GRANT UPDATE ON action.plans TO api_runtime_role;
GRANT USAGE ON SCHEMA action TO worker_runtime_role;
GRANT SELECT,UPDATE ON action.executions,action.risk_acknowledgements TO worker_runtime_role;
GRANT SELECT ON action.risk_acknowledgements TO api_runtime_role;
GRANT DELETE ON action.output_chunks TO worker_runtime_role;
RESET ROLE;
SET ROLE schema_owner;
GRANT UPDATE(status) ON platform.execution_profile_versions TO api_runtime_role;
CREATE UNIQUE INDEX execution_profile_public_identity ON platform.execution_profile_versions(tenant_id,(content->>'executionProfileId'),version_number) WHERE content ? 'executionProfileId';
RESET ROLE;
SET ROLE schema_owner;
-- +goose StatementBegin
CREATE FUNCTION action.lock_target(tid uuid,target text,cuid text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false;END IF;
 PERFORM 1 FROM platform.resource_entities e JOIN platform.cluster_registrations c USING(tenant_id,cluster_id)
 WHERE e.tenant_id=tid AND e.canonical_id=target AND e.deleted_at IS NULL AND c.cluster_uid=cuid AND c.status='active' FOR SHARE OF e,c;
 RETURN FOUND;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION action.lock_target(uuid,text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION action.lock_target(uuid,text,text) TO api_runtime_role,worker_runtime_role;
RESET ROLE;
SET ROLE schema_owner;
CREATE TABLE action.output_archives (
 tenant_id uuid NOT NULL,execution_id uuid NOT NULL,envelope jsonb,
 plaintext_digest text NOT NULL,object_ref jsonb,retain_until timestamptz NOT NULL,verified_at timestamptz,
 PRIMARY KEY(tenant_id,execution_id),FOREIGN KEY(tenant_id,execution_id) REFERENCES action.executions(tenant_id,execution_id)
);
ALTER TABLE action.output_archives ENABLE ROW LEVEL SECURITY;
ALTER TABLE action.output_archives FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON action.output_archives USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT ON action.output_archives TO api_runtime_role;
GRANT SELECT,INSERT,UPDATE ON action.output_archives TO worker_runtime_role;
RESET ROLE;
SET ROLE schema_owner;
CREATE TABLE action.host_onboardings (
 tenant_id uuid NOT NULL,target text NOT NULL,principal text NOT NULL,report_digest text NOT NULL,
 report jsonb NOT NULL,expires_at timestamptz NOT NULL,created_by text NOT NULL,
 PRIMARY KEY(tenant_id,target,principal,report_digest)
);
ALTER TABLE action.host_onboardings ENABLE ROW LEVEL SECURITY;
ALTER TABLE action.host_onboardings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON action.host_onboardings USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT ON action.host_onboardings TO api_runtime_role,worker_runtime_role;
-- +goose StatementBegin
CREATE FUNCTION action.onboard_host(tid uuid,r jsonb,actor text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform,action AS $$
DECLARE cid uuid; target_id text; uid text; BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false;END IF;
 SELECT cluster_id INTO cid FROM platform.cluster_registrations WHERE tenant_id=tid AND cluster_uid=r->>'clusterUid' AND status='active' FOR SHARE;
 IF cid IS NULL THEN RETURN false;END IF;
 target_id:=r->'host'->>'Target'; uid:=r->'host'->>'UID';
 INSERT INTO action.host_onboardings(tenant_id,target,principal,report_digest,report,expires_at,created_by)
 VALUES(tid,target_id,r->'host'->>'Principal',r->>'reportDigest',r,(r->>'expiresAt')::timestamptz,actor);
 INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at)
 VALUES(tid,target_id,cid,'System','',uid,jsonb_build_object('status','matched','sourceValues',jsonb_build_object('uuid',uid)),(r->>'observedAt')::timestamptz)
 ON CONFLICT(tenant_id,canonical_id) DO NOTHING;
 RETURN EXISTS(SELECT 1 FROM platform.resource_entities WHERE tenant_id=tid AND canonical_id=target_id AND cluster_id=cid AND metadata->'sourceValues'->>'uuid'=uid AND deleted_at IS NULL);
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION action.onboard_host(uuid,jsonb,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION action.onboard_host(uuid,jsonb,text) TO api_runtime_role;
RESET ROLE;
