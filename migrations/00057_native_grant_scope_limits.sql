-- +goose Up
-- Match the existing versioned scope contract; preserve native transaction and role checks.
SET ROLE schema_owner;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.admin_account_write(
 p_tenant uuid,p_actor uuid,p_session uuid,p_cv integer,p_av integer,p_sv integer,p_target uuid,p_operation text,p_body jsonb
) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,identity,platform AS $$
DECLARE
 target identity.accounts%ROWTYPE;
 grant_row identity.role_grants%ROWTYPE;
 is_admin boolean;
 admin_count integer;
 next_av integer;
 changed_sessions integer:=0;
 role_name text;
 grant_scope jsonb;
 binding uuid;
 result jsonb;
BEGIN
 IF NOT identity.admin_lock_accounts(p_tenant,p_actor,p_session,p_cv,p_av,p_sv,p_target) THEN
  RAISE EXCEPTION 'native administrator required' USING ERRCODE='42501';
 END IF;
 IF p_target IS NULL OR p_body IS NULL OR jsonb_typeof(p_body)<>'object' THEN
  RAISE EXCEPTION 'invalid account write' USING ERRCODE='22023';
 END IF;
 IF p_operation='create' THEN
  IF p_body - ARRAY['loginName','displayName','passwordHash'] <> '{}'::jsonb THEN
   RAISE EXCEPTION 'unexpected create fields' USING ERRCODE='22023';
  END IF;
  INSERT INTO identity.accounts(tenant_id,user_id,login_name,display_name,subject,status,password_state,must_change_password,account_revision,credential_version,authorization_version,session_version)
  VALUES(p_tenant,p_target,p_body->>'loginName',p_body->>'displayName','native:'||p_target::text,'active','change_required',true,1,1,1,1);
  INSERT INTO identity.credentials(tenant_id,user_id,credential_version,password_hash,status) VALUES(p_tenant,p_target,1,p_body->>'passwordHash','active');
  INSERT INTO identity.password_attempts(tenant_id,user_id,failed_count,window_started_at) VALUES(p_tenant,p_target,0,clock_timestamp());
  RETURN identity.admin_account_view(p_tenant,p_target);
 END IF;
 SELECT * INTO target FROM identity.accounts WHERE tenant_id=p_tenant AND user_id=p_target FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'account unavailable' USING ERRCODE='P0002'; END IF;
 SELECT EXISTS(SELECT 1 FROM identity.role_grants WHERE tenant_id=p_tenant AND user_id=p_target AND role='platform_admin' AND status='active' AND authorization_version=target.authorization_version) INTO is_admin;
 SELECT count(*) INTO admin_count FROM identity.accounts a JOIN identity.role_grants g USING(tenant_id,user_id)
 WHERE a.tenant_id=p_tenant AND a.status='active' AND g.role='platform_admin' AND g.status='active' AND g.authorization_version=a.authorization_version;
 IF p_operation IN('update','password_reset') AND (p_body->>'expectedRevision')::integer IS DISTINCT FROM target.account_revision THEN
  RAISE EXCEPTION 'account revision stale' USING ERRCODE='40001';
 END IF;
 IF p_operation='update' THEN
  IF p_body - ARRAY['displayName','status','expectedRevision'] <> '{}'::jsonb OR NOT(p_body ? 'displayName' OR p_body ? 'status')
   OR (p_body ? 'status' AND p_body->>'status' NOT IN('active','disabled')) THEN
   RAISE EXCEPTION 'invalid account update' USING ERRCODE='22023';
  END IF;
  IF p_body->>'status'='disabled' AND target.status='active' AND is_admin AND admin_count<=1 THEN
   RETURN jsonb_build_object('code','last_active_administrator','activeAdministrators',admin_count,'protectedUserId',p_target,'attemptedStatus','disabled','effectiveStatus','active','detectedAt',clock_timestamp());
  END IF;
  UPDATE identity.accounts SET display_name=COALESCE(p_body->>'displayName',display_name),status=COALESCE(p_body->>'status',status),
   account_revision=account_revision+1,session_version=session_version+CASE WHEN p_body ? 'status' AND p_body->>'status'<>target.status THEN 1 ELSE 0 END,updated_at=clock_timestamp()
   WHERE tenant_id=p_tenant AND user_id=p_target;
  IF p_body ? 'status' AND p_body->>'status'<>target.status THEN
   UPDATE identity.sessions SET revoked_at=clock_timestamp(),revoked_reason='administrator account status change' WHERE tenant_id=p_tenant AND user_id=p_target AND revoked_at IS NULL;
  END IF;
 ELSIF p_operation='password_reset' THEN
  IF p_body - ARRAY['expectedRevision','expectedCredentialVersion','passwordHash'] <> '{}'::jsonb THEN RAISE EXCEPTION 'invalid password reset' USING ERRCODE='22023'; END IF;
  IF (p_body->>'expectedCredentialVersion')::integer IS DISTINCT FROM target.credential_version THEN RAISE EXCEPTION 'credential version stale' USING ERRCODE='40001'; END IF;
  UPDATE identity.credentials SET status='retired' WHERE tenant_id=p_tenant AND user_id=p_target AND status='active';
  INSERT INTO identity.credentials(tenant_id,user_id,credential_version,password_hash,status) VALUES(p_tenant,p_target,target.credential_version+1,p_body->>'passwordHash','active');
  UPDATE identity.accounts SET credential_version=credential_version+1,session_version=session_version+1,account_revision=account_revision+1,password_state='change_required',must_change_password=true,updated_at=clock_timestamp() WHERE tenant_id=p_tenant AND user_id=p_target;
  UPDATE identity.password_attempts SET failed_count=0,window_started_at=clock_timestamp(),locked_until=NULL,updated_at=clock_timestamp() WHERE tenant_id=p_tenant AND user_id=p_target;
  UPDATE identity.sessions SET revoked_at=clock_timestamp(),revoked_reason='administrator password reset' WHERE tenant_id=p_tenant AND user_id=p_target AND revoked_at IS NULL;
  GET DIAGNOSTICS changed_sessions=ROW_COUNT;
 ELSIF p_operation='sessions_revoke' THEN
  IF p_body - ARRAY['expectedSessionVersion','reason'] <> '{}'::jsonb OR length(p_body->>'reason') NOT BETWEEN 1 AND 512 THEN RAISE EXCEPTION 'invalid revocation' USING ERRCODE='22023'; END IF;
  IF (p_body->>'expectedSessionVersion')::integer IS DISTINCT FROM target.session_version THEN RAISE EXCEPTION 'session version stale' USING ERRCODE='40001'; END IF;
  UPDATE identity.accounts SET session_version=session_version+1,account_revision=account_revision+1,updated_at=clock_timestamp() WHERE tenant_id=p_tenant AND user_id=p_target;
  UPDATE identity.sessions SET revoked_at=clock_timestamp(),revoked_reason=p_body->>'reason' WHERE tenant_id=p_tenant AND user_id=p_target AND revoked_at IS NULL;
  GET DIAGNOSTICS changed_sessions=ROW_COUNT;
 ELSIF p_operation IN('grant','revoke_grant') THEN
  IF p_body - ARRAY['role','scope','bindingId','expectedAuthorizationVersion','reason'] <> '{}'::jsonb OR length(p_body->>'reason') NOT BETWEEN 5 AND 512 THEN RAISE EXCEPTION 'invalid grant' USING ERRCODE='22023'; END IF;
  IF (p_body->>'expectedAuthorizationVersion')::integer IS DISTINCT FROM target.authorization_version THEN RAISE EXCEPTION 'authorization version stale' USING ERRCODE='40001'; END IF;
  role_name:=p_body->>'role';
  IF p_operation='revoke_grant' THEN
   SELECT * INTO grant_row FROM identity.role_grants WHERE tenant_id=p_tenant AND user_id=p_target AND binding_id=(p_body->>'bindingId')::uuid AND status='active' FOR UPDATE;
   IF NOT FOUND THEN RAISE EXCEPTION 'grant unavailable' USING ERRCODE='P0002'; END IF;
   role_name:=grant_row.role;
   IF role_name='platform_admin' AND grant_row.authorization_version=target.authorization_version AND target.status='active' AND admin_count<=1 THEN
    RETURN jsonb_build_object('code','last_active_administrator','activeAdministrators',admin_count,'protectedUserId',p_target,'attemptedStatus','disabled','effectiveStatus','active','detectedAt',clock_timestamp());
   END IF;
  ELSE
   grant_scope:=p_body->'scope';
   IF role_name NOT IN('operator','platform_admin') OR grant_scope IS NULL OR jsonb_typeof(grant_scope)<>'object'
    OR grant_scope - ARRAY['clusterUids','namespaces'] <> '{}'::jsonb
    OR jsonb_typeof(COALESCE(grant_scope->'clusterUids','[]'))<>'array' OR jsonb_typeof(COALESCE(grant_scope->'namespaces','[]'))<>'array'
    OR jsonb_array_length(COALESCE(grant_scope->'clusterUids','[]'))>50 OR jsonb_array_length(COALESCE(grant_scope->'namespaces','[]'))>200 THEN
    RAISE EXCEPTION 'invalid native grant scope' USING ERRCODE='22023';
   END IF;
   IF role_name='platform_admin' AND (jsonb_array_length(COALESCE(grant_scope->'clusterUids','[]'))<>0 OR jsonb_array_length(COALESCE(grant_scope->'namespaces','[]'))<>0) THEN RAISE EXCEPTION 'admin grant has no operator scope' USING ERRCODE='22023'; END IF;
   IF jsonb_array_length(COALESCE(grant_scope->'namespaces','[]'))>0 AND jsonb_array_length(COALESCE(grant_scope->'clusterUids','[]'))<>1 THEN RAISE EXCEPTION 'namespace scope requires one cluster' USING ERRCODE='22023'; END IF;
   IF EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(grant_scope->'clusterUids','[]')) uid WHERE NOT EXISTS(SELECT 1 FROM platform.cluster_registrations c WHERE c.tenant_id=p_tenant AND c.cluster_uid=uid.value AND c.status='active')) THEN RAISE EXCEPTION 'cluster not registered' USING ERRCODE='22023'; END IF;
   IF EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(grant_scope->'namespaces','[]')) ns WHERE ns.value !~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$' OR length(ns.value)>63) THEN RAISE EXCEPTION 'invalid namespace' USING ERRCODE='22023'; END IF;
  END IF;
  next_av:=target.authorization_version+1;
  UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason=p_body->>'reason' WHERE tenant_id=p_tenant AND user_id=p_target AND role=role_name AND status='active';
  -- Keep independently valid grants; stale active rows must never regain authority.
  UPDATE identity.role_grants SET authorization_version=next_av WHERE tenant_id=p_tenant AND user_id=p_target AND status='active' AND authorization_version=target.authorization_version;
  IF p_operation='grant' THEN
   binding:=gen_random_uuid();
   INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES(p_tenant,p_target,binding,role_name,grant_scope,next_av,'active');
  END IF;
  UPDATE identity.accounts SET authorization_version=next_av,session_version=session_version+1,account_revision=account_revision+1,updated_at=clock_timestamp() WHERE tenant_id=p_tenant AND user_id=p_target;
  UPDATE identity.sessions SET revoked_at=clock_timestamp(),revoked_reason='administrator authorization change' WHERE tenant_id=p_tenant AND user_id=p_target AND revoked_at IS NULL;
 ELSE
  RAISE EXCEPTION 'unknown native user operation' USING ERRCODE='22023';
 END IF;
 result:=identity.admin_account_view(p_tenant,p_target);
 IF p_operation='password_reset' THEN
  RETURN jsonb_build_object('userId',p_target,'passwordState','change_required','credentialVersion',target.credential_version+1,'sessionsRevoked',changed_sessions,'revocation',result->'revocation');
 ELSIF p_operation='sessions_revoke' THEN RETURN result->'revocation';
 ELSIF p_operation='grant' THEN
  RETURN (SELECT value FROM jsonb_array_elements(result->'roleGrants') WHERE value->>'bindingId'=binding::text);
 END IF;
 RETURN result;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.admin_account_write(uuid,uuid,uuid,integer,integer,integer,uuid,text,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.admin_account_write(uuid,uuid,uuid,integer,integer,integer,uuid,text,jsonb) TO api_runtime_role;
RESET ROLE;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'native authorization is forward-only; preserve granted scopes and revocations'; END $$;
-- +goose StatementEnd
