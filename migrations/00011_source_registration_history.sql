-- +goose Up
SET ROLE schema_owner;

CREATE TABLE platform.cluster_registration_revisions (
  tenant_id uuid NOT NULL,
  cluster_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  cluster_uid text NOT NULL CHECK (length(cluster_uid) BETWEEN 1 AND 512),
  display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 200),
  status text NOT NULL CHECK (status IN ('active', 'disabled')),
  actor_subject text NOT NULL CHECK (length(actor_subject) BETWEEN 1 AND 512),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, cluster_id, revision),
  FOREIGN KEY (tenant_id, cluster_id) REFERENCES platform.cluster_registrations (tenant_id, cluster_id)
);

CREATE INDEX cluster_registrations_tenant_status_idx
  ON platform.cluster_registrations (tenant_id, status, cluster_uid);
CREATE INDEX source_registrations_tenant_status_idx
  ON platform.source_registrations (tenant_id, status, source_type, instance_key);

-- +goose StatementBegin
CREATE FUNCTION platform.keep_cluster_uid_immutable() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.cluster_uid IS DISTINCT FROM OLD.cluster_uid THEN
    RAISE EXCEPTION 'cluster_uid is an immutable tenant-scoped identity' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER cluster_uid_immutable
  BEFORE UPDATE OF cluster_uid ON platform.cluster_registrations
  FOR EACH ROW EXECUTE FUNCTION platform.keep_cluster_uid_immutable();

ALTER TABLE platform.cluster_registration_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform.cluster_registration_revisions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON platform.cluster_registration_revisions
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT ON platform.cluster_registration_revisions TO api_runtime_role, worker_runtime_role;
GRANT USAGE ON SCHEMA platform TO api_runtime_role, worker_runtime_role;
REVOKE UPDATE, DELETE, TRUNCATE ON platform.cluster_registration_revisions FROM PUBLIC, api_runtime_role, worker_runtime_role, ops_readonly_role;
REVOKE UPDATE, DELETE, TRUNCATE ON platform.source_registration_revisions FROM PUBLIC, api_runtime_role, worker_runtime_role, ops_readonly_role;

RESET ROLE;
