-- +goose Up
-- P2-04 historical ownership read (source only until the task database is rolled forward).
--
-- A native account may read business history that is still owned by a pre-native
-- subject only when a reviewed inventory produced an explicit mapping. The
-- mapping is evidence for "who may read", never a role grant: this function
-- exposes the mapping triple and nothing else, and every caller must still
-- re-prove the account's current native role, cluster and namespace authority
-- inside the same transaction. Historical rows keep their frozen subject, id,
-- grant snapshot and digest; no read path rewrites them.
SET ROLE schema_owner;

-- +goose StatementBegin
CREATE FUNCTION identity.read_mapped_legacy_subjects(p_tenant uuid, p_user uuid)
RETURNS TABLE (mapping_id uuid, legacy_subject text, evidence_digest bytea)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity, platform
AS $$
BEGIN
 IF p_tenant IS NULL OR p_user IS NULL
 OR p_tenant IS DISTINCT FROM NULLIF(current_setting('app.tenant_id', true), '')::uuid THEN
  RAISE EXCEPTION 'historical ownership read requires the tenant transaction' USING ERRCODE = '42501';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM platform.tenants t WHERE t.tenant_id = p_tenant AND t.status = 'active') THEN
  RAISE EXCEPTION 'tenant unavailable' USING ERRCODE = '42501';
 END IF;
 RETURN QUERY
  SELECT m.mapping_id, m.legacy_subject, m.evidence_digest
  FROM identity.subject_mappings m
  JOIN identity.subject_mapping_inventories i
    ON i.tenant_id = m.tenant_id AND i.evidence_digest = m.inventory_digest
  JOIN identity.accounts a
    ON a.tenant_id = m.tenant_id AND a.user_id = m.user_id
  WHERE m.tenant_id = p_tenant
    AND m.user_id = p_user
    AND m.basis = 'verified_inventory'
    AND m.inventory_digest IS NOT NULL
    AND a.status = 'active'
  ORDER BY m.mapping_id;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.read_mapped_legacy_subjects(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.read_mapped_legacy_subjects(uuid, uuid) TO api_runtime_role;
RESET ROLE;

-- +goose Down
SET ROLE schema_owner;
DROP FUNCTION IF EXISTS identity.read_mapped_legacy_subjects(uuid, uuid);
RESET ROLE;
