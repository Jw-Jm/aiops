-- +goose Up
SET ROLE schema_owner;

CREATE TABLE incident.records (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  incident_id uuid NOT NULL,
  state text NOT NULL CHECK (state IN ('open', 'acknowledged', 'investigating', 'resolved', 'closed')),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  summary text NOT NULL DEFAULT '',
  payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, incident_id)
);
CREATE INDEX incident_records_tenant_state_idx ON incident.records (tenant_id, state, updated_at DESC);

CREATE TABLE incident.finding_links (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  incident_id uuid NOT NULL,
  finding_id uuid NOT NULL,
  linked_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, incident_id, finding_id),
  FOREIGN KEY (tenant_id, incident_id) REFERENCES incident.records (tenant_id, incident_id),
  FOREIGN KEY (tenant_id, finding_id) REFERENCES finding.records (tenant_id, finding_id)
);

GRANT USAGE ON SCHEMA incident TO api_runtime_role, worker_runtime_role;
GRANT SELECT ON incident.records, incident.finding_links TO api_runtime_role;
GRANT SELECT, INSERT, UPDATE ON incident.records, incident.finding_links TO worker_runtime_role;
RESET ROLE;
