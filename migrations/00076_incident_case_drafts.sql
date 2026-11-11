-- +goose Up
SET ROLE schema_owner;
CREATE TABLE incident.case_drafts (
  tenant_id uuid NOT NULL,
  document_id uuid NOT NULL,
  incident_id uuid NOT NULL,
  incident_revision bigint NOT NULL CHECK (incident_revision > 0),
  incident_state text NOT NULL,
  title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 160),
  summary text NOT NULL CHECK (length(btrim(summary)) BETWEEN 1 AND 500),
  content text NOT NULL CHECK (length(btrim(content)) BETWEEN 1 AND 16000),
  creator_subject text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, document_id),
  FOREIGN KEY (tenant_id, incident_id) REFERENCES incident.records (tenant_id, incident_id)
);
CREATE INDEX incident_case_drafts_origin ON incident.case_drafts (tenant_id, incident_id, document_id);
ALTER TABLE incident.case_drafts ENABLE ROW LEVEL SECURITY;
ALTER TABLE incident.case_drafts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON incident.case_drafts USING (tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK (tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT, INSERT ON incident.case_drafts TO api_runtime_role;
RESET ROLE;
