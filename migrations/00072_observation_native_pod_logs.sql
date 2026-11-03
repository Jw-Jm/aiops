-- +goose Up
SET ROLE schema_owner;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION platform.observation_lock_source(tid uuid,sid uuid,rev bigint,cred bigint,cuid text,ns text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
DECLARE s platform.source_registrations%ROWTYPE;c platform.cluster_registrations%ROWTYPE;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false; END IF;
 SELECT * INTO s FROM platform.source_registrations WHERE tenant_id=tid AND source_id=sid FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT * INTO c FROM platform.cluster_registrations WHERE tenant_id=tid AND cluster_id=s.cluster_id FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 RETURN s.status='active' AND c.status='active'
 AND (s.source_type='victorialogs' OR (s.source_type='kubernetes' AND COALESCE(s.data_scope_mapping->>'nativeTenant','')=''))
 AND s.revision=rev AND s.credential_revision=cred AND c.cluster_uid=cuid
 AND s.data_scope_mapping->'scopes'->'cluster' ? cuid
 AND s.data_scope_mapping->'scopes'->'namespace' ? ns;
END $$;
-- +goose StatementEnd
RESET ROLE;
-- No Down: native-source query records must retain their source authorization contract.
