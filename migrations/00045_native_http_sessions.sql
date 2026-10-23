-- +goose Up
-- Forward-only native HTTP session support for P2-02.
--
-- API runtime still has no direct identity-table grants. Every session lookup,
-- touch, revocation and self password change is a fixed SECURITY DEFINER
-- function with a reset-then-exact pre-auth context and a fixed search_path.
-- Cookie bearer material is never stored in plaintext: callers pass SHA-256
-- digests only.

SET ROLE schema_owner;

-- A single durable entry-wide admission fence prevents username spraying and
-- survives concurrent requests/process restarts. It is not keyed by attacker-
-- supplied login names, so it cannot grow without bound.
CREATE TABLE identity.login_entry_attempts (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  attempt_count integer NOT NULL CHECK (attempt_count BETWEEN 0 AND 100000000),
  window_started_at timestamptz NOT NULL,
  locked_until timestamptz,
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK (locked_until IS NULL OR locked_until > window_started_at)
);
INSERT INTO identity.login_entry_attempts(singleton, attempt_count, window_started_at)
VALUES (true, 0, clock_timestamp());

-- Session operations use the same fail-closed pre-auth discipline as login.
-- A caller-supplied GUC cannot enable these functions: each function resets it
-- before setting its exact operation, direct table grants remain absent, and
-- the helper still requires the security-definer user plus an api-duty session.
-- +goose StatementBegin
CREATE FUNCTION identity.session_context_active(p_operation text)
RETURNS boolean
LANGUAGE sql
STABLE
SET search_path = pg_catalog, identity
AS $$
  SELECT current_user = 'schema_owner'
    AND session_user <> current_user
    AND pg_has_role(session_user, 'api_runtime_role', 'member')
    AND current_setting('app.identity_preauth', true) = p_operation;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.session_context_active(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.session_context_active(text) TO api_runtime_role, migration_role;

-- Extend the existing tenant policies with exact session operations only.
DROP POLICY tenant_isolation ON identity.accounts;
CREATE POLICY tenant_isolation ON identity.accounts
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('find_login_account')
    OR identity.preauth_context_active('record_password_failure')
    OR identity.session_context_active('find_session')
    OR identity.session_context_active('create_session')
    OR identity.session_context_active('touch_session')
    OR identity.session_context_active('revoke_session')
    OR identity.session_context_active('change_own_password')
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('create_session')
    OR identity.session_context_active('change_own_password')
    OR identity.session_context_active('record_password_failure')
    OR identity.session_context_active('revoke_session')
  );

DROP POLICY tenant_isolation ON identity.credentials;
CREATE POLICY tenant_isolation ON identity.credentials
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('find_login_account')
    OR identity.session_context_active('find_session')
    OR identity.session_context_active('change_own_password')
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('change_own_password')
  );

DROP POLICY tenant_isolation ON identity.sessions;
CREATE POLICY tenant_isolation ON identity.sessions
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('find_session')
    OR identity.session_context_active('touch_session')
    OR identity.session_context_active('revoke_session')
    OR identity.session_context_active('change_own_password')
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('create_session')
    OR identity.session_context_active('touch_session')
    OR identity.session_context_active('revoke_session')
    OR identity.session_context_active('change_own_password')
  );

DROP POLICY tenant_isolation ON identity.role_grants;
CREATE POLICY tenant_isolation ON identity.role_grants
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('find_session')
  );

DROP POLICY tenant_isolation ON identity.subject_mappings;
CREATE POLICY tenant_isolation ON identity.subject_mappings
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  );

DROP POLICY tenant_isolation ON identity.administrator_initializations;
CREATE POLICY tenant_isolation ON identity.administrator_initializations
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  );

ALTER TABLE identity.login_entry_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.login_entry_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY login_entry_isolation ON identity.login_entry_attempts
  USING (identity.session_context_active('admit_login'))
  WITH CHECK (identity.session_context_active('admit_login'));

