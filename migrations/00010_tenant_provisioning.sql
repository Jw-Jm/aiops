-- +goose Up
SET ROLE schema_owner;

REVOKE INSERT, UPDATE ON platform.tenants FROM api_runtime_role;

-- Tenant creation is the only supported cross-tenant write. The definer function binds
-- the caller's active platform_admin role to the new tenant while preserving tenant RLS.
-- +goose StatementBegin
CREATE FUNCTION platform.provision_tenant(
  p_actor_tenant_id uuid,
  p_actor_subject text,
  p_tenant_id uuid,
  p_binding_id uuid,
  p_slug text,
  p_display_name text
) RETURNS TABLE(
  created_tenant_id uuid,
  created_slug text,
  created_display_name text,
  created_status text,
  created_revision bigint,
  created_at timestamptz,
  updated_at timestamptz,
  initial_binding_id uuid
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform
AS $$
DECLARE
  expected_tenant uuid;
  prior_tenant_setting text;
BEGIN
  expected_tenant := NULLIF(current_setting('app.tenant_id', true), '')::uuid;
  IF expected_tenant IS NULL OR expected_tenant <> p_actor_tenant_id OR p_actor_subject IS NULL OR length(p_actor_subject) = 0 THEN
    RAISE EXCEPTION 'tenant provisioning actor context mismatch' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM platform.role_bindings
    WHERE tenant_id = p_actor_tenant_id AND subject = p_actor_subject
      AND role_name = 'platform_admin' AND status = 'active'
  ) THEN
    RAISE EXCEPTION 'platform_admin binding is required to provision a tenant' USING ERRCODE = '42501';
  END IF;
  IF p_tenant_id IS NULL OR p_binding_id IS NULL OR p_slug IS NULL OR p_display_name IS NULL THEN
    RAISE EXCEPTION 'tenant provisioning fields are required' USING ERRCODE = '22023';
  END IF;

  INSERT INTO platform.tenants (tenant_id, slug, display_name)
  VALUES (p_tenant_id, p_slug, p_display_name);

  prior_tenant_setting := current_setting('app.tenant_id', true);
  PERFORM set_config('app.tenant_id', p_tenant_id::text, true);
  INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name, cluster_scopes, namespace_scopes)
  VALUES (p_tenant_id, p_binding_id, p_actor_subject, 'platform_admin', '[]'::jsonb, '[]'::jsonb);
  PERFORM set_config('app.tenant_id', COALESCE(prior_tenant_setting, ''), true);

  RETURN QUERY SELECT t.tenant_id, t.slug, t.display_name, t.status, t.revision, t.created_at, t.updated_at, p_binding_id
  FROM platform.tenants AS t WHERE t.tenant_id = p_tenant_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION platform.update_tenant(
  p_tenant_id uuid,
  p_actor_subject text,
  p_expected_revision bigint,
  p_display_name text,
  p_status text
) RETURNS TABLE(
  updated_tenant_id uuid,
  updated_slug text,
  updated_display_name text,
  updated_status text,
  updated_revision bigint,
  created_at timestamptz,
  updated_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform
AS $$
DECLARE
  expected_tenant uuid;
BEGIN
  expected_tenant := NULLIF(current_setting('app.tenant_id', true), '')::uuid;
  IF expected_tenant IS NULL OR expected_tenant <> p_tenant_id OR p_actor_subject IS NULL OR length(p_actor_subject) = 0 THEN
    RAISE EXCEPTION 'tenant update actor context mismatch' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM platform.role_bindings
    WHERE tenant_id = p_tenant_id AND subject = p_actor_subject
      AND role_name = 'platform_admin' AND status = 'active'
  ) THEN
    RAISE EXCEPTION 'platform_admin binding is required to update a tenant' USING ERRCODE = '42501';
  END IF;
  IF p_expected_revision < 1 OR p_display_name IS NULL OR length(p_display_name) NOT BETWEEN 1 AND 200
     OR p_status IS NULL OR p_status NOT IN ('active', 'disabled') THEN
    RAISE EXCEPTION 'invalid tenant update' USING ERRCODE = '22023';
  END IF;

  RETURN QUERY
  UPDATE platform.tenants AS t
  SET display_name = p_display_name, status = p_status, revision = revision + 1, updated_at = clock_timestamp()
  WHERE t.tenant_id = p_tenant_id AND t.revision = p_expected_revision
  RETURNING t.tenant_id, t.slug, t.display_name, t.status, t.revision, t.created_at, t.updated_at;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION platform.provision_tenant(uuid, text, uuid, uuid, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.provision_tenant(uuid, text, uuid, uuid, text, text) TO api_runtime_role;
REVOKE ALL ON FUNCTION platform.update_tenant(uuid, text, bigint, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.update_tenant(uuid, text, bigint, text, text) TO api_runtime_role;
RESET ROLE;
