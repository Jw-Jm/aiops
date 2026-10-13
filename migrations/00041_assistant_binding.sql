-- +goose Up
SET ROLE schema_owner;
ALTER TABLE investigation.assistant_events ALTER COLUMN turn_id DROP NOT NULL;
CREATE TABLE investigation.assistant_binding_requests (
 tenant_id uuid NOT NULL,owner_subject text NOT NULL,idempotency_key text NOT NULL,
 conversation_id uuid NOT NULL,request_digest text NOT NULL,revision bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,owner_subject,idempotency_key),
 FOREIGN KEY(tenant_id,conversation_id) REFERENCES investigation.assistant_conversations(tenant_id,conversation_id)
);
ALTER TABLE investigation.assistant_binding_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE investigation.assistant_binding_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY assistant_binding_tenant ON investigation.assistant_binding_requests USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT,INSERT ON investigation.assistant_binding_requests TO api_runtime_role;
RESET ROLE;
