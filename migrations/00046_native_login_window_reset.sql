-- +goose Up
-- Retain executed migration45. Expired locks cannot carry into a new window,
-- and P2-01 failure clearing keeps its exact pre-auth RLS capability.
SET ROLE schema_owner;

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
  );

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.admit_login_attempt(
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
    next_lock := NULL;
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
CREATE OR REPLACE FUNCTION identity.record_known_login_failure(
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
    next_lock := existing_lock;
    IF existing_window <= p_failed_at - interval '15 minutes' THEN
      next_count := 1;
      next_window := p_failed_at;
      next_lock := NULL;
    ELSE
      next_count := existing_count + 1;
      next_window := existing_window;
    END IF;
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

RESET ROLE;
