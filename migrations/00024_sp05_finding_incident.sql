-- +goose Up
SET ROLE schema_owner;
ALTER TABLE finding.records ADD COLUMN source_fingerprint text, ADD COLUMN occurrence_id text,
 ADD COLUMN aggregate_revision bigint NOT NULL DEFAULT 1 CHECK(aggregate_revision>0),
 ADD COLUMN lifecycle_state text CHECK(lifecycle_state IN('firing','resolved')),
 ADD COLUMN resource_canonical_id text, ADD COLUMN namespace text NOT NULL DEFAULT '',
 ADD COLUMN cluster_uid text, ADD COLUMN source_sequence bigint NOT NULL DEFAULT 0 CHECK(source_sequence>=0),
 ADD COLUMN semantic_digest text;
CREATE UNIQUE INDEX finding_occurrence_unique ON finding.records(tenant_id,source_fingerprint,occurrence_id);
CREATE TABLE finding.transport_keys (
 tenant_id uuid NOT NULL,source_id uuid NOT NULL,key_kind text NOT NULL CHECK(key_kind IN('event','idempotency')),key_value text NOT NULL,
 digest text NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),finding_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,source_id,key_kind,key_value),
 FOREIGN KEY(tenant_id,source_id) REFERENCES platform.source_registrations(tenant_id,source_id),
 FOREIGN KEY(tenant_id,finding_id) REFERENCES finding.records(tenant_id,finding_id)
);
CREATE TABLE finding.timeline (
 tenant_id uuid NOT NULL,finding_id uuid NOT NULL,event_id text NOT NULL,source_id uuid NOT NULL,
 disposition text NOT NULL CHECK(disposition IN('accepted','stale')),aggregate_revision bigint NOT NULL,
 digest text NOT NULL,observed_at timestamptz NOT NULL,received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,source_id,event_id),FOREIGN KEY(tenant_id,finding_id) REFERENCES finding.records(tenant_id,finding_id),
 FOREIGN KEY(tenant_id,source_id) REFERENCES platform.source_registrations(tenant_id,source_id)
);
CREATE TABLE finding.rejections (
 tenant_id uuid NOT NULL,source_id uuid NOT NULL,digest text NOT NULL,error_code text NOT NULL,
 received_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(tenant_id,source_id,digest),
 FOREIGN KEY(tenant_id,source_id) REFERENCES platform.source_registrations(tenant_id,source_id)
);
ALTER TABLE finding.outbox ADD COLUMN aggregate_revision bigint NOT NULL DEFAULT 1;
CREATE TABLE incident.correlation_subjects (
 tenant_id uuid NOT NULL REFERENCES platform.tenants(tenant_id),fingerprint text NOT NULL,
 PRIMARY KEY(tenant_id,fingerprint)
);
-- Keep legacy investigating rows readable; the SP05 whitelist never emits it.
ALTER TABLE incident.records DROP CONSTRAINT records_state_check;
ALTER TABLE incident.records ADD CONSTRAINT records_state_check CHECK(state IN('open','acknowledged','investigating','mitigating','suppressed','resolved','closed'));
ALTER TABLE incident.records ADD COLUMN fingerprint text, ADD COLUMN policy_version text,
 ADD COLUMN cluster_uid text, ADD COLUMN resource_canonical_id text,ADD COLUMN namespace text NOT NULL DEFAULT '',
 ADD COLUMN resolved_at timestamptz, ADD COLUMN recovery_known_at timestamptz,
 ADD COLUMN current_rca_revision bigint NOT NULL DEFAULT 0,ADD COLUMN suppressed_until timestamptz;
CREATE INDEX incident_correlation_idx ON incident.records(tenant_id,fingerprint,created_at DESC);
CREATE TABLE incident.inbox (
 tenant_id uuid NOT NULL,consumer_name text NOT NULL,event_id uuid NOT NULL,
 finding_id uuid NOT NULL,aggregate_revision bigint NOT NULL,incident_id uuid,
 disposition text NOT NULL,PRIMARY KEY(tenant_id,consumer_name,event_id),
 FOREIGN KEY(tenant_id,finding_id) REFERENCES finding.records(tenant_id,finding_id),
 FOREIGN KEY(tenant_id,incident_id) REFERENCES incident.records(tenant_id,incident_id)
);
CREATE TABLE incident.applied_revisions (
 tenant_id uuid NOT NULL,finding_id uuid NOT NULL,aggregate_revision bigint NOT NULL,
 PRIMARY KEY(tenant_id,finding_id),FOREIGN KEY(tenant_id,finding_id) REFERENCES finding.records(tenant_id,finding_id)
);
CREATE TABLE incident.timeline (
 tenant_id uuid NOT NULL,incident_id uuid NOT NULL,timeline_id uuid NOT NULL,
 kind text NOT NULL,actor text NOT NULL,policy_version text NOT NULL,reason text NOT NULL,
 payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,timeline_id),FOREIGN KEY(tenant_id,incident_id) REFERENCES incident.records(tenant_id,incident_id)
);
CREATE TABLE incident.outbox (
 tenant_id uuid NOT NULL,event_id uuid NOT NULL,incident_id uuid NOT NULL,revision bigint NOT NULL,
 payload jsonb NOT NULL,state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','delivered')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(tenant_id,event_id),
 FOREIGN KEY(tenant_id,incident_id) REFERENCES incident.records(tenant_id,incident_id)
);
-- +goose StatementBegin
DO $$
DECLARE item text;
BEGIN
 FOREACH item IN ARRAY ARRAY['finding.transport_keys','finding.timeline','finding.rejections','incident.correlation_subjects','incident.inbox','incident.applied_revisions','incident.timeline','incident.outbox'] LOOP
 EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY',item);
 EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY',item);
 EXECUTE format('CREATE POLICY tenant_isolation ON %s USING(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',item);
 END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT,INSERT,UPDATE ON finding.records,finding.inbox,finding.outbox,finding.transport_keys TO api_runtime_role,worker_runtime_role;
GRANT SELECT,INSERT ON finding.timeline,finding.rejections TO api_runtime_role,worker_runtime_role;
GRANT SELECT ON incident.correlation_subjects,incident.inbox,incident.applied_revisions,incident.timeline,incident.outbox TO api_runtime_role;
GRANT SELECT,INSERT,UPDATE ON incident.correlation_subjects,incident.inbox,incident.applied_revisions,incident.outbox TO worker_runtime_role;
GRANT SELECT,INSERT ON incident.timeline TO worker_runtime_role;
RESET ROLE;
SET ROLE schema_owner;
ALTER TABLE platform.source_registrations DROP CONSTRAINT source_allowed_schemas;
ALTER TABLE platform.source_registrations ADD CONSTRAINT source_allowed_schemas CHECK(cardinality(allowed_schemas)<=2 AND allowed_schemas <@ ARRAY['finding-envelope/v1','finding-envelope/v2']::text[]);
RESET ROLE;
