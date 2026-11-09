-- +goose Up
-- The assistant legal hold is an audit-domain write. Migration 42 fenced it
-- with platform.assistant_lock_audit_authority, which only knows the legacy
-- platform.role_bindings table: a native platform administrator passed the
-- request path (live session + grant proven by AuditScopeTx) and was then
-- refused a second time at the database boundary, because the frozen fence
-- never learned about identity.accounts / identity.role_grants.
--
-- The native fence below proves the same authority inside the database:
--   * tenant fencing: the target tenant must be the transaction tenant;
--   * the subject must be the account row's own subject, never a caller
--     supplied prefix;
--   * identity.authorize_native_session_role re-proves the live session:
--     account active, session unrevoked and unexpired, platform_admin grant
--     active at the account's current authorization version; the account,
--     session and grant rows are pinned FOR SHARE, so a concurrent revocation
--     waits for this transaction instead of racing it;
--   * platform.native_authority_shared re-reads the current grant set at that
--     authorization version and returns no row once the grant is gone;
--   * the advisory lock and the turn lock stay exactly as in migration 42, so
--     hold changes and retention cleanup cannot interleave.
--
-- The legacy callers keep the frozen migration 42 function unchanged, and the
-- API runtime role still cannot update a hold row directly.
SET ROLE schema_owner;

-- +goose StatementBegin
CREATE FUNCTION investigation.assistant_set_hold_native(
  p_tenant_id uuid,
  p_turn_id uuid,
  p_user_id uuid,
  p_session_id uuid,
  p_authorization_version integer,
  p_credential_version integer,
  p_session_version integer,
  p_subject text,
  p_hold boolean,
  p_reason text)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, investigation, platform, identity
AS $$
DECLARE
  version integer;
  bindings uuid[];
BEGIN
  IF p_tenant_id IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid
     OR p_turn_id IS NULL OR p_user_id IS NULL OR p_session_id IS NULL
     OR p_authorization_version IS NULL OR p_authorization_version < 1
     OR p_credential_version IS NULL OR p_credential_version < 1
     OR p_session_version IS NULL OR p_session_version < 1
     OR p_subject IS NULL OR p_subject = ''
     OR p_hold IS NULL
     OR p_reason IS NULL OR length(p_reason) NOT BETWEEN 1 AND 1024 THEN
    RETURN false;
  END IF;
  -- Same tenant wide advisory lock as the legacy fence.
  PERFORM pg_advisory_xact_lock(hashtextextended(p_tenant_id::text,180));
  -- The subject is the account's own subject: a caller supplied prefix never
  -- establishes an identity.
  PERFORM 1 FROM identity.accounts AS account
   WHERE account.tenant_id = p_tenant_id
     AND account.user_id = p_user_id
     AND account.subject = p_subject
     AND account.status = 'active'
   FOR SHARE;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  -- Live session authority: account, session and grant pinned FOR SHARE.
  IF NOT identity.authorize_native_session_role(
       p_tenant_id, p_user_id, p_session_id, 'platform_admin',
       p_authorization_version, p_credential_version, p_session_version) THEN
    RETURN false;
  END IF;
  -- Current grant set at the account's authorization version.
  SELECT authority.authorization_version, authority.binding_ids
    INTO version, bindings
    FROM platform.native_authority_shared(p_tenant_id, p_user_id, 'platform_admin') AS authority;
  IF version IS DISTINCT FROM p_authorization_version OR bindings IS NULL OR cardinality(bindings) = 0 THEN
    RETURN false;
  END IF;
  PERFORM 1 FROM investigation.assistant_turns
   WHERE tenant_id = p_tenant_id AND turn_id = p_turn_id FOR UPDATE;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  INSERT INTO investigation.assistant_legal_holds(tenant_id,turn_id,held,reason,subject)
  VALUES(p_tenant_id,p_turn_id,p_hold,p_reason,p_subject)
  ON CONFLICT(tenant_id,turn_id) DO UPDATE SET held=EXCLUDED.held,reason=EXCLUDED.reason,subject=EXCLUDED.subject,updated_at=clock_timestamp();
  UPDATE investigation.assistant_messages SET legal_hold = p_hold WHERE tenant_id = p_tenant_id AND turn_id = p_turn_id;
  UPDATE investigation.assistant_query_results SET legal_hold = p_hold WHERE tenant_id = p_tenant_id AND turn_id = p_turn_id;
  RETURN true;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION investigation.assistant_set_hold_native(uuid,uuid,uuid,uuid,integer,integer,integer,text,boolean,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION investigation.assistant_set_hold_native(uuid,uuid,uuid,uuid,integer,integer,integer,text,boolean,text) TO api_runtime_role;
RESET ROLE;
