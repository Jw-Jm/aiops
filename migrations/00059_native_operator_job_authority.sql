-- +goose Up
-- Business Jobs must be able to re-prove a native operator authority without a
-- browser session: a queued job outlives the session that admitted it. This
-- migration adds one read-only projection (grant rows + account version) and one
-- policy gate for that exact re-proof. It does not add writes, does not change
-- how grants are created, revoked or versioned, and does not weaken the tenant
-- isolation branches added by migrations 45/47/49/51.
SET ROLE schema_owner;

-- The job-authority gate accepts either runtime duty (API request paths and the
-- Worker), unlike session_context_active which is pinned to the API login
-- because a browser session must never be proven by a Worker.
-- +goose StatementBegin
CREATE FUNCTION identity.job_authority_active(p_operation text)
RETURNS boolean
LANGUAGE sql
STABLE
SET search_path = pg_catalog, identity
AS $$
  SELECT current_user = 'schema_owner'
    AND session_user <> current_user
    AND (pg_has_role(session_user, 'api_runtime_role', 'member')
         OR pg_has_role(session_user, 'worker_runtime_role', 'member'))
    AND current_setting('app.identity_preauth', true) = p_operation
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.job_authority_active(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.job_authority_active(text)
  TO api_runtime_role, worker_runtime_role, migration_role;

-- Returns the live operator grants of an active account, with the account's
-- current authorization version. A Worker rebuilds the frozen job scope from
-- exactly these rows and refuses when the version differs, so a revocation or
-- re-grant can never revive an already invalid job.
--
-- The function lives in the investigation schema on purpose: the Worker already
-- has USAGE there, while identity stays closed to Workers. Only this fixed
-- projection crosses the boundary; no identity table becomes readable.
-- +goose StatementBegin
CREATE FUNCTION investigation.job_native_operator_scope(p_tenant_id uuid, p_user_id uuid)
RETURNS TABLE (authorization_version integer, binding_id uuid, scope jsonb)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'job_authority', true);
  IF p_tenant_id IS NULL OR p_user_id IS NULL THEN
    RETURN;
  END IF;
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
REVOKE ALL ON FUNCTION investigation.job_native_operator_scope(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION investigation.job_native_operator_scope(uuid, uuid)
  TO api_runtime_role, worker_runtime_role, migration_role;

-- ALTER POLICY replaces USING wholesale: every branch from migration 51 is
-- restated, plus only the job-authority operation.
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
    OR identity.mfa_scope_active()
    OR identity.job_authority_active('job_authority')
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('create_session')
    OR identity.session_context_active('change_own_password')
    OR identity.session_context_active('record_password_failure')
    OR identity.session_context_active('revoke_session')
    OR identity.mfa_scope_active()
  );

-- Every branch from migration 49 is restated, plus the job-authority operation.
ALTER POLICY tenant_isolation ON identity.role_grants
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('find_session')
    OR identity.session_context_active('authorize_native_role')
    OR identity.job_authority_active('job_authority')
  );

RESET ROLE;

-- +goose Down
-- Forward-only.
