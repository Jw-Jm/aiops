-- +goose Up
-- Retain executed migration 47; restore the policy branch it dropped.
--
-- ALTER POLICY replaces USING wholesale. Migration 47 rewrote the
-- identity.role_grants policy with only the tenant GUC and the new
-- authorize_native_role operation, which silently removed the
-- session_context_active('find_session') branch added by migration 45.
-- Migration 45's SECURITY DEFINER session lookup cannot install app.tenant_id
-- (the session row must be found before a tenant is known), so the lookup
-- returned an empty grant projection: the live browser session showed
-- authorization.grants: [] and every administrator route answered 403.
--
-- This migration restores the exact branch set plus the new operation. It does
-- not change the function body or any table grant.
SET ROLE schema_owner;

ALTER POLICY tenant_isolation ON identity.role_grants
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('find_session')
    OR identity.session_context_active('authorize_native_role')
  );

RESET ROLE;

-- +goose Down
-- Forward-only: see migration 47.
