-- +goose Up
-- Reviewed inventories are imported under the controlled migration duty.
-- An HTTP administrator cannot manufacture the prerequisite for a mapping.
SET ROLE schema_owner;
CREATE TABLE identity.subject_mapping_inventories (
 tenant_id uuid NOT NULL REFERENCES platform.tenants(tenant_id),
 evidence_digest bytea NOT NULL CHECK(octet_length(evidence_digest)=32),
 document jsonb NOT NULL CHECK(jsonb_typeof(document)='object'),
 imported_by text NOT NULL, imported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,evidence_digest)
);
ALTER TABLE identity.subject_mapping_inventories ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.subject_mapping_inventories FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.subject_mapping_inventories
 USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid)
 WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
ALTER TABLE identity.subject_mappings ADD COLUMN inventory_digest bytea;
ALTER TABLE identity.subject_mappings ADD CONSTRAINT subject_mapping_inventory_fk
 FOREIGN KEY(tenant_id,inventory_digest) REFERENCES identity.subject_mapping_inventories(tenant_id,evidence_digest);
REVOKE INSERT ON identity.subject_mappings FROM migration_role;

-- +goose StatementBegin
CREATE FUNCTION identity.import_subject_inventory(p_tenant uuid,p_canonical text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,identity,platform,investigation,action AS $$
DECLARE doc jsonb; item jsonb; subject_text text; target uuid; inserted boolean;
BEGIN
 IF NOT pg_has_role(session_user,'migration_role','member')
 OR pg_has_role(session_user,'api_runtime_role','member') OR pg_has_role(session_user,'worker_runtime_role','member')
 OR p_tenant IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid THEN
  RAISE EXCEPTION 'dedicated migration duty required' USING ERRCODE='42501';
 END IF;
 IF p_canonical IS NULL OR octet_length(p_canonical)>1048576 THEN RAISE EXCEPTION 'invalid inventory' USING ERRCODE='22023'; END IF;
 doc:=p_canonical::jsonb;
 IF doc - ARRAY['schemaVersion','tenantId','reviewedBy','entries'] <> '{}'::jsonb
 OR doc->>'schemaVersion' IS DISTINCT FROM 'native-subject-inventory/v1' OR (doc->>'tenantId')::uuid IS DISTINCT FROM p_tenant
 OR jsonb_typeof(doc->'reviewedBy') IS DISTINCT FROM 'string'
 OR COALESCE(length(btrim(doc->>'reviewedBy')),0) NOT BETWEEN 1 AND 256
 OR jsonb_typeof(doc->'entries') IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'invalid inventory header' USING ERRCODE='22023'; END IF;
 IF jsonb_array_length(doc->'entries') NOT BETWEEN 1 AND 1000 THEN RAISE EXCEPTION 'invalid inventory size' USING ERRCODE='22023'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('identity-admin:'||p_tenant::text,0));
 PERFORM 1 FROM identity.accounts WHERE tenant_id=p_tenant AND user_id IN(SELECT (value->>'userId')::uuid FROM jsonb_array_elements(doc->'entries')) ORDER BY user_id FOR SHARE;
 PERFORM 1 FROM platform.tenants WHERE tenant_id=p_tenant AND status='active' FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'tenant unavailable' USING ERRCODE='42501'; END IF;
 IF (SELECT count(DISTINCT value->>'legacySubject') FROM jsonb_array_elements(doc->'entries'))<>jsonb_array_length(doc->'entries') THEN RAISE EXCEPTION 'duplicate legacy subject' USING ERRCODE='22023'; END IF;
 FOR item IN SELECT value FROM jsonb_array_elements(doc->'entries') LOOP
  subject_text:=item->>'legacySubject'; target:=(item->>'userId')::uuid;
  IF item - ARRAY['legacySubject','userId'] <> '{}'::jsonb
   OR jsonb_typeof(item->'legacySubject') IS DISTINCT FROM 'string'
   OR jsonb_typeof(item->'userId') IS DISTINCT FROM 'string'
   OR COALESCE(length(subject_text),0) NOT BETWEEN 1 AND 256 OR length(btrim(subject_text))=0
   OR NOT EXISTS(SELECT 1 FROM identity.accounts WHERE tenant_id=p_tenant AND user_id=target)
   OR EXISTS(SELECT 1 FROM identity.accounts WHERE tenant_id=p_tenant AND subject=subject_text) THEN
   RAISE EXCEPTION 'inventory target unavailable or already a native principal' USING ERRCODE='22023';
  END IF;
  IF NOT(EXISTS(SELECT 1 FROM platform.role_bindings WHERE tenant_id=p_tenant AND subject=subject_text)
    OR EXISTS(SELECT 1 FROM investigation.assistant_conversations WHERE tenant_id=p_tenant AND owner_subject=subject_text)
    OR EXISTS(SELECT 1 FROM investigation.jobs WHERE tenant_id=p_tenant AND requesting_subject=subject_text)
    OR EXISTS(SELECT 1 FROM action.executions WHERE tenant_id=p_tenant AND subject=subject_text)) THEN
   RAISE EXCEPTION 'legacy subject is not in this tenant history' USING ERRCODE='22023';
  END IF;
  IF EXISTS(SELECT 1 FROM identity.subject_mappings existing_mapping
   WHERE existing_mapping.tenant_id=p_tenant AND existing_mapping.legacy_subject=subject_text AND existing_mapping.user_id<>target) THEN
   RAISE EXCEPTION 'existing historical mapping cannot be reassigned' USING ERRCODE='40001';
  END IF;
  IF EXISTS(SELECT 1 FROM identity.subject_mapping_inventories inventory, jsonb_array_elements(inventory.document->'entries') prior
   WHERE inventory.tenant_id=p_tenant AND prior->>'legacySubject'=subject_text AND (prior->>'userId')::uuid<>target) THEN
   RAISE EXCEPTION 'reviewed subject cannot be reassigned' USING ERRCODE='40001';
  END IF;
 END LOOP;
 INSERT INTO identity.subject_mapping_inventories(tenant_id,evidence_digest,document,imported_by)
 VALUES(p_tenant,sha256(convert_to(p_canonical,'UTF8')),doc,session_user) ON CONFLICT DO NOTHING;
 inserted:=FOUND; RETURN inserted;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.import_subject_inventory(uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.import_subject_inventory(uuid,text) TO migration_role;

-- +goose StatementBegin
CREATE FUNCTION identity.admin_map_subject(p_tenant uuid,p_actor uuid,p_session uuid,p_cv integer,p_av integer,p_sv integer,p_target uuid,p_body jsonb)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,identity AS $$
DECLARE current_revision integer; mapping uuid; proof bytea; item identity.subject_mappings%ROWTYPE;
BEGIN
 IF NOT identity.admin_lock_accounts(p_tenant,p_actor,p_session,p_cv,p_av,p_sv,p_target) THEN RAISE EXCEPTION 'native administrator required' USING ERRCODE='42501'; END IF;
 IF p_body - ARRAY['legacySubject','basis','evidenceDigest','expectedUserRevision'] <> '{}'::jsonb
  OR p_body->>'basis' IS DISTINCT FROM 'verified_inventory' OR p_body->>'evidenceDigest' !~ '^sha256:[a-f0-9]{64}$' THEN RAISE EXCEPTION 'invalid mapping' USING ERRCODE='22023'; END IF;
 SELECT a.account_revision INTO current_revision FROM identity.accounts a WHERE a.tenant_id=p_tenant AND a.user_id=p_target;
 IF NOT FOUND THEN RAISE EXCEPTION 'target unavailable' USING ERRCODE='P0002'; END IF;
 IF (p_body->>'expectedUserRevision')::integer IS DISTINCT FROM current_revision THEN RAISE EXCEPTION 'user revision stale' USING ERRCODE='40001'; END IF;
 proof:=decode(substr(p_body->>'evidenceDigest',8),'hex');
 IF NOT EXISTS(SELECT 1 FROM identity.subject_mapping_inventories inventory, jsonb_array_elements(inventory.document->'entries') entry
  WHERE inventory.tenant_id=p_tenant AND inventory.evidence_digest=proof AND entry->>'legacySubject'=p_body->>'legacySubject' AND (entry->>'userId')::uuid=p_target) THEN
  RAISE EXCEPTION 'reviewed inventory does not match this subject and user' USING ERRCODE='55000';
 END IF;
 IF EXISTS(SELECT 1 FROM identity.accounts WHERE tenant_id=p_tenant AND subject=p_body->>'legacySubject') THEN RAISE EXCEPTION 'native principal cannot be remapped' USING ERRCODE='22023'; END IF;
 mapping:=gen_random_uuid();
 INSERT INTO identity.subject_mappings(tenant_id,mapping_id,legacy_subject,user_id,basis,evidence_digest,inventory_digest)
 VALUES(p_tenant,mapping,p_body->>'legacySubject',p_target,'verified_inventory',proof,proof) RETURNING * INTO item;
 UPDATE identity.accounts a SET account_revision=a.account_revision+1,authorization_version=a.authorization_version+1,session_version=a.session_version+1,updated_at=clock_timestamp() WHERE a.tenant_id=p_tenant AND a.user_id=p_target;
 UPDATE identity.role_grants SET authorization_version=authorization_version+1 WHERE tenant_id=p_tenant AND user_id=p_target AND status='active';
 UPDATE identity.sessions SET revoked_at=clock_timestamp(),revoked_reason='explicit historical subject mapping' WHERE tenant_id=p_tenant AND user_id=p_target AND revoked_at IS NULL;
 RETURN jsonb_build_object('mappingId',item.mapping_id,'legacySubject',item.legacy_subject,'userId',item.user_id,'tenantId',item.tenant_id,'basis',item.basis,'evidenceDigest','sha256:'||encode(item.evidence_digest,'hex'),'mappedAt',item.mapped_at);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.admin_map_subject(uuid,uuid,uuid,integer,integer,integer,uuid,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.admin_map_subject(uuid,uuid,uuid,integer,integer,integer,uuid,jsonb) TO api_runtime_role;

-- +goose StatementBegin
CREATE FUNCTION identity.admin_subject_mappings(p_tenant uuid,p_actor uuid,p_session uuid,p_cv integer,p_av integer,p_sv integer,p_user uuid,p_subject text,p_after uuid,p_limit integer)
RETURNS SETOF jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,identity AS $$
BEGIN
 IF p_tenant IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 101
 OR NOT identity.authorize_native_session_role(p_tenant,p_actor,p_session,'platform_admin',p_av,p_cv,p_sv) THEN RAISE EXCEPTION 'native administrator required' USING ERRCODE='42501'; END IF;
 RETURN QUERY SELECT jsonb_build_object('mappingId',m.mapping_id,'legacySubject',m.legacy_subject,'userId',m.user_id,'tenantId',m.tenant_id,'basis',m.basis,'evidenceDigest','sha256:'||encode(m.evidence_digest,'hex'),'mappedAt',m.mapped_at)
 FROM identity.subject_mappings m WHERE m.tenant_id=p_tenant AND (p_user IS NULL OR m.user_id=p_user) AND (p_subject IS NULL OR m.legacy_subject=p_subject)
 AND (p_after IS NULL OR m.mapping_id>p_after) ORDER BY m.mapping_id LIMIT p_limit;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.admin_subject_mappings(uuid,uuid,uuid,integer,integer,integer,uuid,text,uuid,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.admin_subject_mappings(uuid,uuid,uuid,integer,integer,integer,uuid,text,uuid,integer) TO api_runtime_role;
RESET ROLE;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'subject inventories and mappings are forward-only; retain historical ownership evidence'; END $$;
-- +goose StatementEnd
