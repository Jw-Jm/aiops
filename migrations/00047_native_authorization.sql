-- +goose Up
-- Forward-only shared authorization for native accounts (P2-04).
--
-- Native sessions project their roles from identity.role_grants, while the
-- existing business transactions re-check platform.role_bindings by subject.
-- That mismatch is the documented root cause of the native administrator 403:
-- a native session is never represented in platform.role_bindings, and copying
-- rows there would silently grant legacy authority.
--
-- This migration adds one fixed SECURITY DEFINER operation that answers exactly
-- one question inside the caller's own transaction: does this active account
-- still hold this active grant at this authorization version? It cannot read
-- another tenant, it cannot be enabled by a caller-supplied GUC (the function
-- resets the context before setting its own operation), and it never returns
-- grant rows, subjects or credential material.
--
-- No table grant is added to api_runtime_role. The function remains subject to
-- FORCE RLS on the identity tables, so the tenant policies are extended with the
-- exact operation name only.

SET ROLE schema_owner;

-- +goose StatementBegin
CREATE FUNCTION identity.authorize_native_role(
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
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  RETURN FOUND;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.authorize_native_role(uuid, uuid, text, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.authorize_native_role(uuid, uuid, text, integer)
  TO api_runtime_role, migration_role;

-- Extend the existing tenant policies with the exact authorization operation.
-- ALTER POLICY only rewrites USING, so the WITH CHECK clauses that limit writes
-- to the session/pre-auth operations from migrations 44-46 stay untouched.
ALTER POLICY tenant_isolation ON identity.accounts
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('find_login_account')
    OR identity.preauth_context_active('record_password_failure')
    OR identity.preauth_context_active('clear_password_failure')
    OR identity.session_context_active('find_session')
    OR identity.session_context_active('create_session')
    OR identity.session_context_active('touch_session')
    OR identity.session_context_active('revoke_session')
    OR identity.session_context_active('change_own_password')
    OR identity.session_context_active('authorize_native_role')
  );

ALTER POLICY tenant_isolation ON identity.role_grants
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('authorize_native_role')
  );

RESET ROLE;

-- +goose Down
-- Forward-only: this migration is not rolled back. Reverting would remove the
-- only native authorization proof and re-open the legacy-authority bypass.
