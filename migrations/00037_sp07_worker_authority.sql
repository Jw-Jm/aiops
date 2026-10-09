-- +goose Up
SET ROLE schema_owner;
-- Worker must lock a published profile against retirement without gaining
-- permission to publish, retire or change its authority fields.
-- +goose StatementBegin
CREATE FUNCTION action.lock_profile(tid uuid,pid uuid,v integer)
RETURNS TABLE(profile_version_id uuid,content jsonb)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN RETURN; END IF;
 RETURN QUERY SELECT p.profile_version_id,p.content FROM platform.execution_profile_versions p
 WHERE p.tenant_id=tid AND p.content->>'executionProfileId'=pid::text
 AND p.version_number=v AND p.status='published' FOR SHARE OF p;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION action.lock_profile(uuid,uuid,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION action.lock_profile(uuid,uuid,integer) TO api_runtime_role,worker_runtime_role;
-- Dispatch repeats the same identity-bound, revoked/absolute/idle expiry
-- predicates and atomically touches only last_used_at in its execution tx.
GRANT SELECT ON platform.step_up_sessions TO worker_runtime_role;
GRANT UPDATE(last_used_at) ON platform.step_up_sessions TO worker_runtime_role;
RESET ROLE;
