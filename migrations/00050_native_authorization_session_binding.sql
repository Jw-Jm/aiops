-- +goose Up
-- Forward-only: bind authorized writes to the exact trusted server session.
--
-- Migration 47/48 answered "does this account still hold this grant at this
-- authorization version?". That is necessary but not sufficient: a request that
-- already passed the native middleware keeps the session it was admitted with,
-- while the server can revoke the session, change the password, disable the
-- account or rotate a version before the business transaction runs. The old
-- check only compared account/authorization_version, so a late transaction could
-- still write with a revoked session (observed on the task database: revoked
-- session issued_session_version=9, account.session_version=10, function true).
--
-- This migration replaces the unbound check with a session-bound one and keeps
-- the same fixed operation name, so no caller can keep using the unbound
-- variant:  identity.authorize_native_session_role(...) requires
--
--   * the exact session id, tenant, user and the session's issued credential,
--     authorization and session versions;
--   * the account to be active, not in first-password state, and to carry the
--     same credential/authorization/session versions;
--   * the session to be full-mode, unrevoked and inside both its idle and
--     absolute expiry windows (database clock);
--   * the tenant to be active;
--   * an active grant for the role at the account's authorization version.
--
-- Lock order is account -> session -> grant with FOR SHARE, matching the order
-- used by identity.revoke_native_session (account update, then session update)
-- and identity.change_own_password (account then session FOR UPDATE). A
-- revocation that commits first makes the later authorization fail; an
-- authorization that takes the shared lock first linearizes and the revocation
-- waits. Neither direction can deadlock on an inverted lock order.
--
-- The function returns a boolean only: no subject, grant row, credential or
-- token material. FORCE RLS, the fixed search_path and the single-operation
-- pre-auth reset are unchanged; API/Worker still hold no identity-table grants.
SET ROLE schema_owner;

-- The previous unbound function must not remain callable: a caller could
-- otherwise skip every session, revocation and expiry check.
DROP FUNCTION IF EXISTS identity.authorize_native_role(uuid, uuid, text, integer);

-- +goose StatementBegin
CREATE FUNCTION identity.authorize_native_session_role(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_role text,
  p_authorization_version integer,
  p_credential_version integer,
  p_session_version integer
)
RETURNS boolean
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  authorized boolean := false;
BEGIN
  -- Reset first: a caller-supplied value is never trusted.
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'authorize_native_role', true);
  IF p_tenant_id IS NULL OR p_user_id IS NULL OR p_session_id IS NULL
     OR p_role IS NULL OR p_role NOT IN ('operator', 'platform_admin')
     OR p_authorization_version IS NULL OR p_authorization_version < 1
     OR p_credential_version IS NULL OR p_credential_version < 1
     OR p_session_version IS NULL OR p_session_version < 1 THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN false;
  END IF;

  -- 1. Account: the trusted source of the current versions and status.
  PERFORM 1
  FROM identity.accounts AS account
  WHERE account.tenant_id = p_tenant_id
    AND account.user_id = p_user_id
    AND account.status = 'active'
    AND account.password_state = 'active'
    AND NOT account.must_change_password
    AND account.credential_version = p_credential_version
    AND account.authorization_version = p_authorization_version
    AND account.session_version = p_session_version
    AND EXISTS (
      SELECT 1 FROM platform.tenants AS tenant
      WHERE tenant.tenant_id = account.tenant_id AND tenant.status = 'active'
    )
  FOR SHARE OF account;
  IF NOT FOUND THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN false;
  END IF;

  -- 2. Session: the exact session the middleware admitted, still unrevoked and
  --    inside both expiry windows, carrying exactly the account's versions.
  PERFORM 1
  FROM identity.sessions AS session
  WHERE session.tenant_id = p_tenant_id
    AND session.session_id = p_session_id
    AND session.user_id = p_user_id
    AND session.mode = 'full'
    AND session.revoked_at IS NULL
    AND session.issued_credential_version = p_credential_version
    AND session.issued_authorization_version = p_authorization_version
    AND session.issued_session_version = p_session_version
    AND clock_timestamp() < session.idle_expires_at
    AND clock_timestamp() < session.absolute_expires_at
  FOR SHARE OF session;
  IF NOT FOUND THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN false;
  END IF;

  -- 3. Grant: pinned for the rest of the caller's write transaction.
  PERFORM 1
  FROM identity.role_grants AS grant_row
  WHERE grant_row.tenant_id = p_tenant_id
    AND grant_row.user_id = p_user_id
    AND grant_row.role = p_role
    AND grant_row.status = 'active'
    AND grant_row.authorization_version = p_authorization_version
  FOR SHARE OF grant_row;
  authorized := FOUND;

  PERFORM set_config('app.identity_preauth', 'disabled', true);
  RETURN authorized;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.authorize_native_session_role(uuid, uuid, uuid, text, integer, integer, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.authorize_native_session_role(uuid, uuid, uuid, text, integer, integer, integer)
  TO api_runtime_role, migration_role;

-- Worker/Job recheck cannot present a browser session: a queued job legitimately
-- outlives the session that created it. It must not fall back to the legacy
-- binding table either. This read-only operation answers "which authorization
-- version and grant rows prove this role right now?" and returns no row when the
-- account is inactive or the grant is gone. It is never used on the request
-- path: request-path authorization always goes through the session-bound
-- function above first.
-- +goose StatementBegin
CREATE FUNCTION identity.native_role_authority(
  p_tenant_id uuid,
  p_user_id uuid,
  p_role text
)
RETURNS TABLE (authorization_version integer, binding_ids uuid[])
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'authorize_native_role', true);
  IF p_tenant_id IS NULL OR p_user_id IS NULL
     OR p_role IS NULL OR p_role NOT IN ('operator', 'platform_admin') THEN
    RETURN;
  END IF;
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

REVOKE ALL ON FUNCTION identity.native_role_authority(uuid, uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.native_role_authority(uuid, uuid, text)
  TO api_runtime_role, migration_role, worker_runtime_role;

-- Extend the session policy with the exact authorization operation. ALTER POLICY
-- replaces USING wholesale, so every branch added by migration 45 is restated.
ALTER POLICY tenant_isolation ON identity.sessions
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('find_session')
    OR identity.session_context_active('touch_session')
    OR identity.session_context_active('revoke_session')
    OR identity.session_context_active('change_own_password')
    OR identity.session_context_active('authorize_native_role')
  );

RESET ROLE;

-- +goose Down
-- Forward-only: reverting would restore an authorization check that ignores the
-- trusted session and revoke every guarantee added here.
