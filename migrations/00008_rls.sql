-- +goose Up
SET ROLE schema_owner;

-- +goose StatementBegin
DO $$
DECLARE
  item record;
BEGIN
  FOR item IN
    SELECT n.nspname AS schema_name, c.relname AS table_name
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relkind = 'r'
      AND n.nspname IN ('platform', 'finding', 'incident', 'investigation', 'action', 'audit')
      AND c.relname <> 'tenants'
      AND EXISTS (
        SELECT 1 FROM pg_attribute a
        WHERE a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
      )
  LOOP
    EXECUTE format('ALTER TABLE %I.%I ENABLE ROW LEVEL SECURITY', item.schema_name, item.table_name);
    EXECUTE format('ALTER TABLE %I.%I FORCE ROW LEVEL SECURITY', item.schema_name, item.table_name);
    EXECUTE format(
      'CREATE POLICY tenant_isolation ON %I.%I USING (tenant_id = NULLIF(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting(''app.tenant_id'', true), '''')::uuid)',
      item.schema_name,
      item.table_name
    );
  END LOOP;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA platform, finding, incident, investigation, action, audit TO api_runtime_role, worker_runtime_role;
GRANT SELECT ON finding.records, finding.inbox, finding.outbox TO api_runtime_role;
GRANT SELECT, INSERT, UPDATE ON finding.records, finding.inbox, finding.outbox TO worker_runtime_role;
GRANT SELECT ON incident.records, incident.finding_links TO api_runtime_role;
GRANT SELECT, INSERT, UPDATE ON incident.records, incident.finding_links TO worker_runtime_role;
GRANT SELECT, INSERT, UPDATE ON investigation.jobs TO api_runtime_role;
GRANT INSERT ON investigation.worker_queue TO api_runtime_role;
GRANT SELECT, UPDATE ON investigation.jobs, investigation.worker_queue TO worker_runtime_role;
GRANT SELECT, INSERT, UPDATE ON action.risk_acknowledgements, action.executions TO api_runtime_role;
GRANT SELECT ON audit.records, audit.segments TO api_runtime_role, worker_runtime_role;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON audit.records FROM PUBLIC, api_runtime_role, worker_runtime_role, ops_readonly_role;
REVOKE UPDATE, DELETE, TRUNCATE ON audit.segments FROM PUBLIC, api_runtime_role, worker_runtime_role, ops_readonly_role;
GRANT USAGE ON SCHEMA platform, audit TO audit_append_owner;
GRANT INSERT ON audit.records TO audit_append_owner;
GRANT USAGE, SELECT ON SEQUENCE audit.records_audit_seq_seq TO audit_append_owner;
GRANT SELECT, INSERT, UPDATE ON audit.tenant_heads TO audit_append_owner;

RESET ROLE;
