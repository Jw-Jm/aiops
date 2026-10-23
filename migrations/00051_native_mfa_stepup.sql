-- +goose Up
-- Forward-only native MFA (TOTP), step-up challenges and recovery (P2-03).
--
-- Design boundary, unchanged from migrations 44-50:
--   * API/Worker hold no direct identity-table grants; every operation below is
--     a fixed SECURITY DEFINER function owned by schema_owner with a reset-then-
--     exact pre-auth context, a fixed search_path and FORCE RLS.
--   * No TOTP secret, recovery code or credential material is ever stored in
--     plaintext. The caller passes an already sealed TOTP secret (the sealing
--     key lives in the task-scoped secret backend, never in the database) and
--     only SHA-256 digests of recovery codes.
--   * Nothing here upgrades a session by itself: a challenge becomes verified
--     only after the server accumulated password + TOTP, and a step-up session
--     is minted from a verified challenge. Recovery codes never mint one.
SET ROLE schema_owner;

-- Single gate for the MFA/step-up function family. It has the same shape as the
-- existing pre-auth helpers: caller-supplied GUCs alone are not enough because
-- the security-definer user and an api-duty session are also required.
-- +goose StatementBegin
CREATE FUNCTION identity.mfa_scope_active()
RETURNS boolean
LANGUAGE sql
STABLE
SET search_path = pg_catalog, identity
AS $$
  SELECT current_user = 'schema_owner'
    AND session_user <> current_user
    AND pg_has_role(session_user, 'api_runtime_role', 'member')
    AND current_setting('app.identity_preauth', true) IN (
      'mfa_enroll', 'mfa_confirm', 'mfa_reset',
      'stepup_create', 'stepup_verify', 'stepup_recheck',
      'recovery_consume'
    );
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.mfa_scope_active() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.mfa_scope_active() TO api_runtime_role, migration_role;

CREATE TABLE identity.mfa_enrollments (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  enrollment_id uuid NOT NULL,
  user_id uuid NOT NULL,
  revision integer NOT NULL DEFAULT 1 CHECK (revision BETWEEN 1 AND 2147483647),
  method text NOT NULL CHECK (method = 'totp'),
  -- Ciphertext only: the sealed provisioning secret is unreadable without the
  -- task-scoped key backend and is never returned after creation.
  secret_ciphertext text NOT NULL CHECK (length(secret_ciphertext) BETWEEN 16 AND 16384),
  secret_key_ref text NOT NULL CHECK (length(secret_key_ref) BETWEEN 1 AND 256),
  secret_key_version text NOT NULL CHECK (length(secret_key_version) BETWEEN 1 AND 64),
  state text NOT NULL CHECK (state IN ('pending_confirmation', 'confirmed', 'expired', 'revoked')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  expires_at timestamptz NOT NULL,
  confirmed_at timestamptz,
  revoked_at timestamptz,
  -- Highest accepted TOTP time step: a code cannot be replayed in its window.
  last_used_step bigint,
  PRIMARY KEY (tenant_id, enrollment_id),
  UNIQUE (user_id, enrollment_id),
  CHECK (expires_at > created_at),
  CHECK (
    (state = 'confirmed' AND confirmed_at IS NOT NULL AND revoked_at IS NULL)
    OR (state = 'revoked')
    OR (state <> 'confirmed' AND confirmed_at IS NULL AND revoked_at IS NULL)
  ),
  CHECK (state <> 'revoked' OR revoked_at IS NOT NULL),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);
CREATE UNIQUE INDEX mfa_enrollments_confirmed_unique
  ON identity.mfa_enrollments (tenant_id, user_id) WHERE state = 'confirmed';
CREATE UNIQUE INDEX mfa_enrollments_pending_unique
  ON identity.mfa_enrollments (tenant_id, user_id) WHERE state = 'pending_confirmation';

CREATE TABLE identity.mfa_recovery_codes (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  user_id uuid NOT NULL,
  code_digest bytea NOT NULL CHECK (octet_length(code_digest) = 32),
  issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  used_at timestamptz,
  PRIMARY KEY (tenant_id, user_id, code_digest),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);

CREATE TABLE identity.step_up_challenges (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  challenge_id uuid NOT NULL,
  user_id uuid NOT NULL,
  session_id uuid NOT NULL,
  operation text NOT NULL CHECK (operation IN (
    'command_execution', 'user_administration', 'mfa_enrollment', 'mfa_reset',
    'recovery_code_regenerate', 'source_administration', 'cluster_administration',
    'registry_administration', 'execution_profile_administration'
  )),
  target jsonb NOT NULL CHECK (jsonb_typeof(target) = 'object'),
  binding_digest bytea NOT NULL CHECK (octet_length(binding_digest) = 32),
  revision integer NOT NULL DEFAULT 1 CHECK (revision BETWEEN 1 AND 2147483647),
  state text NOT NULL CHECK (state IN ('pending', 'verified', 'expired', 'used', 'revoked')),
  required_methods text[] NOT NULL CHECK (cardinality(required_methods) = 2),
  completed_methods text[] NOT NULL DEFAULT ARRAY[]::text[],
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 100),
  attempt_limit integer NOT NULL DEFAULT 5 CHECK (attempt_limit BETWEEN 1 AND 5),
  issued_credential_version integer NOT NULL CHECK (issued_credential_version > 0),
  issued_authorization_version integer NOT NULL CHECK (issued_authorization_version > 0),
  issued_session_version integer NOT NULL CHECK (issued_session_version > 0),
  created_at timestamptz NOT NULL,
  idle_expires_at timestamptz NOT NULL,
  absolute_expires_at timestamptz NOT NULL,
  verified_at timestamptz,
  used_at timestamptz,
  PRIMARY KEY (tenant_id, challenge_id),
  UNIQUE (session_id, challenge_id),
  CHECK (created_at < idle_expires_at AND idle_expires_at < absolute_expires_at),
  CHECK (completed_methods <@ required_methods),
  CHECK ((state = 'verified') = (verified_at IS NOT NULL) OR state = 'used'),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);
