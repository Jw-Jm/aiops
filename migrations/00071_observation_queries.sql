-- +goose Up
SET ROLE schema_owner;
CREATE TABLE investigation.observation_queries (
 tenant_id uuid NOT NULL REFERENCES platform.tenants(tenant_id), query_id uuid NOT NULL,
 owner_subject text NOT NULL, request_key uuid NOT NULL, cluster_uid text NOT NULL, session_id uuid NOT NULL,
 request jsonb NOT NULL, scope jsonb NOT NULL, view jsonb NOT NULL, result jsonb,
 state text NOT NULL CHECK(state IN('accepted','running','completed','failed','cancel_requested','terminated','expired','denied')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 lease_token uuid, lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '30 days',
 PRIMARY KEY(tenant_id,query_id), UNIQUE(tenant_id,owner_subject,request_key)
);
CREATE INDEX observation_owner_history ON investigation.observation_queries(tenant_id,owner_subject,created_at DESC,query_id);
CREATE INDEX observation_work ON investigation.observation_queries(tenant_id,cluster_uid,state,created_at) WHERE state IN('accepted','running','cancel_requested');
CREATE UNIQUE INDEX observation_one_active ON investigation.observation_queries(tenant_id,owner_subject) WHERE state IN('accepted','running','cancel_requested');
ALTER TABLE investigation.observation_queries ENABLE ROW LEVEL SECURITY;
ALTER TABLE investigation.observation_queries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON investigation.observation_queries
 USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid)
 WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT,INSERT,UPDATE ON investigation.observation_queries TO worker_runtime_role;
-- +goose StatementBegin
CREATE FUNCTION investigation.observation_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.tenant_id,NEW.query_id,NEW.owner_subject,NEW.request_key,NEW.cluster_uid,NEW.session_id,NEW.request,NEW.scope,NEW.created_at,NEW.expires_at)
 IS DISTINCT FROM (OLD.tenant_id,OLD.query_id,OLD.owner_subject,OLD.request_key,OLD.cluster_uid,OLD.session_id,OLD.request,OLD.scope,OLD.created_at,OLD.expires_at)
 OR (OLD.result IS NOT NULL AND NEW.result IS DISTINCT FROM OLD.result) THEN
  RAISE EXCEPTION 'manual query identity and published result are immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER observation_immutable BEFORE UPDATE ON investigation.observation_queries FOR EACH ROW EXECUTE FUNCTION investigation.observation_immutable();
-- The read-only Worker may pin a source revision without UPDATE privileges.
-- +goose StatementBegin
CREATE FUNCTION platform.observation_lock_source(tid uuid,sid uuid,rev bigint,cred bigint,cuid text,ns text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
DECLARE s platform.source_registrations%ROWTYPE;c platform.cluster_registrations%ROWTYPE;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false; END IF;
 SELECT * INTO s FROM platform.source_registrations WHERE tenant_id=tid AND source_id=sid FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT * INTO c FROM platform.cluster_registrations WHERE tenant_id=tid AND cluster_id=s.cluster_id FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 RETURN s.status='active' AND s.source_type='victorialogs' AND c.status='active'
 AND s.revision=rev AND s.credential_revision=cred AND c.cluster_uid=cuid
 AND s.data_scope_mapping->'scopes'->'cluster' ? cuid
 AND s.data_scope_mapping->'scopes'->'namespace' ? ns;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.observation_lock_source(uuid,uuid,bigint,bigint,text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.observation_lock_source(uuid,uuid,bigint,bigint,text,text) TO worker_runtime_role;
RESET ROLE;
