-- +goose Up
SET ROLE schema_owner;
-- A narrow owner operation permits read-role consumers to hold revoke/rotate
-- row locks, without granting UPDATE on tenant or registration business rows.
-- +goose StatementBegin
CREATE FUNCTION platform.sp05_lock_source_binding(tid uuid,sid uuid,rev bigint,cred bigint,cid uuid,cuid text,ns text,sv text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
DECLARE s platform.source_registrations%ROWTYPE;c platform.cluster_registrations%ROWTYPE;active_tenant text;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false; END IF;
 SELECT status INTO active_tenant FROM platform.tenants WHERE tenant_id=tid FOR SHARE;
 SELECT * INTO s FROM platform.source_registrations WHERE tenant_id=tid AND source_id=sid FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT * INTO c FROM platform.cluster_registrations WHERE tenant_id=tid AND cluster_id=s.cluster_id FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 RETURN active_tenant='active' AND s.status='active' AND c.status='active'
 AND s.revision=rev AND s.credential_revision=cred AND s.cluster_id=cid AND c.cluster_uid=cuid
 AND sv=ANY(s.allowed_schemas) AND s.data_scope_mapping->'scopes'->'cluster' ? cuid
 AND (ns='' OR s.data_scope_mapping->'scopes'->'namespace' ? ns);
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.sp05_lock_source_binding(uuid,uuid,bigint,bigint,uuid,text,text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.sp05_lock_source_binding(uuid,uuid,bigint,bigint,uuid,text,text,text) TO api_runtime_role,worker_runtime_role;
RESET ROLE;
