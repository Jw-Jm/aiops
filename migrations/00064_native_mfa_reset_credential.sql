-- +goose Up
-- Forward-only repair (MR-014): an administrator MFA reset must not take the
-- account's only usable password version away.
--
-- The frozen 00051/00052 definition increments accounts.credential_version and
-- revokes every session, but never writes the matching identity.credentials row.
-- find_login_account joins credentials on the exact account version, so the
-- unchanged password could no longer be presented at sign-in and the account
-- could never reach the MFA re-enrolment gate.
--
-- The replacement keeps the same lock order (target account first, then
-- credentials/sessions/factors), copies the current active password hash to the
-- new version, retires the previous version, and only then bumps the account.
-- It never reads the plaintext, never lowers a version, never restores a revoked
-- session, and never asks an administrator to set a second password.
--
-- An account that already lost this invariant (credential_version with no active
-- credential row) is refused with SQLSTATE 55000 instead of guessing which
-- historical hash to copy. Such a broken account is repaired only through the
-- evidence-gated procedure in docs/runbooks/native-mfa-recovery.md.
SET ROLE schema_owner;
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
  preserved_hash text;
  next_credential_version integer;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'mfa_reset', true);
  IF p_admin_user_id IS NULL OR p_target_user_id IS NULL
     OR p_confirm_login_name IS NULL OR btrim(p_confirm_login_name) = ''
     OR p_expected_revision IS NULL OR p_expected_revision < 1
     OR p_expected_credential_version IS NULL OR p_expected_credential_version < 1 THEN
    RAISE EXCEPTION 'invalid mfa reset request' USING ERRCODE = '22023';
  END IF;
  -- Lock order: target account first, then its credentials/sessions/factors.
  -- The login name and both versions are verified under the same row lock, so a
  -- concurrent change between the check and the write cannot slip through.
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

  -- Read the exact active credential of the version the account currently
  -- presents. A different version, a retired row, or a missing row is not
  -- "close enough": copying any of them would silently change the password.
  SELECT credential.password_hash INTO preserved_hash
  FROM identity.credentials AS credential
  WHERE credential.tenant_id = p_tenant_id
    AND credential.user_id = p_target_user_id
    AND credential.credential_version = target_credential_version
    AND credential.status = 'active';
  IF preserved_hash IS NULL THEN
    RAISE EXCEPTION 'mfa reset target has no active password credential'
      USING ERRCODE = '55000';
  END IF;

  next_credential_version := target_credential_version + 1;
  -- The hash moves forward unchanged: no re-hash, no version reuse, no plaintext.
  INSERT INTO identity.credentials
    (tenant_id, user_id, credential_version, password_hash, status)
  VALUES
    (p_tenant_id, p_target_user_id, next_credential_version, preserved_hash, 'active');
  UPDATE identity.credentials AS credential
  SET status = 'retired'
  WHERE credential.tenant_id = p_tenant_id
    AND credential.user_id = p_target_user_id
    AND credential.credential_version <> next_credential_version
    AND credential.status = 'active';

  UPDATE identity.accounts AS account
  SET mfa_reset_required = true,
      credential_version = next_credential_version,
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
RESET ROLE;
