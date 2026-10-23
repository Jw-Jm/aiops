-- +goose Up
-- Resolve the proof of the exact challenge rather than the most recent proof.
SET ROLE schema_owner;
-- Keep account before tenant/session locks, including calls from admin writes.
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
  SELECT authorization_version INTO account_av FROM identity.accounts
  WHERE tenant_id = p_tenant_id AND user_id = p_user_id
    AND status = 'active' AND password_state = 'active' AND NOT must_change_password
    AND credential_version = p_credential_version AND session_version = p_session_version
  FOR UPDATE;
  IF NOT FOUND THEN RETURN false; END IF;
  PERFORM 1 FROM platform.tenants WHERE tenant_id = p_tenant_id AND status = 'active' FOR SHARE;
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
CREATE FUNCTION identity.step_up_proof_for_challenge(
 p_tenant_id uuid,p_user_id uuid,p_session_id uuid,p_challenge_id uuid
) RETURNS TABLE(out_step_up_ref uuid,out_operation text,out_binding_digest bytea,
 out_verified_methods text[],out_idle_expires_at timestamptz,out_absolute_expires_at timestamptz)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,identity AS $$
BEGIN
 PERFORM set_config('app.identity_preauth','disabled',true);
 PERFORM set_config('app.identity_preauth','stepup_recheck',true);
 RETURN QUERY SELECT step_up_ref,operation,binding_digest,verified_methods,idle_expires_at,absolute_expires_at
 FROM identity.step_up_sessions WHERE tenant_id=p_tenant_id AND user_id=p_user_id
  AND session_id=p_session_id AND challenge_id=p_challenge_id AND revoked_at IS NULL
  AND clock_timestamp()<idle_expires_at AND clock_timestamp()<absolute_expires_at;
 PERFORM set_config('app.identity_preauth','disabled',true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.step_up_proof_for_challenge(uuid,uuid,uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.step_up_proof_for_challenge(uuid,uuid,uuid,uuid) TO api_runtime_role;

-- Administrator reset locks both accounts in UUID order before any grant lock
-- or lock upgrade; concurrent resets cannot each pin the other's target first.
-- +goose StatementBegin
CREATE FUNCTION identity.mfa_lock_reset_accounts(p_tenant_id uuid,p_actor_id uuid,p_target_id uuid)
RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,identity AS $$
BEGIN
 PERFORM set_config('app.identity_preauth','disabled',true);
 PERFORM set_config('app.identity_preauth','mfa_reset',true);
 PERFORM 1 FROM identity.accounts WHERE tenant_id=p_tenant_id AND user_id IN (p_actor_id,p_target_id)
 ORDER BY user_id FOR UPDATE;
 PERFORM set_config('app.identity_preauth','disabled',true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.mfa_lock_reset_accounts(uuid,uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.mfa_lock_reset_accounts(uuid,uuid,uuid) TO api_runtime_role;
RESET ROLE;
-- +goose Down
-- Forward only; keep challenge and recovery state.
