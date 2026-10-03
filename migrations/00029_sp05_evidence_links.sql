-- +goose Up
SET ROLE schema_owner;
CREATE TABLE finding.evidence_refs (
 tenant_id uuid NOT NULL,finding_id uuid NOT NULL,evidence_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,finding_id,evidence_id),
 FOREIGN KEY(tenant_id,finding_id) REFERENCES finding.records(tenant_id,finding_id),
 FOREIGN KEY(tenant_id,evidence_id) REFERENCES platform.evidence_metadata(tenant_id,evidence_id)
);
ALTER TABLE finding.evidence_refs ENABLE ROW LEVEL SECURITY;
ALTER TABLE finding.evidence_refs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON finding.evidence_refs USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
ALTER TABLE platform.evidence_retention_references DROP CONSTRAINT evidence_retention_references_reference_kind_check;
ALTER TABLE platform.evidence_retention_references ADD CONSTRAINT evidence_retention_references_reference_kind_check CHECK(reference_kind IN('incident','rca','action','audit','archive','finding'));
GRANT SELECT,INSERT ON finding.evidence_refs TO api_runtime_role,worker_runtime_role;
-- Read-only runtime roles need a narrow row-lock fence, not UPDATE permission
-- on registrations. Revocation/credential rotation serialize with this fence.
-- +goose StatementBegin
CREATE FUNCTION platform.sp05_lock_evidence_source(tid uuid,sid uuid,rev bigint) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
DECLARE valid boolean;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false; END IF;
 PERFORM 1 FROM platform.tenants WHERE tenant_id=tid FOR SHARE;
 PERFORM 1 FROM platform.source_registrations WHERE tenant_id=tid AND source_id=sid FOR SHARE;
 PERFORM 1 FROM platform.cluster_registrations c JOIN platform.source_registrations s USING(tenant_id,cluster_id) WHERE s.tenant_id=tid AND s.source_id=sid FOR SHARE OF c;
 SELECT EXISTS(SELECT 1 FROM platform.source_registrations s JOIN platform.tenants t USING(tenant_id) LEFT JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE s.tenant_id=tid AND s.source_id=sid AND s.revision=rev AND s.status='active' AND t.status='active' AND (c.cluster_id IS NULL OR c.status='active')) INTO valid;
 RETURN valid;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.sp05_lock_evidence_source(uuid,uuid,bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.sp05_lock_evidence_source(uuid,uuid,bigint) TO api_runtime_role,worker_runtime_role;
RESET ROLE;