-- Existing sessions predate this column. A conservative default is safe because
-- every active session is revalidated against the account version on use.
ALTER TABLE identity.sessions
  ADD COLUMN issued_session_version integer NOT NULL DEFAULT 1
  CHECK (issued_session_version > 0);

-- +goose StatementBegin
CREATE FUNCTION identity.create_native_session(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_mode text,
  p_token_digest bytea,
  p_csrf_digest bytea,
  p_credential_version integer,
  p_authorization_version integer,
  p_session_version integer,
  p_idle_expires_at timestamptz,
  p_absolute_expires_at timestamptz
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'create_session', true);
  IF p_mode NOT IN ('full', 'first_password_only')
     OR octet_length(p_token_digest) <> 32
     OR octet_length(p_csrf_digest) <> 32
     OR p_idle_expires_at <= clock_timestamp()
     OR p_absolute_expires_at <= p_idle_expires_at THEN
    RAISE EXCEPTION 'invalid native session request' USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM identity.accounts account
    WHERE account.tenant_id = p_tenant_id
      AND account.user_id = p_user_id
      AND account.status = 'active'
      AND account.credential_version = p_credential_version
      AND account.authorization_version = p_authorization_version
      AND account.session_version = p_session_version
      AND (
        (p_mode = 'first_password_only' AND account.must_change_password)
        OR (p_mode = 'full' AND NOT account.must_change_password)
      )
  ) THEN
    RAISE EXCEPTION 'native session target account does not match'
      USING ERRCODE = '23514';
  END IF;
  INSERT INTO identity.sessions
    (tenant_id, session_id, user_id, mode, token_digest, csrf_digest,
     issued_credential_version, issued_authorization_version, issued_session_version,
     issued_at, last_seen_at, idle_expires_at, absolute_expires_at)
  VALUES
    (p_tenant_id, p_session_id, p_user_id, p_mode, p_token_digest, p_csrf_digest,
     p_credential_version, p_authorization_version, p_session_version,
     clock_timestamp(), clock_timestamp(), p_idle_expires_at, p_absolute_expires_at);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.find_active_session(p_token_digest bytea)
