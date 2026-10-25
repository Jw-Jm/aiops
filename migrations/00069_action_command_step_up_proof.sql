-- +goose Up
-- MR-003: a high-privilege native command is acknowledged and prepared under an
-- exact native step-up proof, then dispatched and claimed asynchronously by a
-- Worker that has no browser session. The command therefore freezes the accepted
-- proof reference and binding digest next to the execution, so the asynchronous
-- stages can re-prove the same proof instead of requiring a Keycloak SID/ACR that
-- a native account never has.
--
-- The columns never store a cookie, password, TOTP secret or bearer token: only the
-- proof reference, its operation, the canonical binding digest and the expiry the
-- proof had when the execution was created.
SET ROLE schema_owner;

ALTER TABLE action.executions
  ADD COLUMN step_up_session_id uuid,
  ADD COLUMN step_up_proof_id uuid,
  ADD COLUMN step_up_operation text,
  ADD COLUMN step_up_binding_digest bytea,
  ADD COLUMN step_up_expires_at timestamptz;

-- Only two shapes exist: no proof at all (legacy Keycloak step-up, or a command
-- that needs none), or a complete proof reference. "IS TRUE" is required because a
-- CHECK constraint accepts a NULL result, and octet_length(NULL) is NULL.
ALTER TABLE action.executions
  ADD CONSTRAINT action_executions_step_up_shape CHECK ((
    (step_up_session_id IS NULL AND step_up_proof_id IS NULL AND step_up_operation IS NULL
      AND step_up_binding_digest IS NULL AND step_up_expires_at IS NULL)
    OR (step_up_session_id IS NOT NULL AND step_up_proof_id IS NOT NULL
      AND step_up_operation IS NOT NULL AND step_up_operation <> ''
      AND step_up_binding_digest IS NOT NULL AND octet_length(step_up_binding_digest) = 32
      AND step_up_expires_at IS NOT NULL)
  ) IS TRUE) NOT VALID;
ALTER TABLE action.executions VALIDATE CONSTRAINT action_executions_step_up_shape;

COMMENT ON COLUMN action.executions.step_up_session_id IS
  'Native session whose verified password+TOTP step-up authorized this command.';
COMMENT ON COLUMN action.executions.step_up_proof_id IS
  'identity.step_up_sessions reference of the accepted proof; re-proved at dispatch and claim.';
COMMENT ON COLUMN action.executions.step_up_operation IS
  'Operation the proof was minted for (command_execution); never re-used across operations.';
COMMENT ON COLUMN action.executions.step_up_binding_digest IS
  'Proof binding digest of the exact command target, frozen with the execution.';
COMMENT ON COLUMN action.executions.step_up_expires_at IS
  'Proof expiry observed at prepare time; a later dispatch never extends it.';

-- Re-prove a frozen native command proof without a browser session. The Worker
-- only holds the execution row; this function answers whether the proof that
-- authorized it is still live for the same account, session, operation and exact
-- binding digest, at the same authorization version, with the account and session
-- still at the versions the proof was issued for. It reads no cookie and requires
-- no Keycloak SID/ACR.
-- +goose StatementBegin
CREATE FUNCTION identity.native_command_proof_recheck(
  p_tenant_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_step_up_ref uuid,
  p_operation text,
  p_binding_digest bytea,
  p_authorization_version integer
) RETURNS TABLE(
  out_issued_credential_version integer,
  out_issued_session_version integer,
  out_issued_authorization_version integer
)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity, platform
AS $$
DECLARE
  accepted boolean := false;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_recheck', true);
  IF p_tenant_id IS NULL OR p_user_id IS NULL OR p_session_id IS NULL OR p_step_up_ref IS NULL
     OR p_operation IS NULL OR p_operation = ''
     OR p_binding_digest IS NULL OR octet_length(p_binding_digest) <> 32
     OR p_authorization_version IS NULL OR p_authorization_version < 1 THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN;
  END IF;
  -- Account, session, then proof: the same order the interactive proof and the
  -- administrative writes use, so a concurrent password change, revocation or
  -- disable waits instead of interleaving with this decision.
  PERFORM 1 FROM identity.accounts AS account
    WHERE account.tenant_id = p_tenant_id AND account.user_id = p_user_id
    FOR SHARE;
  PERFORM 1 FROM identity.sessions AS session
    WHERE session.tenant_id = p_tenant_id AND session.user_id = p_user_id
      AND session.session_id = p_session_id
    FOR SHARE;
  PERFORM 1 FROM identity.step_up_sessions AS proof
    WHERE proof.tenant_id = p_tenant_id AND proof.user_id = p_user_id
      AND proof.step_up_ref = p_step_up_ref
    FOR SHARE;
  SELECT EXISTS (
    SELECT 1
    FROM identity.step_up_sessions AS proof
    JOIN identity.sessions AS session
      ON session.tenant_id = proof.tenant_id AND session.user_id = proof.user_id
     AND session.session_id = proof.session_id
    JOIN identity.accounts AS account
      ON account.tenant_id = proof.tenant_id AND account.user_id = proof.user_id
    JOIN platform.tenants AS tenant ON tenant.tenant_id = proof.tenant_id
    WHERE proof.tenant_id = p_tenant_id AND proof.user_id = p_user_id
      AND proof.session_id = p_session_id
      AND proof.step_up_ref = p_step_up_ref
      AND proof.operation = p_operation
      AND proof.binding_digest = p_binding_digest
      AND proof.verified_methods @> ARRAY['password', 'totp']::text[]
      AND proof.revoked_at IS NULL
      AND clock_timestamp() < proof.idle_expires_at
      AND clock_timestamp() < proof.absolute_expires_at
      AND session.mode = 'full'
      AND session.revoked_at IS NULL
      -- The session must still be inside both of its own windows. The proof can
      -- outlive the browser session that produced it, but a session that idled
      -- out or reached its absolute end is not a live authority any more.
      AND clock_timestamp() < session.idle_expires_at
      AND clock_timestamp() < session.absolute_expires_at
      AND session.issued_credential_version = proof.issued_credential_version
      AND session.issued_session_version = proof.issued_session_version
      AND session.issued_authorization_version = proof.issued_authorization_version
      AND account.status = 'active'
      AND account.password_state = 'active'
      AND NOT account.must_change_password
      -- An account whose second factor was reset by an administrator has no
      -- standing step-up, even if an old proof row still exists.
      AND NOT account.mfa_reset_required
      AND account.credential_version = proof.issued_credential_version
      AND account.session_version = proof.issued_session_version
      AND account.authorization_version = proof.issued_authorization_version
      AND account.authorization_version = p_authorization_version
      AND tenant.status = 'active'
  ) INTO accepted;
  IF NOT accepted THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN;
  END IF;
  -- The caller re-derives the binding digest from the frozen command with these
  -- versions, so a proof of another command can never be presented.
  RETURN QUERY
    SELECT proof.issued_credential_version, proof.issued_session_version, proof.issued_authorization_version
    FROM identity.step_up_sessions AS proof
    WHERE proof.tenant_id = p_tenant_id AND proof.user_id = p_user_id
      AND proof.session_id = p_session_id AND proof.step_up_ref = p_step_up_ref;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.native_command_proof_recheck(uuid, uuid, uuid, uuid, text, bytea, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.native_command_proof_recheck(uuid, uuid, uuid, uuid, text, bytea, integer)
  TO api_runtime_role, worker_runtime_role;

RESET ROLE;
