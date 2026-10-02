-- +goose Up
SET ROLE schema_owner;

CREATE TABLE platform.resource_entities (
 tenant_id uuid NOT NULL REFERENCES platform.tenants(tenant_id),
 canonical_id text NOT NULL,
 cluster_id uuid,
 kind text NOT NULL,
 namespace text NOT NULL DEFAULT '',
 name text NOT NULL,
 metadata jsonb NOT NULL CHECK(jsonb_typeof(metadata)='object'),
 observed_at timestamptz NOT NULL,
 deleted_at timestamptz,
 PRIMARY KEY(tenant_id,canonical_id),
 FOREIGN KEY(tenant_id,cluster_id) REFERENCES platform.cluster_registrations(tenant_id,cluster_id)
);
CREATE TABLE platform.resource_aliases (
 tenant_id uuid NOT NULL,
 scope text NOT NULL,
 alias_kind text NOT NULL,
 alias_value text NOT NULL,
 canonical_id text NOT NULL,
 source_id uuid NOT NULL,
 provenance jsonb NOT NULL,
 PRIMARY KEY(tenant_id,scope,alias_kind,alias_value,canonical_id),
 FOREIGN KEY(tenant_id,canonical_id) REFERENCES platform.resource_entities(tenant_id,canonical_id),
 FOREIGN KEY(tenant_id,source_id) REFERENCES platform.source_registrations(tenant_id,source_id)
);
CREATE TABLE platform.graph_ownership (
 tenant_id uuid NOT NULL,
 cluster_uid text NOT NULL,
 owner_epoch bigint NOT NULL CHECK(owner_epoch>0),
 owner_instance text NOT NULL,
 owner_endpoint text NOT NULL,
 lease_uid text NOT NULL,
 route_expires_at timestamptz NOT NULL,
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,cluster_uid),
 FOREIGN KEY(tenant_id,cluster_uid) REFERENCES platform.cluster_registrations(tenant_id,cluster_uid)
);
CREATE TABLE platform.adapter_scope_verifications (
 tenant_id uuid NOT NULL,
 source_id uuid NOT NULL,
 source_revision bigint NOT NULL,
 binding_digest text NOT NULL CHECK(binding_digest ~ '^sha256:[0-9a-f]{64}$'),
 adapter_version text NOT NULL,
 verified_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 report_digest text NOT NULL CHECK(report_digest ~ '^sha256:[0-9a-f]{64}$'),
 PRIMARY KEY(tenant_id,source_id,source_revision,binding_digest),
 FOREIGN KEY(tenant_id,source_id) REFERENCES platform.source_registrations(tenant_id,source_id)
);
CREATE TABLE platform.evidence_metadata (
 tenant_id uuid NOT NULL,
 evidence_id uuid NOT NULL,
 source_id uuid NOT NULL,
 canonical_id text NOT NULL,
 namespace text NOT NULL,
 metadata jsonb NOT NULL CHECK(jsonb_typeof(metadata)='object'),
 content_digest text NOT NULL CHECK(content_digest ~ '^sha256:[0-9a-f]{64}$'),
 replay_state text NOT NULL CHECK(replay_state IN('source_available','archive_pending','archived_verified','unavailable')),
 retain_until timestamptz NOT NULL,
 legal_hold boolean NOT NULL DEFAULT false,
 deleting boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,evidence_id),
 FOREIGN KEY(tenant_id,source_id) REFERENCES platform.source_registrations(tenant_id,source_id)
);
CREATE TABLE platform.evidence_archive_intents (
 tenant_id uuid NOT NULL,
 evidence_id uuid NOT NULL,
 object_id uuid NOT NULL,
 backend_logical_id text NOT NULL,
 envelope jsonb,
 ciphertext_digest text,
 encryption_key_version text,
 object_ref jsonb,
 status text NOT NULL CHECK(status IN('pending','verified','deleting','deleted')),
 attempts integer NOT NULL DEFAULT 0,
 last_error text,
 verified_at timestamptz,
 PRIMARY KEY(tenant_id,evidence_id),
 UNIQUE(tenant_id,object_id),
 FOREIGN KEY(tenant_id,evidence_id) REFERENCES platform.evidence_metadata(tenant_id,evidence_id),
 CHECK(status<>'verified' OR (object_ref IS NOT NULL AND encryption_key_version IS NOT NULL AND ciphertext_digest IS NOT NULL AND verified_at IS NOT NULL))
);
CREATE TABLE platform.evidence_dependencies (
 tenant_id uuid NOT NULL,
 referrer_id uuid NOT NULL,
 dependency_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,referrer_id,dependency_id),
 FOREIGN KEY(tenant_id,referrer_id) REFERENCES platform.evidence_metadata(tenant_id,evidence_id),
 FOREIGN KEY(tenant_id,dependency_id) REFERENCES platform.evidence_metadata(tenant_id,evidence_id),
 CHECK(referrer_id<>dependency_id)
);
-- This is the SP-04 dependency-protection contract, not an SP-05/SP-07 reducer.
CREATE TABLE platform.evidence_retention_references (
 tenant_id uuid NOT NULL,
 reference_kind text NOT NULL CHECK(reference_kind IN('incident','rca','action','audit','archive')),
 reference_id uuid NOT NULL,
 evidence_id uuid NOT NULL,
 retain_until timestamptz NOT NULL,
 active boolean NOT NULL DEFAULT false,
 PRIMARY KEY(tenant_id,reference_kind,reference_id,evidence_id),
 FOREIGN KEY(tenant_id,evidence_id) REFERENCES platform.evidence_metadata(tenant_id,evidence_id)
);

-- +goose StatementBegin
DO $$
DECLARE tab text;
BEGIN
 FOREACH tab IN ARRAY ARRAY['resource_entities','resource_aliases','graph_ownership','adapter_scope_verifications','evidence_metadata','evidence_archive_intents','evidence_dependencies','evidence_retention_references'] LOOP
  EXECUTE format('ALTER TABLE platform.%I ENABLE ROW LEVEL SECURITY',tab);
  EXECUTE format('ALTER TABLE platform.%I FORCE ROW LEVEL SECURITY',tab);
  EXECUTE format('CREATE POLICY tenant_isolation ON platform.%I USING (tenant_id = NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',tab);
 END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT ON platform.resource_entities,platform.resource_aliases,platform.graph_ownership,platform.adapter_scope_verifications,platform.evidence_metadata,platform.evidence_archive_intents,platform.evidence_dependencies,platform.evidence_retention_references TO api_runtime_role;
GRANT SELECT,INSERT,UPDATE ON platform.resource_entities,platform.resource_aliases,platform.graph_ownership,platform.adapter_scope_verifications,platform.evidence_metadata,platform.evidence_archive_intents,platform.evidence_dependencies,platform.evidence_retention_references TO worker_runtime_role;
GRANT UPDATE(legal_hold) ON platform.evidence_metadata TO api_runtime_role;
GRANT DELETE ON platform.evidence_metadata,platform.evidence_archive_intents,platform.evidence_dependencies,platform.evidence_retention_references TO worker_runtime_role;
RESET ROLE;