RETURNS TABLE (
  tenant_id uuid,
  user_id uuid,
  session_id uuid,
  mode text,
  csrf_digest bytea,
  issued_at timestamptz,
  idle_expires_at timestamptz,
  absolute_expires_at timestamptz,
  subject text,
  login_name text,
  display_name text,
  account_status text,
  password_state text,
  must_change_password boolean,
  account_revision integer,
  credential_version integer,
  authorization_version integer,
  session_version integer,
  password_hash text,
  grants jsonb
)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'find_session', true);
  IF p_token_digest IS NULL OR octet_length(p_token_digest) <> 32 THEN
    RETURN;
  END IF;
  RETURN QUERY
  SELECT session.tenant_id, session.user_id, session.session_id,
         session.mode, session.csrf_digest,
         session.issued_at, session.idle_expires_at, session.absolute_expires_at,
         account.subject, account.login_name, account.display_name,
         account.status, account.password_state, account.must_change_password,
         account.account_revision, account.credential_version,
         account.authorization_version, account.session_version,
         credential.password_hash,
         COALESCE(grants.value, '[]'::jsonb)
  FROM identity.sessions AS session
  JOIN identity.accounts AS account
    ON account.tenant_id = session.tenant_id
   AND account.user_id = session.user_id
  JOIN identity.credentials AS credential
    ON credential.tenant_id = account.tenant_id
   AND credential.user_id = account.user_id
   AND credential.credential_version = account.credential_version
   AND credential.status = 'active'
  CROSS JOIN LATERAL (
    SELECT COALESCE(jsonb_agg(jsonb_build_object(
             'bindingId', grant_row.binding_id,
             'userId', grant_row.user_id,
             'tenantId', grant_row.tenant_id,
             'role', grant_row.role,
             'scope', grant_row.scope,
             'authorizationVersion', grant_row.authorization_version,
             'grantedAt', grant_row.granted_at,
             'revokedAt', grant_row.revoked_at
           ) ORDER BY grant_row.role, grant_row.binding_id), '[]'::jsonb) AS value
    FROM identity.role_grants AS grant_row
    WHERE grant_row.tenant_id = account.tenant_id
      AND grant_row.user_id = account.user_id
      AND grant_row.status = 'active'
      AND grant_row.authorization_version = account.authorization_version
  ) AS grants
  WHERE session.token_digest = p_token_digest
    AND session.revoked_at IS NULL
    AND session.issued_credential_version = account.credential_version
    AND session.issued_authorization_version = account.authorization_version
    AND session.issued_session_version = account.session_version
    AND account.status = 'active'
    AND clock_timestamp() < session.idle_expires_at
    AND clock_timestamp() < session.absolute_expires_at;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.touch_native_session(
  p_token_digest bytea,
  p_idle_seconds integer
) RETURNS timestamptz
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  next_idle timestamptz;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'touch_session', true);
  IF p_token_digest IS NULL OR octet_length(p_token_digest) <> 32
     OR p_idle_seconds < 1 OR p_idle_seconds > 86400 THEN
    RAISE EXCEPTION 'invalid native session touch' USING ERRCODE = '22023';
  END IF;
  UPDATE identity.sessions AS session
  SET last_seen_at = least(
        clock_timestamp(),
        session.absolute_expires_at - interval '1 microsecond'
      ),
      idle_expires_at = least(
        session.absolute_expires_at - interval '1 microsecond',
        clock_timestamp() + make_interval(secs => p_idle_seconds)
      )
  WHERE session.token_digest = p_token_digest
    AND session.revoked_at IS NULL
    AND clock_timestamp() < session.idle_expires_at
    AND clock_timestamp() < session.absolute_expires_at
    AND EXISTS (
      SELECT 1
      FROM identity.accounts AS account
      WHERE account.tenant_id = session.tenant_id
        AND account.user_id = session.user_id
        AND account.status = 'active'
        AND account.credential_version = session.issued_credential_version
        AND account.authorization_version = session.issued_authorization_version
        AND account.session_version = session.issued_session_version
    )
  RETURNING session.idle_expires_at INTO next_idle;
  IF next_idle IS NULL THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  RETURN next_idle;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.revoke_native_session(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_revoked_at timestamptz,
  p_reason text,
  p_expected_credential_version integer,
  p_expected_authorization_version integer,
  p_expected_session_version integer
) RETURNS TABLE (
  credential_version integer,
  session_version integer,
  authorization_version integer
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'revoke_session', true);
  IF p_reason IS NULL OR length(p_reason) NOT BETWEEN 1 AND 512 THEN
    RAISE EXCEPTION 'invalid session revocation reason' USING ERRCODE = '22023';
  END IF;
  UPDATE identity.accounts AS account
  SET session_version = account.session_version + 1
  WHERE account.tenant_id = p_tenant_id
    AND account.user_id = p_user_id
    AND account.status = 'active'
    AND account.credential_version = p_expected_credential_version
    AND account.authorization_version = p_expected_authorization_version
    AND account.session_version = p_expected_session_version
  RETURNING
    account.credential_version,
    account.session_version,
    account.authorization_version
  INTO credential_version, session_version, authorization_version;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'session revocation version is stale' USING ERRCODE = '40001';
  END IF;
  UPDATE identity.sessions AS session
  SET revoked_at = p_revoked_at,
      revoked_reason = p_reason
  WHERE session.tenant_id = p_tenant_id
    AND session.user_id = p_user_id
    AND session.session_id = p_session_id
    AND session.revoked_at IS NULL
    AND clock_timestamp() < session.idle_expires_at
    AND clock_timestamp() < session.absolute_expires_at;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  RETURN NEXT;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.change_own_password(
  p_tenant_id uuid,
  p_user_id uuid,
  p_old_session_id uuid,
  p_new_session_id uuid,
  p_new_token_digest bytea,
  p_new_csrf_digest bytea,
  p_expected_credential_version integer,
  p_expected_authorization_version integer,
  p_expected_session_version integer,
  p_new_password_hash text,
  p_idle_expires_at timestamptz,
  p_absolute_expires_at timestamptz
) RETURNS TABLE (
  credential_version integer,
  session_version integer,
  authorization_version integer,
  account_revision integer
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  next_credential_version integer;
  new_session_mode text := 'full';
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'change_own_password', true);
  IF octet_length(p_new_token_digest) <> 32
     OR octet_length(p_new_csrf_digest) <> 32
     OR p_idle_expires_at <= clock_timestamp()
     OR p_absolute_expires_at <= p_idle_expires_at THEN
    RAISE EXCEPTION 'invalid password change session' USING ERRCODE = '22023';
  END IF;
  SELECT account.credential_version + 1 INTO next_credential_version
  FROM identity.accounts AS account
  JOIN identity.sessions AS old_session
    ON old_session.tenant_id = account.tenant_id
   AND old_session.user_id = account.user_id
   AND old_session.session_id = p_old_session_id
  WHERE account.tenant_id = p_tenant_id
    AND account.user_id = p_user_id
    AND account.status = 'active'
    AND account.credential_version = p_expected_credential_version
    AND account.authorization_version = p_expected_authorization_version
    AND account.session_version = p_expected_session_version
    AND old_session.revoked_at IS NULL
    AND old_session.issued_credential_version = p_expected_credential_version
    AND old_session.issued_authorization_version = p_expected_authorization_version
    AND old_session.issued_session_version = p_expected_session_version
    AND clock_timestamp() < old_session.idle_expires_at
    AND clock_timestamp() < old_session.absolute_expires_at
  FOR UPDATE OF account, old_session;
  IF next_credential_version IS NULL THEN
    RAISE EXCEPTION 'credential or session version is stale' USING ERRCODE = '40001';
  END IF;

  INSERT INTO identity.credentials
    (tenant_id, user_id, credential_version, password_hash, status)
  VALUES
    (p_tenant_id, p_user_id, next_credential_version, p_new_password_hash, 'active');

  UPDATE identity.accounts AS account
  SET password_state = 'active',
      must_change_password = false,
      account_revision = account.account_revision + 1,
      credential_version = next_credential_version,
      session_version = account.session_version + 1
  WHERE account.tenant_id = p_tenant_id
    AND account.user_id = p_user_id
  RETURNING
    account.credential_version,
    account.session_version,
    account.authorization_version,
    account.account_revision
  INTO credential_version, session_version, authorization_version, account_revision;

  UPDATE identity.sessions AS session
  SET revoked_at = clock_timestamp(),
      revoked_reason = 'credential_version_changed'
  WHERE session.tenant_id = p_tenant_id
    AND session.user_id = p_user_id
    AND session.revoked_at IS NULL;

  INSERT INTO identity.sessions
    (tenant_id, session_id, user_id, mode, token_digest, csrf_digest,
     issued_credential_version, issued_authorization_version, issued_session_version,
     issued_at, last_seen_at, idle_expires_at, absolute_expires_at)
  VALUES
    (p_tenant_id, p_new_session_id, p_user_id, new_session_mode,
     p_new_token_digest, p_new_csrf_digest,
     next_credential_version, authorization_version, session_version,
     clock_timestamp(), clock_timestamp(), p_idle_expires_at, p_absolute_expires_at);
  RETURN NEXT;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.administrator_state(
  p_tenant_id uuid,
  p_user_id uuid
) RETURNS TABLE (
  active_administrators integer,
  is_administrator boolean,
  is_last_active_administrator boolean
)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  active_count integer := 0;
  target_is_admin boolean := false;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'find_session', true);
  SELECT count(*) INTO active_count
  FROM identity.role_grants AS grant_row
  JOIN identity.accounts AS account
    ON account.tenant_id = grant_row.tenant_id
   AND account.user_id = grant_row.user_id
  WHERE grant_row.tenant_id = p_tenant_id
    AND grant_row.role = 'platform_admin'
    AND grant_row.status = 'active'
    AND account.status = 'active'
    AND grant_row.authorization_version = account.authorization_version;
  SELECT EXISTS (
    SELECT 1 FROM identity.role_grants AS target
    JOIN identity.accounts AS account
      ON account.tenant_id = target.tenant_id
     AND account.user_id = target.user_id
    WHERE target.tenant_id = p_tenant_id
      AND target.user_id = p_user_id
      AND target.role = 'platform_admin'
      AND target.status = 'active'
      AND account.status = 'active'
      AND target.authorization_version = account.authorization_version
  ) INTO target_is_admin;
  RETURN QUERY SELECT
    active_count,
    target_is_admin,
    target_is_admin AND active_count <= 1;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.admit_login_attempt(
  p_now timestamptz,
  p_limit integer,
  p_window_seconds integer,
  p_lock_seconds integer
) RETURNS TABLE (allowed boolean, retry_at timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  next_count integer;
  next_window timestamptz;
  next_lock timestamptz;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'admit_login', true);
  IF p_now IS NULL OR p_limit < 1 OR p_limit > 100000
     OR p_window_seconds < 1 OR p_window_seconds > 86400
     OR p_lock_seconds < 1 OR p_lock_seconds > 86400 THEN
    RAISE EXCEPTION 'invalid login admission request' USING ERRCODE = '22023';
  END IF;
  SELECT attempt_count, window_started_at, locked_until
  INTO next_count, next_window, next_lock
  FROM identity.login_entry_attempts
  WHERE singleton
  FOR UPDATE;
  IF NOT FOUND THEN
    INSERT INTO identity.login_entry_attempts(singleton, attempt_count, window_started_at)
    VALUES (true, 1, p_now);
    RETURN QUERY SELECT true, NULL::timestamptz;
    RETURN;
  END IF;
  IF next_lock IS NOT NULL AND next_lock > p_now THEN
    RETURN QUERY SELECT false, next_lock;
    RETURN;
  END IF;
  IF next_window <= p_now - make_interval(secs => p_window_seconds) THEN
    next_count := 1;
    next_window := p_now;
  ELSE
    next_count := next_count + 1;
  END IF;
  IF next_count > p_limit THEN
    next_lock := p_now + make_interval(secs => p_lock_seconds);
  END IF;
  UPDATE identity.login_entry_attempts
  SET attempt_count = next_count, window_started_at = next_window,
      locked_until = next_lock, updated_at = p_now
  WHERE singleton;
  RETURN QUERY SELECT next_lock IS NULL OR next_lock <= p_now, next_lock;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.record_known_login_failure(
  p_tenant_id uuid,
  p_user_id uuid,
  p_failed_at timestamptz,
  p_limit integer,
  p_lock_seconds integer
) RETURNS TABLE (out_failed_count integer, out_locked_until timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  existing_count integer;
  existing_window timestamptz;
  existing_lock timestamptz;
  next_count integer;
  next_window timestamptz;
  next_lock timestamptz;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'record_password_failure', true);
  IF p_tenant_id IS NULL OR p_user_id IS NULL OR p_failed_at IS NULL
     OR p_limit < 1 OR p_limit > 100000 OR p_lock_seconds < 1 OR p_lock_seconds > 86400 THEN
    RAISE EXCEPTION 'invalid known login failure' USING ERRCODE = '22023';
  END IF;
  PERFORM 1 FROM identity.accounts AS account
  WHERE account.tenant_id = p_tenant_id AND account.user_id = p_user_id
    AND account.status = 'active'
  FOR UPDATE OF account;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'native password account is unavailable' USING ERRCODE = '28000';
  END IF;
  SELECT attempts.failed_count, attempts.window_started_at, attempts.locked_until
  INTO existing_count, existing_window, existing_lock
  FROM identity.password_attempts AS attempts
  WHERE attempts.tenant_id = p_tenant_id AND attempts.user_id = p_user_id
  FOR UPDATE;
  IF NOT FOUND THEN
    next_count := 1;
    next_window := p_failed_at;
    next_lock := CASE WHEN next_count >= p_limit THEN p_failed_at + make_interval(secs => p_lock_seconds) END;
    INSERT INTO identity.password_attempts
      (tenant_id, user_id, failed_count, window_started_at, locked_until, updated_at)
    VALUES (p_tenant_id, p_user_id, next_count, next_window, next_lock, p_failed_at);
  ELSE
    IF existing_window <= p_failed_at - interval '15 minutes' THEN
      next_count := 1;
      next_window := p_failed_at;
    ELSE
      next_count := existing_count + 1;
      next_window := existing_window;
    END IF;
    next_lock := existing_lock;
    IF next_count >= p_limit THEN
      next_lock := GREATEST(existing_lock, p_failed_at + make_interval(secs => p_lock_seconds));
    END IF;
    UPDATE identity.password_attempts AS attempts
    SET failed_count = next_count, window_started_at = next_window,
        locked_until = next_lock, updated_at = p_failed_at
    WHERE attempts.tenant_id = p_tenant_id AND attempts.user_id = p_user_id;
  END IF;
  RETURN QUERY SELECT next_count, next_lock;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.resolve_cluster_ids(
  p_tenant_id uuid,
  p_cluster_uids text[]
) RETURNS TABLE (cluster_uid text, cluster_id uuid)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity, platform
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'find_session', true);
  IF p_tenant_id IS NULL OR p_cluster_uids IS NULL OR cardinality(p_cluster_uids) > 50 THEN
    RETURN;
  END IF;
  RETURN QUERY
  SELECT registration.cluster_uid, registration.cluster_id
  FROM platform.cluster_registrations AS registration
  WHERE registration.tenant_id = p_tenant_id
    AND registration.status = 'active'
    AND registration.cluster_uid = ANY(p_cluster_uids);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.list_authorized_clusters(
  p_tenant_id uuid,
  p_cluster_ids uuid[]
) RETURNS TABLE (cluster_id uuid, cluster_uid text, display_name text)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity, platform
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'find_session', true);
  IF p_tenant_id IS NULL OR p_cluster_ids IS NULL OR cardinality(p_cluster_ids) > 50 THEN
    RETURN;
  END IF;
  RETURN QUERY
  SELECT registration.cluster_id, registration.cluster_uid, registration.display_name
  FROM platform.cluster_registrations AS registration
  WHERE registration.tenant_id = p_tenant_id
    AND registration.status = 'active'
    AND registration.cluster_id = ANY(p_cluster_ids)
  ORDER BY registration.display_name
  LIMIT 100;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.create_native_session(uuid, uuid, uuid, text, bytea, bytea, integer, integer, integer, timestamptz, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.find_active_session(bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.touch_native_session(bytea, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.revoke_native_session(uuid, uuid, uuid, timestamptz, text, integer, integer, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.change_own_password(uuid, uuid, uuid, uuid, bytea, bytea, integer, integer, integer, text, timestamptz, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.session_context_active(text) TO api_runtime_role, migration_role;
GRANT EXECUTE ON FUNCTION identity.create_native_session(uuid, uuid, uuid, text, bytea, bytea, integer, integer, integer, timestamptz, timestamptz) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.find_active_session(bytea) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.touch_native_session(bytea, integer) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.revoke_native_session(uuid, uuid, uuid, timestamptz, text, integer, integer, integer) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.change_own_password(uuid, uuid, uuid, uuid, bytea, bytea, integer, integer, integer, text, timestamptz, timestamptz) TO api_runtime_role;
REVOKE ALL ON FUNCTION identity.administrator_state(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.administrator_state(uuid, uuid) TO api_runtime_role;
REVOKE ALL ON FUNCTION identity.admit_login_attempt(timestamptz, integer, integer, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.record_known_login_failure(uuid, uuid, timestamptz, integer, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.resolve_cluster_ids(uuid, text[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.list_authorized_clusters(uuid, uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.admit_login_attempt(timestamptz, integer, integer, integer) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.record_known_login_failure(uuid, uuid, timestamptz, integer, integer) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.resolve_cluster_ids(uuid, text[]) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.list_authorized_clusters(uuid, uuid[]) TO api_runtime_role;

RESET ROLE;
