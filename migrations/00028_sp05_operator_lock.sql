-- +goose Up
SET ROLE schema_owner;
-- Hold current operator grants while committing an authorized domain mutation.
-- +goose StatementBegin
CREATE FUNCTION platform.sp05_lock_operator_scope(tid uuid,sub text,cuid text,ns text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
DECLARE c platform.cluster_registrations%ROWTYPE;b platform.role_bindings%ROWTYPE;tenant_state text;allowed boolean:=false;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false;END IF;
 SELECT status INTO tenant_state FROM platform.tenants WHERE tenant_id=tid FOR SHARE;
 SELECT * INTO c FROM platform.cluster_registrations WHERE tenant_id=tid AND cluster_uid=cuid FOR SHARE;
 IF NOT FOUND OR c.status<>'active' OR tenant_state<>'active' THEN RETURN false;END IF;
 FOR b IN SELECT * FROM platform.role_bindings WHERE tenant_id=tid AND subject=sub AND role_name='operator' FOR SHARE LOOP
  IF b.status='active' AND ((ns='' AND b.cluster_scopes ? c.cluster_id::text) OR (ns<>'' AND EXISTS(SELECT 1 FROM jsonb_array_elements(b.namespace_scopes) n WHERE n->>'clusterId'=c.cluster_id::text AND n->>'namespace'=ns))) THEN allowed:=true; END IF;
 END LOOP;
 RETURN allowed;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.sp05_lock_operator_scope(uuid,text,text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.sp05_lock_operator_scope(uuid,text,text,text) TO api_runtime_role,worker_runtime_role;
RESET ROLE;
