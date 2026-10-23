-- +goose Up
-- Retain executed migration 47; correct its result handling in a forward
-- migration.
--
-- Migration 47 returned FOUND after two PERFORM set_config(...) calls. PERFORM
-- is itself a SELECT, so FOUND described set_config (always one row) instead of
-- the grant lookup. The static counter-examples in the repository test caught a
-- true/false mix, and the live database check returned true for both a stale
-- authorization version and an operator role the account never held.
--
-- This migration replaces only the function body. The policies, grants and the
-- single-operation discipline added by 47 are unchanged.
SET ROLE schema_owner;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.authorize_native_role(
  p_tenant_id uuid,
  p_user_id uuid,
  p_role text,
  p_authorization_version integer
)
RETURNS boolean
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  locked_binding uuid;
  authorized boolean := false;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'authorize_native_role', true);
  IF p_tenant_id IS NULL OR p_user_id IS NULL
     OR p_role IS NULL OR p_role NOT IN ('operator', 'platform_admin')
     OR p_authorization_version IS NULL OR p_authorization_version < 1 THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN false;
  END IF;
  -- FOR SHARE keeps the granted row pinned for the rest of the caller's write
  -- transaction, matching the row-share fence the legacy binding check used.
  SELECT grant_row.binding_id INTO locked_binding
  FROM identity.accounts AS account
  JOIN identity.role_grants AS grant_row
    ON grant_row.tenant_id = account.tenant_id
   AND grant_row.user_id = account.user_id
  WHERE account.tenant_id = p_tenant_id
    AND account.user_id = p_user_id
    AND account.status = 'active'
    AND account.authorization_version = p_authorization_version
    AND grant_row.role = p_role
    AND grant_row.status = 'active'
    AND grant_row.authorization_version = p_authorization_version
    AND EXISTS (
      SELECT 1 FROM platform.tenants AS tenant
      WHERE tenant.tenant_id = account.tenant_id AND tenant.status = 'active'
    )
  FOR SHARE OF grant_row
  LIMIT 1;
  -- Capture the lookup result before any further statement overwrites FOUND.
  authorized := FOUND;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  RETURN authorized;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.authorize_native_role(uuid, uuid, text, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.authorize_native_role(uuid, uuid, text, integer)
  TO api_runtime_role, migration_role;

RESET ROLE;

-- +goose Down
-- Forward-only: see migration 47.
