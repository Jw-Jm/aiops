-- +goose Up
-- Migration 59 exposed an unlocked read-only projection. A Worker could finish
-- the authority recheck, then a concurrent administrative revocation could
-- commit before the Worker's business transaction committed, letting the Worker
-- write under authority it no longer had. These projections take the same row
-- locks the administrative write takes (account row first, then the grant rows)
-- and are VOLATILE because they lock; the callers keep the transaction open
-- until their business statements commit.
SET ROLE schema_owner;

-- +goose StatementBegin
CREATE FUNCTION platform.native_operator_scope_locked(p_tenant_id uuid, p_user_id uuid)
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
  -- Lock order matches identity.admin_account_write (migration 55/57):
  -- the account row first, then that account's grant rows.
  PERFORM 1 FROM identity.accounts AS account
    WHERE account.tenant_id = p_tenant_id AND account.user_id = p_user_id
    FOR UPDATE;
  PERFORM 1 FROM identity.role_grants AS grant_row
    WHERE grant_row.tenant_id = p_tenant_id
      AND grant_row.user_id = p_user_id
      AND grant_row.role = 'operator'
    ORDER BY grant_row.binding_id
    FOR UPDATE;
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
REVOKE ALL ON FUNCTION platform.native_operator_scope_locked(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.native_operator_scope_locked(uuid, uuid)
  TO api_runtime_role, worker_runtime_role, migration_role;

-- +goose StatementBegin
CREATE FUNCTION platform.native_authority_locked(
  p_tenant_id uuid,
  p_user_id uuid,
  p_role text
)
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
    FOR UPDATE;
  PERFORM 1 FROM identity.role_grants AS grant_row
    WHERE grant_row.tenant_id = p_tenant_id
      AND grant_row.user_id = p_user_id
      AND grant_row.role = p_role
    ORDER BY grant_row.binding_id
    FOR UPDATE;
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
REVOKE ALL ON FUNCTION platform.native_authority_locked(uuid, uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.native_authority_locked(uuid, uuid, text)
  TO api_runtime_role, worker_runtime_role, migration_role;

-- The projections live in platform because Workload and API both already have
-- USAGE there and identity stays closed to Workers. They only expose the fixed
-- projection, never identity tables.
--
-- The unlocked projection stays in the frozen 59 text but is no longer callable
-- by a runtime duty, so a future caller cannot silently reintroduce the gap.
REVOKE EXECUTE ON FUNCTION investigation.job_native_operator_scope(uuid, uuid)
  FROM api_runtime_role, worker_runtime_role, migration_role;

RESET ROLE;

-- +goose Down
-- Forward-only.
