-- +goose Up
SET ROLE schema_owner;
CREATE TABLE platform.sp04_request_ledger (
 tenant_id uuid NOT NULL REFERENCES platform.tenants(tenant_id),
 subject text NOT NULL CHECK(length(subject) BETWEEN 1 AND 256),
 operation text NOT NULL CHECK(operation IN ('evidence-query','diagnostic-graph')),
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 request_digest text NOT NULL CHECK(request_digest ~ '^sha256:[0-9a-f]{64}$'),
 object_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,subject,operation,idempotency_key)
);
ALTER TABLE platform.sp04_request_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform.sp04_request_ledger FORCE ROW LEVEL SECURITY;
CREATE POLICY sp04_request_tenant ON platform.sp04_request_ledger USING(tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT,INSERT ON platform.sp04_request_ledger TO api_runtime_role;
RESET ROLE;
