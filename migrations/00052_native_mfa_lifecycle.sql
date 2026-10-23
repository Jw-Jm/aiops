-- +goose Up
-- Forward-only repair; migrations 50/51 already ran on the isolated MFA instance.
SET ROLE schema_owner;
ALTER TABLE identity.accounts
  ADD COLUMN mfa_reset_required boolean NOT NULL DEFAULT false,
  ADD COLUMN recovery_failures integer NOT NULL DEFAULT 0 CHECK (recovery_failures BETWEEN 0 AND 5),
  ADD COLUMN recovery_window_at timestamptz;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.mfa_session_proof(
  p_tenant_id uuid, p_user_id uuid, p_session_id uuid,
  p_credential_version integer, p_session_version integer
) RETURNS boolean
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE account_av integer;
BEGIN
  -- Pin account then session before any factor/challenge/proof write. Account
  -- UPDATE also serializes factor replacement and recovery with revocation.
  PERFORM 1 FROM platform.tenants WHERE tenant_id = p_tenant_id AND status = 'active' FOR SHARE;
  IF NOT FOUND THEN RETURN false; END IF;
  SELECT authorization_version INTO account_av FROM identity.accounts
  WHERE tenant_id = p_tenant_id AND user_id = p_user_id
    AND status = 'active' AND password_state = 'active' AND NOT must_change_password
    AND credential_version = p_credential_version AND session_version = p_session_version
  FOR UPDATE;
  IF NOT FOUND THEN RETURN false; END IF;
  PERFORM 1 FROM identity.sessions
  WHERE tenant_id = p_tenant_id AND user_id = p_user_id AND session_id = p_session_id
    AND mode = 'full' AND revoked_at IS NULL
    AND issued_credential_version = p_credential_version
    AND issued_session_version = p_session_version
    AND issued_authorization_version = account_av
    AND clock_timestamp() < idle_expires_at AND clock_timestamp() < absolute_expires_at
  FOR UPDATE;
  RETURN FOUND;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION identity.mfa_lock_session(p_tenant_id uuid, p_user_id uuid, p_session_id uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE cv integer; sv integer;
BEGIN
  SELECT issued_credential_version, issued_session_version INTO cv, sv
  FROM identity.sessions
  WHERE tenant_id=p_tenant_id AND user_id=p_user_id AND session_id=p_session_id;
  RETURN identity.mfa_session_proof(p_tenant_id,p_user_id,p_session_id,cv,sv);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.mfa_lock_session(uuid,uuid,uuid) FROM PUBLIC;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.step_up_recheck(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_step_up_ref uuid,
  p_operation text,
  p_binding_digest bytea,
  p_authorization_version integer,
  p_credential_version integer,
  p_session_version integer,
  p_idle_seconds integer
) RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  accepted boolean := false;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_recheck', true);
  IF NOT identity.mfa_session_proof(p_tenant_id,p_user_id,p_session_id,p_credential_version,p_session_version)
     OR NOT EXISTS (SELECT 1 FROM identity.accounts WHERE tenant_id=p_tenant_id AND user_id=p_user_id
                    AND authorization_version=p_authorization_version) THEN
    PERFORM set_config('app.identity_preauth','disabled',true);
    RETURN false;
  END IF;
  IF p_binding_digest IS NULL OR octet_length(p_binding_digest) <> 32
     OR p_idle_seconds < 1 OR p_idle_seconds > 3600 THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN false;
  END IF;
  -- The high-risk write rechecks, in its own transaction, the same session and
  -- step-up proof the verification response described, plus every version.
  UPDATE identity.step_up_sessions AS proof
  SET idle_expires_at = LEAST(
        proof.absolute_expires_at - interval '1 microsecond',
        clock_timestamp() + make_interval(secs => p_idle_seconds))
  WHERE proof.tenant_id = p_tenant_id AND proof.user_id = p_user_id
    AND proof.step_up_ref = p_step_up_ref
    AND proof.session_id = p_session_id
    AND proof.operation = p_operation
    AND proof.binding_digest = p_binding_digest
    AND proof.verified_methods @> ARRAY['password', 'totp']::text[]
    AND proof.revoked_at IS NULL
    AND proof.issued_credential_version = p_credential_version
    AND proof.issued_authorization_version = p_authorization_version
    AND proof.issued_session_version = p_session_version
    AND clock_timestamp() < proof.idle_expires_at
    AND clock_timestamp() < proof.absolute_expires_at;
  accepted := FOUND;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  RETURN accepted;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.step_up_session_mint(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_challenge_id uuid,
  p_step_up_ref uuid,
  p_idle_seconds integer,
  p_absolute_seconds integer
) RETURNS TABLE (
  out_step_up_ref uuid,
  out_verified_methods text[],
  out_created_at timestamptz,
  out_idle_expires_at timestamptz,
  out_absolute_expires_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  challenge identity.step_up_challenges%ROWTYPE;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF NOT identity.mfa_lock_session(p_tenant_id,p_user_id,p_session_id) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE='28000';
  END IF;
  IF p_idle_seconds < 60 OR p_idle_seconds > 3600
     OR p_absolute_seconds < p_idle_seconds OR p_absolute_seconds > 7200 THEN
    RAISE EXCEPTION 'invalid step-up lifetime' USING ERRCODE = '22023';
  END IF;
  SELECT * INTO challenge FROM identity.step_up_challenges AS candidate
  WHERE candidate.tenant_id = p_tenant_id AND candidate.user_id = p_user_id
    AND candidate.challenge_id = p_challenge_id
  FOR UPDATE;
  IF NOT FOUND
     OR challenge.state <> 'verified'
     OR challenge.session_id <> p_session_id
     OR NOT (challenge.completed_methods @> ARRAY['password', 'totp']::text[])
     OR challenge.verified_at IS NULL
     OR clock_timestamp() >= challenge.absolute_expires_at
     OR clock_timestamp() >= challenge.idle_expires_at THEN
    RAISE EXCEPTION 'step-up challenge is not verified' USING ERRCODE = '28000';
  END IF;
  -- Single use: a verified challenge cannot mint a second step-up session.
  UPDATE identity.step_up_challenges AS updated
  SET state = 'used', used_at = clock_timestamp()
  WHERE updated.tenant_id = p_tenant_id AND updated.challenge_id = p_challenge_id
    AND updated.state = 'verified';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'step-up challenge was already used' USING ERRCODE = '40001';
  END IF;
  INSERT INTO identity.step_up_sessions
    (tenant_id, step_up_ref, challenge_id, user_id, session_id, operation,
     binding_digest, verified_methods, issued_credential_version,
     issued_authorization_version, issued_session_version,
     created_at, idle_expires_at, absolute_expires_at)
  VALUES
    (p_tenant_id, p_step_up_ref, p_challenge_id, p_user_id, p_session_id,
     challenge.operation, challenge.binding_digest, challenge.completed_methods,
     challenge.issued_credential_version, challenge.issued_authorization_version,
     challenge.issued_session_version, clock_timestamp(),
     clock_timestamp() + make_interval(secs => p_idle_seconds),
     clock_timestamp() + make_interval(secs => p_absolute_seconds));
  RETURN QUERY
  SELECT p_step_up_ref, challenge.completed_methods, clock_timestamp(),
         clock_timestamp() + make_interval(secs => p_idle_seconds),
         clock_timestamp() + make_interval(secs => p_absolute_seconds);
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.step_up_challenge_fail(
  p_tenant_id uuid,
  p_user_id uuid,
  p_challenge_id uuid,
  p_session_id uuid,
  p_expected_revision integer
) RETURNS TABLE (out_revision integer, out_state text, out_remaining integer)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF NOT identity.mfa_lock_session(p_tenant_id,p_user_id,p_session_id) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE='28000';
  END IF;
  UPDATE identity.step_up_challenges AS challenge
  SET attempts = challenge.attempts + 1,
      revision = challenge.revision + 1,
      state = CASE WHEN challenge.attempts + 1 >= challenge.attempt_limit
                   THEN 'revoked' ELSE 'pending' END
  WHERE challenge.tenant_id = p_tenant_id AND challenge.user_id = p_user_id
    AND challenge.challenge_id = p_challenge_id
    AND challenge.session_id = p_session_id
    AND challenge.state = 'pending'
    AND challenge.revision = p_expected_revision
    AND clock_timestamp() < challenge.idle_expires_at
    AND clock_timestamp() < challenge.absolute_expires_at
  RETURNING challenge.revision, challenge.state,
            GREATEST(challenge.attempt_limit - challenge.attempts, 0)
  INTO out_revision, out_state, out_remaining;
  IF out_revision IS NULL THEN
    RAISE EXCEPTION 'step-up challenge is not pending at this revision' USING ERRCODE = '40001';
  END IF;
  RETURN NEXT;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.recovery_code_consume(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_challenge_id uuid,
  p_code_digest bytea,
  p_expected_revision integer
) RETURNS TABLE (
  out_remaining integer,
  out_sessions_revoked integer,
  out_challenge_revision integer
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  challenge identity.step_up_challenges%ROWTYPE;
  consumed boolean := false;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'recovery_consume', true);
  IF NOT identity.mfa_lock_session(p_tenant_id,p_user_id,p_session_id) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE='28000';
  END IF;
  IF p_code_digest IS NULL OR octet_length(p_code_digest) <> 32
     OR p_expected_revision IS NULL OR p_expected_revision < 1 THEN
    RAISE EXCEPTION 'invalid recovery request' USING ERRCODE = '22023';
  END IF;
  SELECT * INTO challenge FROM identity.step_up_challenges AS candidate
  WHERE candidate.tenant_id = p_tenant_id AND candidate.user_id = p_user_id
    AND candidate.challenge_id = p_challenge_id
  FOR UPDATE;
  IF NOT FOUND OR challenge.session_id <> p_session_id
     OR challenge.operation <> 'recovery_code_regenerate'
     OR challenge.state <> 'pending'
     OR challenge.revision <> p_expected_revision
     OR clock_timestamp() >= challenge.absolute_expires_at
     OR clock_timestamp() >= challenge.idle_expires_at THEN
    RAISE EXCEPTION 'recovery challenge is not pending' USING ERRCODE = '40001';
  END IF;
  UPDATE identity.accounts SET recovery_failures=0, recovery_window_at=clock_timestamp()
  WHERE tenant_id=p_tenant_id AND user_id=p_user_id
    AND (recovery_window_at IS NULL OR recovery_window_at + interval '15 minutes' <= clock_timestamp());
  IF EXISTS (SELECT 1 FROM identity.accounts WHERE tenant_id=p_tenant_id AND user_id=p_user_id AND recovery_failures>=5) THEN
    RAISE EXCEPTION 'recovery attempt budget exhausted' USING ERRCODE='28000';
  END IF;
  -- Concurrency fence: only one transaction can mark a code used.
  UPDATE identity.mfa_recovery_codes AS code
  SET used_at = clock_timestamp()
  WHERE code.tenant_id = p_tenant_id AND code.user_id = p_user_id
    AND code.code_digest = p_code_digest AND code.used_at IS NULL;
  consumed := FOUND;
  IF NOT consumed THEN
    UPDATE identity.accounts SET recovery_failures=recovery_failures+1
    WHERE tenant_id=p_tenant_id AND user_id=p_user_id;
    UPDATE identity.step_up_challenges SET attempts=attempts+1, revision=revision+1,
      state=CASE WHEN attempts+1>=attempt_limit THEN 'revoked' ELSE 'pending' END
    WHERE tenant_id=p_tenant_id AND challenge_id=p_challenge_id
    RETURNING revision INTO out_challenge_revision;
    -- Return a failed result so the caller commits the budget instead of
    -- rolling the failure back with a verification error.
    RETURN QUERY SELECT -1, 0, out_challenge_revision;
    RETURN;
  END IF;
  -- A recovery code only restores LoA1: it removes every second factor, every
  -- remaining recovery code and every session, so the account must sign in with
  -- its password and re-enrol a factor.
  DELETE FROM identity.mfa_recovery_codes AS code
  WHERE code.tenant_id = p_tenant_id AND code.user_id = p_user_id;
  UPDATE identity.mfa_enrollments AS enrollment
  SET state = 'revoked', revoked_at = clock_timestamp()
  WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
    AND enrollment.state IN ('confirmed', 'pending_confirmation');
  UPDATE identity.accounts AS account
  SET mfa_reset_required = true,
      recovery_failures = 0,
      session_version = account.session_version + 1,
      updated_at = clock_timestamp()
  WHERE account.tenant_id = p_tenant_id AND account.user_id = p_user_id;
  UPDATE identity.sessions AS session
  SET revoked_at = clock_timestamp(),
      revoked_reason = 'recovery_code_used'
  WHERE session.tenant_id = p_tenant_id AND session.user_id = p_user_id
    AND session.revoked_at IS NULL;
  GET DIAGNOSTICS out_sessions_revoked = ROW_COUNT;
  -- The challenge is consumed without claiming a verified second factor: the
  -- completed method set stays a subset of the required password+TOTP set.
  UPDATE identity.step_up_challenges AS updated
  SET state = 'used', used_at = clock_timestamp(),
      revision = updated.revision + 1
  WHERE updated.tenant_id = p_tenant_id AND updated.challenge_id = p_challenge_id;
  RETURN QUERY SELECT 0, out_sessions_revoked,
    (SELECT revision FROM identity.step_up_challenges AS current
     WHERE current.tenant_id = p_tenant_id AND current.challenge_id = p_challenge_id);
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.mfa_begin_enrollment(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_enrollment_id uuid,
  p_ciphertext text,
  p_key_ref text,
  p_key_version text,
  p_credential_version integer,
  p_session_version integer,
  p_expires_at timestamptz
) RETURNS TABLE (out_revision integer, out_expires_at timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity, platform
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'mfa_enroll', true);
  IF p_tenant_id IS NULL OR p_user_id IS NULL OR p_session_id IS NULL
     OR p_enrollment_id IS NULL OR p_ciphertext IS NULL OR length(p_ciphertext) < 16
     OR p_key_ref IS NULL OR p_key_version IS NULL
     OR p_credential_version IS NULL OR p_session_version IS NULL
     OR p_expires_at IS NULL OR p_expires_at <= clock_timestamp()
     OR p_expires_at > clock_timestamp() + interval '20 minutes' THEN
    RAISE EXCEPTION 'invalid mfa enrollment request' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.mfa_session_proof(p_tenant_id, p_user_id, p_session_id, p_credential_version, p_session_version) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  -- Serialize concurrent enrolment attempts for the same account.
  PERFORM 1 FROM identity.accounts AS account
  WHERE account.tenant_id = p_tenant_id AND account.user_id = p_user_id
  FOR UPDATE OF account;

  UPDATE identity.mfa_enrollments SET state='revoked', revoked_at=clock_timestamp()
  WHERE tenant_id=p_tenant_id AND user_id=p_user_id AND state='pending_confirmation';
  INSERT INTO identity.mfa_enrollments
    (tenant_id, enrollment_id, user_id, revision, method, secret_ciphertext,
     secret_key_ref, secret_key_version, state, expires_at)
  VALUES
    (p_tenant_id, p_enrollment_id, p_user_id, 1, 'totp', p_ciphertext,
     p_key_ref, p_key_version, 'pending_confirmation', p_expires_at);
  RETURN QUERY SELECT 1, p_expires_at;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.mfa_confirm_enrollment(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_enrollment_id uuid,
  p_expected_revision integer,
  p_credential_version integer,
  p_session_version integer,
  p_last_used_step bigint,
  p_recovery_digests bytea[]
) RETURNS TABLE (out_revision integer, out_confirmed_at timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  next_revision integer;
  confirmed timestamptz;
  digest bytea;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'mfa_confirm', true);
  IF p_expected_revision IS NULL OR p_expected_revision < 1
     OR p_last_used_step IS NULL OR p_last_used_step < 0
     OR p_recovery_digests IS NULL OR cardinality(p_recovery_digests) <> 10 THEN
    RAISE EXCEPTION 'invalid mfa confirmation request' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.mfa_session_proof(p_tenant_id, p_user_id, p_session_id, p_credential_version, p_session_version) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  -- Keep the prior factor until this atomic confirmation succeeds.
  UPDATE identity.mfa_enrollments SET state='revoked', revoked_at=clock_timestamp()
  WHERE tenant_id=p_tenant_id AND user_id=p_user_id AND state='confirmed';
  UPDATE identity.mfa_enrollments AS enrollment
  SET state = 'confirmed',
      confirmed_at = clock_timestamp(),
      revision = enrollment.revision + 1,
      last_used_step = p_last_used_step
  WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
    AND enrollment.enrollment_id = p_enrollment_id
    AND enrollment.state = 'pending_confirmation'
    AND enrollment.revision = p_expected_revision
    AND clock_timestamp() < enrollment.expires_at
  RETURNING enrollment.revision, enrollment.confirmed_at INTO next_revision, confirmed;
  IF next_revision IS NULL THEN
    RAISE EXCEPTION 'mfa enrollment is not confirmable' USING ERRCODE = '40001';
  END IF;

  DELETE FROM identity.mfa_recovery_codes AS code
  WHERE code.tenant_id = p_tenant_id AND code.user_id = p_user_id;
  FOREACH digest IN ARRAY p_recovery_digests LOOP
    IF octet_length(digest) <> 32 THEN
      RAISE EXCEPTION 'invalid recovery code digest' USING ERRCODE = '22023';
    END IF;
    INSERT INTO identity.mfa_recovery_codes (tenant_id, user_id, code_digest)
    VALUES (p_tenant_id, p_user_id, digest);
  END LOOP;

  -- Any other pending enrolment is superseded by the confirmed one.
  UPDATE identity.mfa_enrollments AS enrollment
  SET state = 'revoked', revoked_at = clock_timestamp()
  WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
    AND enrollment.state = 'pending_confirmation';

  UPDATE identity.accounts SET mfa_reset_required=false
  WHERE tenant_id=p_tenant_id AND user_id=p_user_id;
  RETURN QUERY SELECT next_revision, confirmed;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.mfa_admin_reset(
  p_tenant_id uuid,
  p_admin_user_id uuid,
  p_target_user_id uuid,
  p_confirm_login_name text,
  p_expected_revision integer,
  p_expected_credential_version integer
) RETURNS TABLE (
  out_credential_version integer,
  out_session_version integer,
  out_authorization_version integer,
  out_sessions_revoked integer,
  out_enrollments_removed integer,
  out_recovery_codes_removed integer
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  revoked integer := 0;
  enrollments integer := 0;
  codes integer := 0;
  target_login text;
  target_revision integer;
  target_credential_version integer;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'mfa_reset', true);
  IF p_admin_user_id IS NULL OR p_target_user_id IS NULL
     OR p_confirm_login_name IS NULL OR btrim(p_confirm_login_name) = ''
     OR p_expected_revision IS NULL OR p_expected_revision < 1
     OR p_expected_credential_version IS NULL OR p_expected_credential_version < 1 THEN
    RAISE EXCEPTION 'invalid mfa reset request' USING ERRCODE = '22023';
  END IF;
  -- Lock order: target account first, then its sessions/enrollments. This is the
  -- same order authorization and password rotation use. The login name and both
  -- versions are verified under the same row lock, so a concurrent change
  -- between the check and the write cannot slip through.
  SELECT candidate.login_name, candidate.account_revision, candidate.credential_version
  INTO target_login, target_revision, target_credential_version
  FROM identity.accounts AS candidate
  WHERE candidate.tenant_id = p_tenant_id AND candidate.user_id = p_target_user_id
  FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'mfa reset target is unavailable' USING ERRCODE = '40001';
  END IF;
  IF lower(target_login) <> lower(btrim(p_confirm_login_name))
     OR target_revision <> p_expected_revision
     OR target_credential_version <> p_expected_credential_version THEN
    RAISE EXCEPTION 'mfa reset target confirmation does not match' USING ERRCODE = '40001';
  END IF;
  UPDATE identity.accounts AS account
  SET mfa_reset_required = true,
      credential_version = account.credential_version + 1,
      session_version = account.session_version + 1,
      account_revision = account.account_revision + 1,
      updated_at = clock_timestamp()
  WHERE account.tenant_id = p_tenant_id AND account.user_id = p_target_user_id
  RETURNING account.credential_version, account.session_version,
            account.authorization_version
  INTO out_credential_version, out_session_version, out_authorization_version;
  UPDATE identity.sessions AS session
  SET revoked_at = clock_timestamp(), revoked_reason = 'mfa_reset_by_administrator'
  WHERE session.tenant_id = p_tenant_id AND session.user_id = p_target_user_id
    AND session.revoked_at IS NULL;
  GET DIAGNOSTICS revoked = ROW_COUNT;
  UPDATE identity.mfa_enrollments AS enrollment
  SET state = 'revoked', revoked_at = clock_timestamp()
  WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_target_user_id
    AND enrollment.state IN ('confirmed', 'pending_confirmation');
  GET DIAGNOSTICS enrollments = ROW_COUNT;
  DELETE FROM identity.mfa_recovery_codes AS code
  WHERE code.tenant_id = p_tenant_id AND code.user_id = p_target_user_id;
  GET DIAGNOSTICS codes = ROW_COUNT;
  UPDATE identity.step_up_sessions AS proof
  SET revoked_at = clock_timestamp()
  WHERE proof.tenant_id = p_tenant_id AND proof.user_id = p_target_user_id
    AND proof.revoked_at IS NULL;
  UPDATE identity.step_up_challenges AS challenge
  SET state = 'revoked'
  WHERE challenge.tenant_id = p_tenant_id AND challenge.user_id = p_target_user_id
    AND challenge.state = 'pending';
  RETURN QUERY SELECT out_credential_version, out_session_version,
    out_authorization_version, revoked, enrollments, codes;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.step_up_challenge_create(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_challenge_id uuid,
  p_operation text,
  p_target jsonb,
  p_binding_digest bytea,
  p_credential_version integer,
  p_authorization_version integer,
  p_session_version integer,
  p_idle_expires_at timestamptz,
  p_absolute_expires_at timestamptz
) RETURNS TABLE (
  out_challenge_id uuid,
  out_revision integer,
  out_attempt_limit integer,
  out_created_at timestamptz,
  out_idle_expires_at timestamptz,
  out_absolute_expires_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_create', true);
  IF p_challenge_id IS NULL OR p_operation IS NULL OR p_target IS NULL
     OR jsonb_typeof(p_target) <> 'object'
     OR p_binding_digest IS NULL OR octet_length(p_binding_digest) <> 32
     OR p_idle_expires_at <= clock_timestamp()
     OR p_absolute_expires_at <= p_idle_expires_at
     OR p_absolute_expires_at > clock_timestamp() + interval '15 minutes' THEN
    RAISE EXCEPTION 'invalid step-up challenge request' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.mfa_session_proof(p_tenant_id, p_user_id, p_session_id, p_credential_version, p_session_version) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  IF EXISTS (SELECT 1 FROM identity.accounts WHERE tenant_id=p_tenant_id AND user_id=p_user_id AND mfa_reset_required)
     AND p_operation <> 'mfa_enrollment' THEN
    RAISE EXCEPTION 'mfa setup is required' USING ERRCODE='28000';
  END IF;
  -- Only one pending challenge per session and operation: obsolete attempts are
  -- revoked instead of accumulating replay windows.
  UPDATE identity.step_up_challenges AS challenge
  SET state = 'revoked'
  WHERE challenge.tenant_id = p_tenant_id AND challenge.user_id = p_user_id
    AND challenge.session_id = p_session_id
    AND challenge.operation = p_operation
    AND challenge.state = 'pending';
  INSERT INTO identity.step_up_challenges
    (tenant_id, challenge_id, user_id, session_id, operation, target,
     binding_digest, revision, state, required_methods, completed_methods,
     attempts, attempt_limit, issued_credential_version,
     issued_authorization_version, issued_session_version,
     created_at, idle_expires_at, absolute_expires_at)
  VALUES
    (p_tenant_id, p_challenge_id, p_user_id, p_session_id, p_operation,
     p_target, p_binding_digest, 1, 'pending',
     ARRAY['password', 'totp'], ARRAY[]::text[], 0, 5,
     p_credential_version, p_authorization_version, p_session_version,
     clock_timestamp(), p_idle_expires_at, p_absolute_expires_at);
  RETURN QUERY
  SELECT p_challenge_id, 1, 5, clock_timestamp(), p_idle_expires_at, p_absolute_expires_at;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.mfa_state(
  p_tenant_id uuid,
  p_user_id uuid
) RETURNS TABLE (
  out_state text,
  out_enrollment_id uuid,
  out_confirmed_at timestamptz,
  out_recovery_codes_remaining integer
)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'mfa_confirm', true);
  RETURN QUERY
  SELECT COALESCE(enrollment.state, CASE WHEN EXISTS (SELECT 1 FROM identity.accounts WHERE tenant_id=p_tenant_id AND user_id=p_user_id AND mfa_reset_required) THEN 'reset_required' ELSE 'none' END),
         enrollment.enrollment_id,
         enrollment.confirmed_at,
         (SELECT count(*)::integer FROM identity.mfa_recovery_codes AS code
          WHERE code.tenant_id = p_tenant_id AND code.user_id = p_user_id
            AND code.used_at IS NULL)
  FROM (SELECT 1) AS singleton
  LEFT JOIN LATERAL (
    SELECT candidate.state, candidate.enrollment_id, candidate.confirmed_at
    FROM identity.mfa_enrollments AS candidate
    WHERE candidate.tenant_id = p_tenant_id AND candidate.user_id = p_user_id
      AND candidate.state IN ('confirmed', 'pending_confirmation')
    ORDER BY (candidate.state = 'confirmed') DESC, candidate.created_at DESC
    LIMIT 1
  ) AS enrollment ON true;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION identity.mfa_setup_required(p_tenant_id uuid,p_user_id uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog,identity AS $$
DECLARE required boolean;
BEGIN
  PERFORM set_config('app.identity_preauth','disabled',true);
  PERFORM set_config('app.identity_preauth','mfa_confirm',true);
  SELECT mfa_reset_required INTO required FROM identity.accounts WHERE tenant_id=p_tenant_id AND user_id=p_user_id;
  PERFORM set_config('app.identity_preauth','disabled',true);
  RETURN COALESCE(required,true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.mfa_setup_required(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.mfa_setup_required(uuid,uuid) TO api_runtime_role;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.authorize_native_session_role(
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
    AND NOT account.mfa_reset_required
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
RESET ROLE;
-- +goose Down
-- Forward-only: preserve factor recovery and revocation state.
