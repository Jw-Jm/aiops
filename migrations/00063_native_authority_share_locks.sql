-- +goose Up
-- Migration 61 took FOR UPDATE on the account/grant rows. A native API request
-- already holds FOR SHARE on the same rows from identity.authorize_native_session_role,
-- so two same-account requests could each hold SHARE and then request UPDATE —
-- a classic lock upgrade deadlock.
--
-- The projections now take FOR SHARE in the same order (account row, then grant
-- rows). SHARE is compatible with the API's existing SHARE (no upgrade, no
-- deadlock) and still conflicts with every administrative write: the
-- administrative path locks the account FOR UPDATE and then UPDATEs the grant
-- rows, so it waits until the reading transaction commits.
SET ROLE schema_owner;

-- +goose StatementBegin
CREATE FUNCTION platform.native_operator_scope_shared(p_tenant_id uuid, p_user_id uuid)
RETURNS TABLE (authorization_version integer, binding_id uuid, scope jsonb)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'job_authority', true);
  IF p_tenant_id IS NULL OR p_user_id IS NULL THEN
    RETURN;
  END IF;
  -- Same order as identity.admin_account_write: account row, then grants.
  PERFORM 1 FROM identity.accounts AS account
    WHERE account.tenant_id = p_tenant_id AND account.user_id = p_user_id
    FOR SHARE;
  PERFORM 1 FROM identity.role_grants AS grant_row
    WHERE grant_row.tenant_id = p_tenant_id
      AND grant_row.user_id = p_user_id
      AND grant_row.role = 'operator'
    ORDER BY grant_row.binding_id
    FOR SHARE;
  RETURN QUERY
  SELECT account.authorization_version, grant_row.binding_id, grant_row.scope
  FROM identity.accounts AS account
  JOIN identity.role_grants AS grant_row
    ON grant_row.tenant_id = account.tenant_id
   AND grant_row.user_id = account.user_id
  WHERE account.tenant_id = p_tenant_id
    AND account.user_id = p_user_id
    AND account.status = 'active'
    AND grant_row.role = 'operator'
    AND grant_row.status = 'active'
    AND grant_row.authorization_version = account.authorization_version
    AND EXISTS (
      SELECT 1 FROM platform.tenants AS tenant
      WHERE tenant.tenant_id = account.tenant_id AND tenant.status = 'active'
    )
  ORDER BY grant_row.binding_id;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.native_operator_scope_shared(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.native_operator_scope_shared(uuid, uuid)
  TO api_runtime_role, worker_runtime_role, migration_role;

-- +goose StatementBegin
CREATE FUNCTION platform.native_authority_shared(p_tenant_id uuid, p_user_id uuid, p_role text)
RETURNS TABLE (authorization_version integer, binding_ids uuid[])
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'job_authority', true);
  IF p_tenant_id IS NULL OR p_user_id IS NULL
     OR p_role IS NULL OR p_role NOT IN ('operator', 'platform_admin') THEN
    RETURN;
  END IF;
  PERFORM 1 FROM identity.accounts AS account
    WHERE account.tenant_id = p_tenant_id AND account.user_id = p_user_id
    FOR SHARE;
  PERFORM 1 FROM identity.role_grants AS grant_row
    WHERE grant_row.tenant_id = p_tenant_id
      AND grant_row.user_id = p_user_id
      AND grant_row.role = p_role
    ORDER BY grant_row.binding_id
    FOR SHARE;
  RETURN QUERY
  SELECT account.authorization_version,
         COALESCE(
           (SELECT array_agg(grant_row.binding_id ORDER BY grant_row.binding_id)
            FROM identity.role_grants AS grant_row
            WHERE grant_row.tenant_id = account.tenant_id
              AND grant_row.user_id = account.user_id
              AND grant_row.role = p_role
              AND grant_row.status = 'active'
              AND grant_row.authorization_version = account.authorization_version),
           ARRAY[]::uuid[])
  FROM identity.accounts AS account
  WHERE account.tenant_id = p_tenant_id
    AND account.user_id = p_user_id
    AND account.status = 'active'
    AND EXISTS (
      SELECT 1 FROM platform.tenants AS tenant
      WHERE tenant.tenant_id = account.tenant_id AND tenant.status = 'active'
    )
    AND EXISTS (
      SELECT 1 FROM identity.role_grants AS grant_row
      WHERE grant_row.tenant_id = account.tenant_id
        AND grant_row.user_id = account.user_id
        AND grant_row.role = p_role
        AND grant_row.status = 'active'
        AND grant_row.authorization_version = account.authorization_version
    );
  PERFORM set_config('app.identity_preauth', 'disabled', true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.native_authority_shared(uuid, uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.native_authority_shared(uuid, uuid, text)
  TO api_runtime_role, worker_runtime_role, migration_role;

-- The FOR UPDATE variants of migration 61 stay in the frozen text but are no
-- longer callable by a runtime duty, so the deadlock-prone mode cannot return.
REVOKE EXECUTE ON FUNCTION platform.native_operator_scope_locked(uuid, uuid)
  FROM api_runtime_role, worker_runtime_role, migration_role;
REVOKE EXECUTE ON FUNCTION platform.native_authority_locked(uuid, uuid, text)
  FROM api_runtime_role, worker_runtime_role, migration_role;

RESET ROLE;

-- +goose Down
-- Forward-only.