CREATE INDEX step_up_challenges_session_idx
  ON identity.step_up_challenges (tenant_id, session_id, operation, created_at DESC);

CREATE TABLE identity.step_up_sessions (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  step_up_ref uuid NOT NULL,
  challenge_id uuid NOT NULL,
  user_id uuid NOT NULL,
  session_id uuid NOT NULL,
  operation text NOT NULL,
  binding_digest bytea NOT NULL CHECK (octet_length(binding_digest) = 32),
  verified_methods text[] NOT NULL CHECK (cardinality(verified_methods) = 2),
  issued_credential_version integer NOT NULL CHECK (issued_credential_version > 0),
  issued_authorization_version integer NOT NULL CHECK (issued_authorization_version > 0),
  issued_session_version integer NOT NULL CHECK (issued_session_version > 0),
  created_at timestamptz NOT NULL,
  idle_expires_at timestamptz NOT NULL,
  absolute_expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  PRIMARY KEY (tenant_id, step_up_ref),
  UNIQUE (tenant_id, challenge_id),
  CHECK (created_at < idle_expires_at AND idle_expires_at < absolute_expires_at),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);
CREATE INDEX step_up_sessions_session_idx
  ON identity.step_up_sessions (tenant_id, session_id, operation) WHERE revoked_at IS NULL;

ALTER TABLE identity.mfa_enrollments ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.mfa_enrollments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.mfa_enrollments
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  );

ALTER TABLE identity.mfa_recovery_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.mfa_recovery_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.mfa_recovery_codes
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  );

ALTER TABLE identity.step_up_challenges ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.step_up_challenges FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.step_up_challenges
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  );

ALTER TABLE identity.step_up_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.step_up_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.step_up_sessions
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  );

-- Account/session/credential writes from this family (admin reset, recovery,
-- confirm) must pass the same policies as the existing session operations.
-- ALTER POLICY replaces USING wholesale, so every earlier branch is restated.
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
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('create_session')
    OR identity.session_context_active('change_own_password')
    OR identity.session_context_active('record_password_failure')
    OR identity.session_context_active('revoke_session')
    OR identity.mfa_scope_active()
  );

