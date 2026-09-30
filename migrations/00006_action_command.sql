-- +goose Up
SET ROLE schema_owner;

CREATE TABLE action.risk_acknowledgements (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  acknowledgement_id uuid NOT NULL,
  subject text NOT NULL,
  target_canonical_id text NOT NULL,
  shell text NOT NULL,
  command_digest text NOT NULL CHECK (command_digest ~ '^sha256:[0-9a-f]{64}$'),
  risk_level text NOT NULL CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
  policy_version_id uuid,
  step_up_session_id uuid,
  confirmed_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  PRIMARY KEY (tenant_id, acknowledgement_id),
  FOREIGN KEY (tenant_id, policy_version_id) REFERENCES platform.registry_versions (tenant_id, version_id),
  FOREIGN KEY (tenant_id, step_up_session_id) REFERENCES platform.step_up_sessions (tenant_id, session_id),
  CHECK (expires_at > confirmed_at)
);

CREATE TABLE action.executions (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  execution_id uuid NOT NULL,
  subject text NOT NULL,
  target_canonical_id text NOT NULL,
  shell text NOT NULL,
  command_digest text NOT NULL CHECK (command_digest ~ '^sha256:[0-9a-f]{64}$'),
  risk_acknowledgement_id uuid NOT NULL,
  execution_profile_version_id uuid NOT NULL,
  idempotency_key_digest text NOT NULL CHECK (idempotency_key_digest ~ '^sha256:[0-9a-f]{64}$'),
  state text NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'execution_unknown')),
  command_archive_ref text,
  output_archive_ref text,
  exit_code integer,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, execution_id),
  UNIQUE (tenant_id, subject, idempotency_key_digest),
  FOREIGN KEY (tenant_id, risk_acknowledgement_id) REFERENCES action.risk_acknowledgements (tenant_id, acknowledgement_id),
  FOREIGN KEY (tenant_id, execution_profile_version_id) REFERENCES platform.execution_profile_versions (tenant_id, profile_version_id)
);

GRANT USAGE ON SCHEMA action TO api_runtime_role;
GRANT SELECT, INSERT, UPDATE ON action.risk_acknowledgements, action.executions TO api_runtime_role;
RESET ROLE;
