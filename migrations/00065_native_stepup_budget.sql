-- +goose Up
-- Forward-only repair (MR-020): account and factor scoped step-up failure budget.
--
-- The frozen 00051/00052 lifecycle records failures on the challenge row only.
-- Replacing a challenge therefore restarts attempts at zero, so an attacker who
-- already holds a valid LoA1 native session could keep guessing six-digit TOTP
-- codes (or the account password) by discarding challenges. The existing login
-- lock and the recovery-code lock do not cover these two step-up factors.
--
-- This migration adds one narrow, FORCE-RLS table keyed by account and factor and
-- reuses the already accepted account-lock semantics: at most five failures
-- inside fifteen minutes lock that factor for fifteen minutes. The counter is a
-- PostgreSQL row, shared by every API instance and session.
--
-- Concurrency contract (this is the part the first attempt got wrong):
--   * an attempt is only counted after the caller holds the target account row
--     lock, so all verifications for one account are serialized across every API
--     instance, session and challenge;
--   * quota, current account/session versions and the challenge CAS are checked
--     inside that same locked transaction BEFORE the credential is compared, so
--     a request without quota or with a stale revision never reaches the
--     comparison;
--   * the caller performs the comparison and records success/failure inside that
--     same transaction, so an executed failure cannot be rolled back by a later
--     CAS or challenge replacement;
--   * lock order is account -> session proof -> challenge -> budget/enrolment in
--     every function below, matching the existing password rotation and
--     administrator reset order.
--
-- Fixed policy (not client selectable):
--   * factors: password, totp;
--   * window: 15 minutes, limit: 5 failures, lock: 15 minutes;
--   * a lock clears only when its window expires or when a fully verified
--     password+TOTP step-up is minted. The audited administrator password reset
--     does not clear it: that flow rotates the password and the login-attempt
--     lock, not this factor budget, so a reset account may still have to wait out
--     the cooldown before its first step-up.
-- Recovery codes keep their independent account lock (identity.accounts
-- recovery_failures) and are not counted here.
--
-- The TOTP factor is only in scope while the account has a confirmed factor.
-- A pending re-enrolment (including the post-reset flow) proves the brand-new
-- secret its holder just created, so counting it would only let a reset account
-- lock itself out of the re-enrolment gate.
SET ROLE schema_owner;

CREATE TABLE identity.step_up_failures (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  user_id uuid NOT NULL,
  factor text NOT NULL CHECK (factor IN ('password', 'totp')),
  failed_count integer NOT NULL CHECK (failed_count BETWEEN 0 AND 1000000),
  window_started_at timestamptz NOT NULL,
  locked_until timestamptz,
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, user_id, factor),
  CHECK (locked_until IS NULL OR locked_until > window_started_at),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);

ALTER TABLE identity.step_up_failures ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.step_up_failures FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.step_up_failures
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.mfa_scope_active()
  );
-- The controlled migration/verification duty may inspect the counters (evidence
-- and the recovery runbook). API/Worker still have no direct table grant.
GRANT SELECT ON identity.step_up_failures TO migration_role;

-- +goose StatementBegin
CREATE FUNCTION identity.step_up_budget_factor_in_scope(
  p_tenant_id uuid,
  p_user_id uuid,
  p_factor text
) RETURNS boolean
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  confirmed boolean := false;
BEGIN
  IF p_factor IS NULL THEN
    RETURN false;
  END IF;
  IF p_factor = 'password' THEN
    RETURN true;
  END IF;
  IF p_factor <> 'totp' THEN
    RETURN false;
  END IF;
  SELECT EXISTS (
    SELECT 1 FROM identity.mfa_enrollments AS enrollment
    WHERE enrollment.tenant_id = p_tenant_id AND enrollment.user_id = p_user_id
      AND enrollment.state = 'confirmed'
  ) INTO confirmed;
  RETURN confirmed;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.step_up_budget_factor_in_scope(uuid, uuid, text) FROM PUBLIC;

