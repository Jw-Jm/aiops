-- +goose Up
-- MR-017: the API and the Worker are separate processes. An internal signed
-- context may carry a native reference, but the Worker must re-prove it from
-- persisted state instead of trusting a session cookie, a subject prefix or a
-- signed scope. This narrow function is the only new read the Worker needs for
-- the interactive source: it resolves a live native session to its account and
-- current authorization version, under the same account -> session share-lock
-- order the administrative revocation uses.
SET ROLE schema_owner;

-- +goose StatementBegin
CREATE FUNCTION identity.native_internal_session_shared(
  p_tenant_id uuid,
  p_session_id uuid
) RETURNS TABLE (
  out_user_id uuid,
  out_subject text,
  out_authorization_version integer
)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  target_user uuid;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'job_authority', true);
  IF p_tenant_id IS NULL OR p_session_id IS NULL THEN
    RETURN;
  END IF;
  -- The session row is read first only to learn which account to lock; the row
  -- itself is locked after the account, so a concurrent revocation (account
  -- FOR UPDATE, then sessions) cannot deadlock against this path.
  SELECT session.user_id INTO target_user
  FROM identity.sessions AS session
  WHERE session.tenant_id = p_tenant_id AND session.session_id = p_session_id;
  IF target_user IS NULL THEN
    RETURN;
  END IF;
  PERFORM 1 FROM identity.accounts AS account
  WHERE account.tenant_id = p_tenant_id AND account.user_id = target_user
  FOR SHARE;
  PERFORM 1 FROM identity.sessions AS session
  WHERE session.tenant_id = p_tenant_id AND session.session_id = p_session_id
  FOR SHARE;
  RETURN QUERY
  SELECT account.user_id, account.subject, account.authorization_version
  FROM identity.accounts AS account
  JOIN identity.sessions AS session
    ON session.tenant_id = account.tenant_id AND session.user_id = account.user_id
  WHERE account.tenant_id = p_tenant_id
    AND account.user_id = target_user
    AND account.status = 'active'
    AND session.session_id = p_session_id
    AND session.mode = 'full'
    AND session.revoked_at IS NULL
    AND clock_timestamp() < session.idle_expires_at
    AND clock_timestamp() < session.absolute_expires_at
    AND session.issued_credential_version = account.credential_version
    AND session.issued_session_version = account.session_version;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.native_internal_session_shared(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.native_internal_session_shared(uuid, uuid)
  TO api_runtime_role, worker_runtime_role, migration_role;
-- EXECUTE alone is not enough: the Worker also needs to resolve the identity
-- schema name. This is schema USAGE only; no identity table right is granted to
-- worker_runtime_role (the function is SECURITY DEFINER and returns a projection).
GRANT USAGE ON SCHEMA identity TO worker_runtime_role;
RESET ROLE;
