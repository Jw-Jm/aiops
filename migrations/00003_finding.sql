-- +goose Up
SET ROLE schema_owner;

CREATE TABLE finding.records (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  finding_id uuid NOT NULL,
  source_id uuid NOT NULL,
  cluster_id uuid,
  schema_version text NOT NULL,
  event_id text NOT NULL,
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  observed_at timestamptz,
  received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, finding_id),
  UNIQUE (tenant_id, source_id, event_id),
  FOREIGN KEY (tenant_id, source_id) REFERENCES platform.source_registrations (tenant_id, source_id),
  FOREIGN KEY (tenant_id, cluster_id) REFERENCES platform.cluster_registrations (tenant_id, cluster_id)
);
CREATE INDEX finding_records_tenant_received_idx ON finding.records (tenant_id, received_at DESC);

CREATE TABLE finding.inbox (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  event_id text NOT NULL,
  source_id uuid NOT NULL,
  request_digest text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
  state text NOT NULL CHECK (state IN ('accepted', 'duplicate', 'rejected')),
  finding_id uuid,
  received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, source_id, event_id),
  FOREIGN KEY (tenant_id, source_id) REFERENCES platform.source_registrations (tenant_id, source_id),
  FOREIGN KEY (tenant_id, finding_id) REFERENCES finding.records (tenant_id, finding_id)
);

CREATE TABLE finding.outbox (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  outbox_id uuid NOT NULL,
  finding_id uuid NOT NULL,
  event_type text NOT NULL,
  schema_version text NOT NULL,
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'claimed', 'delivered', 'deadletter')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  claim_token uuid,
  claimed_at timestamptz,
  delivered_at timestamptz,
  last_error_code text,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, outbox_id),
  FOREIGN KEY (tenant_id, finding_id) REFERENCES finding.records (tenant_id, finding_id)
);
CREATE INDEX finding_outbox_pending_idx ON finding.outbox (next_attempt_at, created_at) WHERE state IN ('pending', 'claimed');

GRANT USAGE ON SCHEMA finding TO api_runtime_role, worker_runtime_role;
GRANT SELECT, INSERT ON finding.records, finding.inbox, finding.outbox TO api_runtime_role;
GRANT SELECT, INSERT, UPDATE ON finding.records, finding.inbox, finding.outbox TO worker_runtime_role;
RESET ROLE;