-- Internal read of one factor. No runtime duty calls it directly: the quota is
-- enforced by identity.step_up_attempt_begin under the account lock.
-- +goose StatementBegin
CREATE FUNCTION identity.step_up_budget_factor_state(
  p_tenant_id uuid,
  p_user_id uuid,
  p_factor text
) RETURNS TABLE (out_allowed boolean, out_retry_at timestamptz, out_remaining integer)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  stored_count integer;
  stored_window timestamptz;
  stored_lock timestamptz;
  stored_found boolean := false;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF p_factor IS NULL OR p_factor NOT IN ('password', 'totp') THEN
    RAISE EXCEPTION 'invalid step-up budget factor' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.step_up_budget_factor_in_scope(p_tenant_id, p_user_id, p_factor) THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN QUERY SELECT true, NULL::timestamptz, 5;
    RETURN;
  END IF;
  SELECT failures.failed_count, failures.window_started_at, failures.locked_until
  INTO stored_count, stored_window, stored_lock
  FROM identity.step_up_failures AS failures
  WHERE failures.tenant_id = p_tenant_id AND failures.user_id = p_user_id
    AND failures.factor = p_factor;
  stored_found := FOUND;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  IF NOT stored_found THEN
    RETURN QUERY SELECT true, NULL::timestamptz, 5;
    RETURN;
  END IF;
  IF stored_lock IS NOT NULL AND stored_lock > clock_timestamp() THEN
    RETURN QUERY SELECT false, stored_lock, 0;
    RETURN;
  END IF;
  IF stored_window <= clock_timestamp() - interval '15 minutes' THEN
    RETURN QUERY SELECT true, NULL::timestamptz, 5;
    RETURN;
  END IF;
  -- Fail closed: five failures inside the window mean the factor is locked even
  -- if a hand-written row carries a cooldown shorter than the window.
  IF stored_count >= 5 THEN
    RETURN QUERY SELECT false, stored_window + interval '15 minutes', 0;
    RETURN;
  END IF;
  RETURN QUERY SELECT true, NULL::timestamptz, GREATEST(5 - stored_count, 0);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.step_up_budget_factor_state(uuid, uuid, text) FROM PUBLIC;

-- Read-only projection used for remainingAttempts. The tightest factor decides.
-- This is the only budget function granted to the runtime duty.
-- +goose StatementBegin
CREATE FUNCTION identity.step_up_budget_check(
  p_tenant_id uuid,
  p_user_id uuid
) RETURNS TABLE (out_allowed boolean, out_retry_at timestamptz, out_remaining integer)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  factor text;
  factor_allowed boolean;
  factor_retry timestamptz;
  factor_remaining integer;
  overall_allowed boolean := true;
  overall_retry timestamptz := NULL;
  overall_remaining integer := 5;
