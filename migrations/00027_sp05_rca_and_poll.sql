-- +goose Up
SET ROLE schema_owner;
CREATE TABLE finding.poll_occurrences (
 tenant_id uuid NOT NULL,source_id uuid NOT NULL,signal_key text NOT NULL,
 occurrence_id uuid NOT NULL,starts_at timestamptz NOT NULL,active boolean NOT NULL,
 PRIMARY KEY(tenant_id,source_id,signal_key),FOREIGN KEY(tenant_id,source_id) REFERENCES platform.source_registrations(tenant_id,source_id)
);
CREATE TABLE incident.rca_revisions (
 tenant_id uuid NOT NULL,incident_id uuid NOT NULL,revision bigint NOT NULL CHECK(revision>0),
 evaluation_key text NOT NULL,input_digest text NOT NULL,base_incident_revision bigint NOT NULL,
 recipe_version_id uuid,recipe_digest text NOT NULL,actor text NOT NULL,source text NOT NULL CHECK(source IN('deterministic','pyrca','agent','manual')),
 provenance jsonb NOT NULL,input_manifest jsonb NOT NULL,result jsonb NOT NULL,
 superseded boolean NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,incident_id,revision),UNIQUE(tenant_id,incident_id,evaluation_key),
 FOREIGN KEY(tenant_id,incident_id) REFERENCES incident.records(tenant_id,incident_id),
 FOREIGN KEY(tenant_id,recipe_version_id) REFERENCES platform.registry_versions(tenant_id,version_id)
);
CREATE TABLE incident.rca_evidence_refs (
 tenant_id uuid NOT NULL,incident_id uuid NOT NULL,revision bigint NOT NULL,evidence_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,incident_id,revision,evidence_id),
 FOREIGN KEY(tenant_id,incident_id,revision) REFERENCES incident.rca_revisions(tenant_id,incident_id,revision),
 FOREIGN KEY(tenant_id,evidence_id) REFERENCES platform.evidence_metadata(tenant_id,evidence_id)
);
-- +goose StatementBegin
DO $$
DECLARE item text;
BEGIN
 FOREACH item IN ARRAY ARRAY['finding.poll_occurrences','incident.rca_revisions','incident.rca_evidence_refs'] LOOP
 EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY',item);
 EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY',item);
 EXECUTE format('CREATE POLICY tenant_isolation ON %s USING(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',item);
 END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT ON finding.poll_occurrences,incident.rca_revisions,incident.rca_evidence_refs TO api_runtime_role;
GRANT SELECT,INSERT,UPDATE ON finding.poll_occurrences TO worker_runtime_role;
GRANT SELECT,INSERT ON incident.rca_revisions,incident.rca_evidence_refs TO worker_runtime_role;
-- API manual transitions are guarded by current operator scope/revision/audit.
GRANT SELECT,INSERT,UPDATE ON incident.records,incident.finding_links,incident.correlation_subjects,incident.outbox TO api_runtime_role;
GRANT INSERT ON incident.timeline TO api_runtime_role;
RESET ROLE;
