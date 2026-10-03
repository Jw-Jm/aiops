-- +goose Up
SET ROLE schema_owner;
-- +goose StatementBegin
CREATE FUNCTION platform.sp05_lock_recipe(tid uuid,vid uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN false;END IF;
 PERFORM 1 FROM platform.registry_versions WHERE tenant_id=tid AND version_id=vid AND kind='recipe' AND retired_at IS NULL FOR SHARE;
 RETURN FOUND;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.sp05_lock_recipe(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.sp05_lock_recipe(uuid,uuid) TO worker_runtime_role;
RESET ROLE;