BEGIN
  FOREACH factor IN ARRAY ARRAY['password', 'totp']::text[] LOOP
    SELECT factor_budget.out_allowed, factor_budget.out_retry_at, factor_budget.out_remaining
    INTO factor_allowed, factor_retry, factor_remaining
    FROM identity.step_up_budget_factor_state(p_tenant_id, p_user_id, factor) AS factor_budget;
    IF NOT factor_allowed THEN
      overall_allowed := false;
      overall_retry := GREATEST(overall_retry, factor_retry);
    END IF;
    overall_remaining := LEAST(overall_remaining, COALESCE(factor_remaining, 0));
  END LOOP;
  RETURN QUERY SELECT overall_allowed, overall_retry, overall_remaining;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.step_up_budget_check(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.step_up_budget_check(uuid, uuid) TO api_runtime_role;

-- Internal write. Only identity.step_up_attempt_failed calls it, inside the
-- transaction that already holds the account row lock, so no runtime EXECUTE is
-- granted: there is no way to add a failure without a reserved attempt.
-- +goose StatementBegin
CREATE FUNCTION identity.step_up_budget_fail(
  p_tenant_id uuid,
  p_user_id uuid,
  p_factor text
) RETURNS TABLE (out_remaining integer, out_retry_at timestamptz)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  next_count integer;
  next_window timestamptz;
  next_lock timestamptz;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF p_factor IS NULL OR p_factor NOT IN ('password', 'totp') THEN
    RAISE EXCEPTION 'invalid step-up budget factor' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.step_up_budget_factor_in_scope(p_tenant_id, p_user_id, p_factor) THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN QUERY SELECT 5, NULL::timestamptz;
    RETURN;
  END IF;
  INSERT INTO identity.step_up_failures AS failures
    (tenant_id, user_id, factor, failed_count, window_started_at, locked_until, updated_at)
  VALUES
    (p_tenant_id, p_user_id, p_factor, 1, clock_timestamp(), NULL, clock_timestamp())
  ON CONFLICT (tenant_id, user_id, factor) DO UPDATE
    SET failed_count = CASE
          WHEN failures.window_started_at <= clock_timestamp() - interval '15 minutes'
            THEN 1
          ELSE failures.failed_count + 1
        END,
        window_started_at = CASE
          WHEN failures.window_started_at <= clock_timestamp() - interval '15 minutes'
            THEN clock_timestamp()
          ELSE failures.window_started_at
        END,
        locked_until = CASE
          WHEN (CASE
                  WHEN failures.window_started_at <= clock_timestamp() - interval '15 minutes'
                    THEN 1
                  ELSE failures.failed_count + 1
                END) >= 5
            THEN GREATEST(failures.locked_until, clock_timestamp() + interval '15 minutes')
          ELSE failures.locked_until
        END,
        updated_at = clock_timestamp()
  RETURNING failures.failed_count, failures.window_started_at, failures.locked_until
  INTO next_count, next_window, next_lock;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  IF next_lock IS NOT NULL AND next_lock > clock_timestamp() THEN
    RETURN QUERY SELECT 0, next_lock;
    RETURN;
  END IF;
  IF next_window <= clock_timestamp() - interval '15 minutes' THEN
    RETURN QUERY SELECT 5, NULL::timestamptz;
    RETURN;
  END IF;
  RETURN QUERY SELECT GREATEST(5 - next_count, 0), NULL::timestamptz;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.step_up_budget_fail(uuid, uuid, text) FROM PUBLIC;

-- Called only from identity.step_up_session_mint (schema_owner) after both
-- factors were proven. No runtime duty needs to clear a budget directly.
-- +goose StatementBegin
CREATE FUNCTION identity.step_up_budget_clear(
  p_tenant_id uuid,
  p_user_id uuid
) RETURNS void
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  DELETE FROM identity.step_up_failures AS failures
  WHERE failures.tenant_id = p_tenant_id AND failures.user_id = p_user_id;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.step_up_budget_clear(uuid, uuid) FROM PUBLIC;

-- The account row is the cross-instance serialization point for every step-up
-- attempt, challenge creation and verified-method write. Holding it also pins the
-- current credential/session/authorization versions for the whole transaction.
-- +goose StatementBegin
CREATE FUNCTION identity.step_up_lock_account(
  p_tenant_id uuid,
  p_user_id uuid
) RETURNS boolean
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  found boolean := false;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  SELECT true INTO found
  FROM identity.accounts AS account
  WHERE account.tenant_id = p_tenant_id AND account.user_id = p_user_id
    AND account.status = 'active'
  FOR UPDATE;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  RETURN COALESCE(found, false);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.step_up_lock_account(uuid, uuid) FROM PUBLIC;

-- First half of one verification: reserve the attempt. The reservation is the
-- account row lock plus the challenge revision check - it deliberately does not
-- advance the externally visible challenge revision, because the existing
-- contract is "one external verification advances the revision exactly once"
-- (by the success or failure writer below). It re-validates the current
-- session/versions, the challenge revision, state and expiry, and only then
-- checks the factor quota. Every failure here happens before the caller compares
-- a credential, and nothing it wrote survives a rollback.
-- +goose StatementBegin
CREATE FUNCTION identity.step_up_attempt_begin(
  p_tenant_id uuid,
  p_user_id uuid,
  p_challenge_id uuid,
  p_session_id uuid,
  p_expected_revision integer,
  p_method text
) RETURNS TABLE (
  out_revision integer,
  out_state text,
  out_remaining integer,
  out_attempt_remaining integer,
  out_retry_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  challenge identity.step_up_challenges%ROWTYPE;
  budget_allowed boolean := true;
  budget_remaining integer := 0;
  budget_retry timestamptz := NULL;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF p_method IS NULL OR p_method NOT IN ('password', 'totp')
     OR p_challenge_id IS NULL OR p_session_id IS NULL
     OR p_expected_revision IS NULL OR p_expected_revision < 1 THEN
    RAISE EXCEPTION 'invalid step-up attempt' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.step_up_lock_account(p_tenant_id, p_user_id) THEN
    RAISE EXCEPTION 'native account is not active' USING ERRCODE = '28000';
  END IF;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  -- Re-read the current session and its issued versions under the account lock.
  IF NOT identity.mfa_lock_session(p_tenant_id, p_user_id, p_session_id) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  SELECT * INTO challenge FROM identity.step_up_challenges AS candidate
  WHERE candidate.tenant_id = p_tenant_id AND candidate.user_id = p_user_id
    AND candidate.challenge_id = p_challenge_id
  FOR UPDATE;
  IF NOT FOUND
     OR challenge.session_id <> p_session_id
     OR challenge.state <> 'pending'
     OR challenge.revision <> p_expected_revision
     OR challenge.attempts >= challenge.attempt_limit
     OR NOT (p_method = ANY (challenge.required_methods))
     OR challenge.completed_methods @> ARRAY[p_method]::text[]
     OR clock_timestamp() >= challenge.idle_expires_at
     OR clock_timestamp() >= challenge.absolute_expires_at THEN
    RAISE EXCEPTION 'step-up challenge is not pending at this revision' USING ERRCODE = '40001';
  END IF;
  SELECT factor_budget.out_allowed, factor_budget.out_remaining, factor_budget.out_retry_at
  INTO budget_allowed, budget_remaining, budget_retry
  FROM identity.step_up_budget_factor_state(p_tenant_id, p_user_id, p_method) AS factor_budget;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF budget_allowed IS DISTINCT FROM true THEN
    RAISE EXCEPTION 'step-up attempt budget is exhausted' USING ERRCODE = '42900';
  END IF;
  out_revision := challenge.revision;
  out_state := challenge.state;
  out_attempt_remaining := GREATEST(challenge.attempt_limit - challenge.attempts, 0);
  out_remaining := LEAST(COALESCE(out_attempt_remaining, 0), COALESCE(budget_remaining, 0));
  out_retry_at := budget_retry;
  RETURN NEXT;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.step_up_attempt_begin(uuid, uuid, uuid, uuid, integer, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.step_up_attempt_begin(uuid, uuid, uuid, uuid, integer, text) TO api_runtime_role;

-- Second half, failure branch: the reserved attempt was really compared and was
-- wrong. The budget increment is committed with it, and this statement is the
-- single place that advances the challenge revision for a failure. There is no
-- CAS to lose against, because the caller still holds the account and challenge
-- locks.
-- +goose StatementBegin
CREATE FUNCTION identity.step_up_attempt_failed(
  p_tenant_id uuid,
  p_user_id uuid,
  p_challenge_id uuid,
  p_session_id uuid,
  p_expected_revision integer,
  p_method text
) RETURNS TABLE (
  out_revision integer,
  out_state text,
  out_remaining integer,
  out_attempt_remaining integer,
  out_retry_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  challenge identity.step_up_challenges%ROWTYPE;
  budget_remaining integer := 0;
  budget_retry timestamptz := NULL;
  attempt_remaining integer := 0;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF p_method IS NULL OR p_method NOT IN ('password', 'totp')
     OR p_expected_revision IS NULL OR p_expected_revision < 1 THEN
    RAISE EXCEPTION 'invalid step-up failure' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.step_up_lock_account(p_tenant_id, p_user_id) THEN
    RAISE EXCEPTION 'native account is not active' USING ERRCODE = '28000';
  END IF;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF NOT identity.mfa_lock_session(p_tenant_id, p_user_id, p_session_id) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  SELECT * INTO challenge FROM identity.step_up_challenges AS candidate
  WHERE candidate.tenant_id = p_tenant_id AND candidate.user_id = p_user_id
    AND candidate.challenge_id = p_challenge_id
  FOR UPDATE;
  IF NOT FOUND
     OR challenge.session_id <> p_session_id
     OR challenge.state <> 'pending'
     OR challenge.revision <> p_expected_revision THEN
    RAISE EXCEPTION 'step-up attempt was not reserved' USING ERRCODE = '40001';
  END IF;
  SELECT budget.out_remaining, budget.out_retry_at
  INTO budget_remaining, budget_retry
  FROM identity.step_up_budget_fail(p_tenant_id, p_user_id, p_method) AS budget;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  UPDATE identity.step_up_challenges AS updated
  SET attempts = updated.attempts + 1,
      revision = updated.revision + 1,
      state = CASE WHEN updated.attempts + 1 >= updated.attempt_limit
                   THEN 'revoked' ELSE updated.state END
  WHERE updated.tenant_id = p_tenant_id AND updated.challenge_id = p_challenge_id
    AND updated.state = 'pending'
    AND updated.revision = p_expected_revision
  RETURNING updated.revision, updated.state,
            GREATEST(updated.attempt_limit - updated.attempts, 0)
  INTO out_revision, out_state, attempt_remaining;
  IF out_revision IS NULL THEN
    RAISE EXCEPTION 'step-up challenge is not pending' USING ERRCODE = '40001';
  END IF;
  out_remaining := LEAST(COALESCE(attempt_remaining, 0), COALESCE(budget_remaining, 0));
  out_attempt_remaining := attempt_remaining;
  out_retry_at := budget_retry;
  RETURN NEXT;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.step_up_attempt_failed(uuid, uuid, uuid, uuid, integer, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.step_up_attempt_failed(uuid, uuid, uuid, uuid, integer, text) TO api_runtime_role;

-- The frozen 5-argument failure recorder stays in migration 00052 verbatim but is
-- no longer executable by a runtime duty: it would count a challenge attempt
-- without the account budget or the account lock.
REVOKE EXECUTE ON FUNCTION identity.step_up_challenge_fail(uuid, uuid, uuid, uuid, integer)
  FROM api_runtime_role, migration_role;

-- Challenge creation checks the account budget under the same account lock, so
-- replacing a challenge cannot buy new attempts and cannot race a verification.
-- Recovery codes keep their own lock and are exempt.
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
DECLARE
  budget_allowed boolean;
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
  IF NOT identity.step_up_lock_account(p_tenant_id, p_user_id) THEN
    RAISE EXCEPTION 'native account is not active' USING ERRCODE = '28000';
  END IF;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_create', true);
  IF NOT identity.mfa_session_proof(p_tenant_id, p_user_id, p_session_id, p_credential_version, p_session_version) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  IF EXISTS (SELECT 1 FROM identity.accounts WHERE tenant_id=p_tenant_id AND user_id=p_user_id AND mfa_reset_required)
     AND p_operation <> 'mfa_enrollment' THEN
    RAISE EXCEPTION 'mfa setup is required' USING ERRCODE='28000';
  END IF;
  IF p_operation <> 'recovery_code_regenerate' THEN
    SELECT budget.out_allowed INTO budget_allowed
    FROM identity.step_up_budget_check(p_tenant_id, p_user_id) AS budget;
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    PERFORM set_config('app.identity_preauth', 'stepup_create', true);
    IF budget_allowed IS DISTINCT FROM true THEN
      RAISE EXCEPTION 'step-up attempt budget is exhausted' USING ERRCODE = '42900';
    END IF;
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

-- The success path re-checks the same quota under the same lock, so a verified
-- method cannot be recorded without a reserved attempt and a live budget.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.step_up_challenge_add_method(
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
  budget_allowed boolean;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF p_method NOT IN ('password', 'totp') THEN
    RAISE EXCEPTION 'invalid step-up method' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.step_up_lock_account(p_tenant_id, p_user_id) THEN
    RAISE EXCEPTION 'native account is not active' USING ERRCODE = '28000';
  END IF;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF NOT identity.mfa_session_proof(p_tenant_id, p_user_id, p_session_id, p_credential_version, p_session_version) THEN
    RAISE EXCEPTION 'native session is not active' USING ERRCODE = '28000';
  END IF;
  SELECT budget.out_allowed INTO budget_allowed
  FROM identity.step_up_budget_factor_state(p_tenant_id, p_user_id, p_method) AS budget;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF budget_allowed IS DISTINCT FROM true THEN
    RAISE EXCEPTION 'step-up attempt budget is exhausted' USING ERRCODE = '42900';
  END IF;
  UPDATE identity.step_up_challenges AS challenge
  SET completed_methods = challenge.completed_methods || p_method,
      -- Success writes do not touch the failure counter; the revision advance
      -- here is the single external step for a successful verification.
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

-- Minting is the only success path that clears the budget: both factors were
-- proven for this exact session, operation, binding and version set.
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
  PERFORM identity.step_up_budget_clear(p_tenant_id, p_user_id);
  RETURN QUERY
  SELECT p_step_up_ref, challenge.completed_methods, clock_timestamp(),
         clock_timestamp() + make_interval(secs => p_idle_seconds),
         clock_timestamp() + make_interval(secs => p_absolute_seconds);
END;
$$;
-- +goose StatementEnd

-- Lock-order repair: the frozen 00051/00052 confirmation writes enrolment rows
-- first and the account last, which inverts the account -> enrolment order used
-- by enrolment start, password rotation, administrator reset and every step-up
-- verification above. With verification now holding the account lock across the
-- TOTP comparison, that inversion is a real deadlock window, so the confirmation
-- takes the same account row lock first. The observable behaviour is unchanged.
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
  -- Same first lock as enrolment start and every step-up attempt.
  PERFORM 1 FROM identity.accounts AS account
  WHERE account.tenant_id = p_tenant_id AND account.user_id = p_user_id
  FOR UPDATE OF account;
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
RESET ROLE;
