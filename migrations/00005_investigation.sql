-- +goose Up
SET ROLE schema_owner;

CREATE TABLE investigation.jobs (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  job_id uuid NOT NULL,
  incident_id uuid NOT NULL,
  trigger_revision bigint NOT NULL CHECK (trigger_revision > 0),
  policy_version_id uuid,
  state text NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'expired')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, job_id),
  UNIQUE (tenant_id, incident_id, trigger_revision),
  FOREIGN KEY (tenant_id, incident_id) REFERENCES incident.records (tenant_id, incident_id),
  FOREIGN KEY (tenant_id, policy_version_id) REFERENCES platform.registry_versions (tenant_id, version_id)
);

CREATE TABLE investigation.worker_queue (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  job_id uuid NOT NULL,
  lease_owner text,
  lease_token uuid,
  lease_expires_at timestamptz,
  heartbeat_at timestamptz,
  fencing_epoch bigint NOT NULL DEFAULT 0 CHECK (fencing_epoch >= 0),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, job_id),
  FOREIGN KEY (tenant_id, job_id) REFERENCES investigation.jobs (tenant_id, job_id)
);
CREATE INDEX investigation_worker_queue_claim_idx ON investigation.worker_queue (next_attempt_at, lease_expires_at);

GRANT USAGE ON SCHEMA investigation TO api_runtime_role, worker_runtime_role;
GRANT SELECT, INSERT, UPDATE ON investigation.jobs TO api_runtime_role;
GRANT INSERT ON investigation.worker_queue TO api_runtime_role;
GRANT SELECT, UPDATE ON investigation.jobs, investigation.worker_queue TO worker_runtime_role;
RESET ROLE;