ALTER POLICY tenant_isolation ON identity.sessions
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('find_session')
    OR identity.session_context_active('touch_session')
    OR identity.session_context_active('revoke_session')
    OR identity.session_context_active('change_own_password')
    OR identity.session_context_active('authorize_native_role')
    OR identity.mfa_scope_active()
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('create_session')
    OR identity.session_context_active('touch_session')
    OR identity.session_context_active('revoke_session')
    OR identity.session_context_active('change_own_password')
    OR identity.mfa_scope_active()
  );

ALTER POLICY tenant_isolation ON identity.credentials
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('find_login_account')
    OR identity.session_context_active('find_session')
    OR identity.session_context_active('change_own_password')
    OR identity.mfa_scope_active()
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.session_context_active('change_own_password')
    OR identity.mfa_scope_active()
  );

-- Verifies the trusted session and returns it without locking: the caller's
-- further statements lock only what they own (account, enrollment, challenge).
-- +goose StatementBegin
CREATE FUNCTION identity.mfa_session_proof(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_credential_version integer,
  p_session_version integer
) RETURNS boolean
LANGUAGE sql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
  SELECT EXISTS (
    SELECT 1
    FROM identity.sessions AS session
    JOIN identity.accounts AS account
      ON account.tenant_id = session.tenant_id
     AND account.user_id = session.user_id
    WHERE session.tenant_id = p_tenant_id
      AND session.session_id = p_session_id
      AND session.user_id = p_user_id
      AND session.mode = 'full'
      AND session.revoked_at IS NULL
      AND session.issued_credential_version = p_credential_version
      AND session.issued_session_version = p_session_version
      AND account.status = 'active'
      AND account.credential_version = p_credential_version
      AND account.session_version = p_session_version
      AND clock_timestamp() < session.idle_expires_at
      AND clock_timestamp() < session.absolute_expires_at
  );
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.mfa_session_proof(uuid, uuid, uuid, integer, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.mfa_session_proof(uuid, uuid, uuid, integer, integer) TO api_runtime_role;

-- ---------------------------------------------------------------------------
-- MFA enrolment
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE FUNCTION identity.mfa_begin_enrollment(
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
DECLARE
  has_confirmed boolean := false;
  has_step_up boolean := false;
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

  SELECT EXISTS (
    SELECT 1 FROM identity.mfa_enrollments AS enrollment
    WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
      AND enrollment.state = 'confirmed'
  ) INTO has_confirmed;
  IF has_confirmed THEN
    -- Replacing an existing second factor is a high-risk change: it must not be
    -- possible with only the logged-in cookie and an attacker-chosen new seed.
    SELECT EXISTS (
      SELECT 1 FROM identity.step_up_sessions AS proof
      WHERE proof.tenant_id = p_tenant_id AND proof.user_id = p_user_id
        AND proof.session_id = p_session_id
        AND proof.operation = 'mfa_enrollment'
        AND proof.revoked_at IS NULL
        AND clock_timestamp() < proof.idle_expires_at
        AND clock_timestamp() < proof.absolute_expires_at
        AND proof.issued_credential_version = p_credential_version
        AND proof.issued_session_version = p_session_version
    ) INTO has_step_up;
    IF NOT has_step_up THEN
      RAISE EXCEPTION 'replacing a confirmed factor requires step-up' USING ERRCODE = '28000';
    END IF;
    UPDATE identity.mfa_enrollments AS enrollment
    SET state = 'revoked', revoked_at = clock_timestamp()
    WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
      AND enrollment.state IN ('confirmed', 'pending_confirmation');
    DELETE FROM identity.mfa_recovery_codes AS code
    WHERE code.tenant_id = p_tenant_id AND code.user_id = p_user_id;
  ELSE
    UPDATE identity.mfa_enrollments AS enrollment
    SET state = 'revoked', revoked_at = clock_timestamp()
    WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
      AND enrollment.state = 'pending_confirmation';
  END IF;

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
CREATE FUNCTION identity.mfa_enrollment_view(
  p_tenant_id uuid,
  p_user_id uuid,
  p_enrollment_id uuid
) RETURNS TABLE (
  out_enrollment_id uuid,
  out_state text,
  out_revision integer,
  out_created_at timestamptz,
  out_expires_at timestamptz,
  out_confirmed_at timestamptz
)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'mfa_confirm', true);
  -- A pending enrolment that ran out of time is reported as expired, not as an
  -- endless pending state.
  UPDATE identity.mfa_enrollments AS enrollment
  SET state = 'expired'
  WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
    AND enrollment.enrollment_id = p_enrollment_id
    AND enrollment.state = 'pending_confirmation'
    AND clock_timestamp() >= enrollment.expires_at;
  RETURN QUERY
  SELECT enrollment.enrollment_id, enrollment.state, enrollment.revision,
         enrollment.created_at, enrollment.expires_at, enrollment.confirmed_at
  FROM identity.mfa_enrollments AS enrollment
  WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
    AND enrollment.enrollment_id = p_enrollment_id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.mfa_enrollment_secret(
  p_tenant_id uuid,
  p_user_id uuid,
  p_enrollment_id uuid,
  p_state text
) RETURNS TABLE (
  out_enrollment_id uuid,
  out_revision integer,
  out_state text,
  out_ciphertext text,
  out_key_ref text,
  out_key_version text,
  out_expires_at timestamptz,
  out_last_used_step bigint
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'mfa_confirm', true);
  IF p_state NOT IN ('pending_confirmation', 'confirmed') THEN
    RETURN;
  END IF;
  RETURN QUERY
  SELECT enrollment.enrollment_id, enrollment.revision, enrollment.state,
         enrollment.secret_ciphertext, enrollment.secret_key_ref,
         enrollment.secret_key_version, enrollment.expires_at,
         enrollment.last_used_step
  FROM identity.mfa_enrollments AS enrollment
  WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
    AND enrollment.enrollment_id = p_enrollment_id
    AND enrollment.state = p_state;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.mfa_confirm_enrollment(
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

  RETURN QUERY SELECT next_revision, confirmed;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.mfa_record_totp_step(
  p_tenant_id uuid,
  p_user_id uuid,
  p_enrollment_id uuid,
  p_step bigint
) RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  accepted boolean := false;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF p_step IS NULL OR p_step < 0 THEN
    RETURN false;
  END IF;
  -- A TOTP code is accepted once per time step. The conditional update is the
  -- concurrency fence: two parallel verifications cannot both consume it.
  UPDATE identity.mfa_enrollments AS enrollment
  SET last_used_step = p_step
  WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
    AND enrollment.enrollment_id = p_enrollment_id
    AND enrollment.state = 'confirmed'
    AND (enrollment.last_used_step IS NULL OR enrollment.last_used_step < p_step);
  accepted := FOUND;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  RETURN accepted;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.mfa_state(
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
  SELECT COALESCE(enrollment.state, 'none'),
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

-- ---------------------------------------------------------------------------
-- Step-up challenges and sessions
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE FUNCTION identity.step_up_challenge_create(
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
CREATE FUNCTION identity.step_up_challenge_get(
  p_tenant_id uuid,
  p_user_id uuid,
  p_challenge_id uuid
) RETURNS TABLE (
  out_challenge_id uuid,
  out_session_id uuid,
  out_operation text,
  out_target jsonb,
  out_binding_digest bytea,
  out_state text,
  out_revision integer,
  out_required_methods text[],
  out_completed_methods text[],
  out_attempts integer,
  out_attempt_limit integer,
  out_issued_credential_version integer,
  out_issued_authorization_version integer,
  out_issued_session_version integer,
  out_created_at timestamptz,
  out_idle_expires_at timestamptz,
  out_absolute_expires_at timestamptz,
  out_verified_at timestamptz
)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  UPDATE identity.step_up_challenges AS challenge
  SET state = 'expired'
  WHERE challenge.tenant_id = p_tenant_id AND challenge.user_id = p_user_id
    AND challenge.challenge_id = p_challenge_id
    AND challenge.state = 'pending'
    AND (clock_timestamp() >= challenge.idle_expires_at
         OR clock_timestamp() >= challenge.absolute_expires_at);
  RETURN QUERY
  SELECT challenge.challenge_id, challenge.session_id, challenge.operation,
         challenge.target, challenge.binding_digest, challenge.state,
         challenge.revision, challenge.required_methods,
         challenge.completed_methods, challenge.attempts,
         challenge.attempt_limit, challenge.issued_credential_version,
         challenge.issued_authorization_version, challenge.issued_session_version,
         challenge.created_at, challenge.idle_expires_at,
         challenge.absolute_expires_at, challenge.verified_at
  FROM identity.step_up_challenges AS challenge
  WHERE challenge.tenant_id = p_tenant_id AND challenge.user_id = p_user_id
    AND challenge.challenge_id = p_challenge_id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.step_up_challenge_add_method(
  p_tenant_id uuid,
  p_user_id uuid,
  p_challenge_id uuid,
  p_session_id uuid,
  p_expected_revision integer,
  p_method text,
  p_credential_version integer,
  p_session_version integer,
  p_idle_expires_at timestamptz
) RETURNS TABLE (
  out_revision integer,
  out_state text,
  out_completed_methods text[],
  out_verified_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  next_methods text[];
  next_state text;
  verified timestamptz;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF p_method NOT IN ('password', 'totp') THEN
    RAISE EXCEPTION 'invalid step-up method' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.mfa_session_proof(p_tenant_id, p_user_id, p_session_id, p_credential_version, p_session_version) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  UPDATE identity.step_up_challenges AS challenge
  SET completed_methods = challenge.completed_methods || p_method,
      revision = challenge.revision + 1,
      idle_expires_at = LEAST(p_idle_expires_at, challenge.absolute_expires_at - interval '1 microsecond'),
      state = CASE
        WHEN (challenge.completed_methods || p_method) @> ARRAY['password', 'totp']::text[]
          THEN 'verified'
        ELSE 'pending'
      END,
      verified_at = CASE
        WHEN (challenge.completed_methods || p_method) @> ARRAY['password', 'totp']::text[]
          THEN clock_timestamp()
        ELSE NULL
      END
  WHERE challenge.tenant_id = p_tenant_id AND challenge.user_id = p_user_id
    AND challenge.challenge_id = p_challenge_id
    AND challenge.session_id = p_session_id
    AND challenge.state = 'pending'
    AND challenge.revision = p_expected_revision
    AND NOT (challenge.completed_methods @> ARRAY[p_method]::text[])
    AND challenge.completed_methods <@ ARRAY['password', 'totp']::text[]
    AND clock_timestamp() < challenge.idle_expires_at
    AND clock_timestamp() < challenge.absolute_expires_at
    AND p_method = ANY (challenge.required_methods)
  RETURNING challenge.revision, challenge.state, challenge.completed_methods,
            challenge.verified_at
  INTO out_revision, out_state, out_completed_methods, out_verified_at;
  IF out_revision IS NULL THEN
    RAISE EXCEPTION 'step-up challenge is not pending at this revision' USING ERRCODE = '40001';
  END IF;
  RETURN NEXT;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.step_up_challenge_fail(
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
CREATE FUNCTION identity.step_up_session_mint(
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
     OR clock_timestamp() >= challenge.absolute_expires_at THEN
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
CREATE FUNCTION identity.step_up_session_view(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid
) RETURNS TABLE (
  out_step_up_ref uuid,
  out_operation text,
  out_binding_digest bytea,
  out_verified_methods text[],
  out_idle_expires_at timestamptz,
  out_absolute_expires_at timestamptz
)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_recheck', true);
  RETURN QUERY
  SELECT proof.step_up_ref, proof.operation, proof.binding_digest,
         proof.verified_methods, proof.idle_expires_at, proof.absolute_expires_at
  FROM identity.step_up_sessions AS proof
  WHERE proof.tenant_id = p_tenant_id AND proof.user_id = p_user_id
    AND proof.session_id = p_session_id
    AND proof.revoked_at IS NULL
    AND clock_timestamp() < proof.idle_expires_at
    AND clock_timestamp() < proof.absolute_expires_at
  ORDER BY proof.created_at DESC
  LIMIT 1;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.step_up_recheck(
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

-- ---------------------------------------------------------------------------
-- Recovery codes and administrator reset
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE FUNCTION identity.recovery_code_consume(
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
  -- Concurrency fence: only one transaction can mark a code used.
  UPDATE identity.mfa_recovery_codes AS code
  SET used_at = clock_timestamp()
  WHERE code.tenant_id = p_tenant_id AND code.user_id = p_user_id
    AND code.code_digest = p_code_digest AND code.used_at IS NULL;
  consumed := FOUND;
  IF NOT consumed THEN
    RETURN; -- unknown or already used: the caller reports a failed verification
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
  SET session_version = account.session_version + 1,
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
CREATE FUNCTION identity.mfa_admin_reset(
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
  SET credential_version = account.credential_version + 1,
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

REVOKE ALL ON FUNCTION identity.mfa_begin_enrollment(uuid, uuid, uuid, uuid, text, text, text, integer, integer, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.mfa_enrollment_view(uuid, uuid, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.mfa_enrollment_secret(uuid, uuid, uuid, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.mfa_confirm_enrollment(uuid, uuid, uuid, uuid, integer, integer, integer, bigint, bytea[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.mfa_record_totp_step(uuid, uuid, uuid, bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.mfa_state(uuid, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.step_up_challenge_create(uuid, uuid, uuid, uuid, text, jsonb, bytea, integer, integer, integer, timestamptz, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.step_up_challenge_get(uuid, uuid, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.step_up_challenge_add_method(uuid, uuid, uuid, uuid, integer, text, integer, integer, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.step_up_challenge_fail(uuid, uuid, uuid, uuid, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.step_up_session_mint(uuid, uuid, uuid, uuid, uuid, integer, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.step_up_session_view(uuid, uuid, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.step_up_recheck(uuid, uuid, uuid, uuid, text, bytea, integer, integer, integer, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.recovery_code_consume(uuid, uuid, uuid, uuid, bytea, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.mfa_admin_reset(uuid, uuid, uuid, text, integer, integer) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION identity.mfa_begin_enrollment(uuid, uuid, uuid, uuid, text, text, text, integer, integer, timestamptz) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.mfa_enrollment_view(uuid, uuid, uuid) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.mfa_enrollment_secret(uuid, uuid, uuid, text) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.mfa_confirm_enrollment(uuid, uuid, uuid, uuid, integer, integer, integer, bigint, bytea[]) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.mfa_record_totp_step(uuid, uuid, uuid, bigint) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.mfa_state(uuid, uuid) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.step_up_challenge_create(uuid, uuid, uuid, uuid, text, jsonb, bytea, integer, integer, integer, timestamptz, timestamptz) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.step_up_challenge_get(uuid, uuid, uuid) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.step_up_challenge_add_method(uuid, uuid, uuid, uuid, integer, text, integer, integer, timestamptz) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.step_up_challenge_fail(uuid, uuid, uuid, uuid, integer) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.step_up_session_mint(uuid, uuid, uuid, uuid, uuid, integer, integer) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.step_up_session_view(uuid, uuid, uuid) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.step_up_recheck(uuid, uuid, uuid, uuid, text, bytea, integer, integer, integer, integer) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.recovery_code_consume(uuid, uuid, uuid, uuid, bytea, integer) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.mfa_admin_reset(uuid, uuid, uuid, text, integer, integer) TO api_runtime_role;

RESET ROLE;

-- +goose Down
-- Forward-only: reverting would drop the only second-factor and step-up state
-- and silently downgrade every LoA2 guarantee added here.
