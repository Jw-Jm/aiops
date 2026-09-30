-- +goose Up
SET ROLE schema_owner;

ALTER TABLE platform.registry_versions
  ADD CONSTRAINT registry_versions_publication_identity_unique
    UNIQUE (tenant_id, version_id, kind, logical_name),
  ADD CONSTRAINT registry_versions_draft_identity_fk
    FOREIGN KEY (tenant_id, kind, logical_name, draft_id)
    REFERENCES platform.registry_drafts (tenant_id, kind, logical_name, draft_id);

ALTER TABLE platform.registry_activations
  ADD CONSTRAINT registry_activations_version_identity_fk
    FOREIGN KEY (tenant_id, version_id, kind, logical_name)
    REFERENCES platform.registry_versions (tenant_id, version_id, kind, logical_name);

CREATE TABLE platform.registry_activation_history (
  tenant_id uuid NOT NULL,
  activation_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  kind text NOT NULL CHECK (kind IN ('policy', 'recipe', 'tool')),
  logical_name text NOT NULL CHECK (length(logical_name) BETWEEN 1 AND 200),
  scope_type text NOT NULL CHECK (scope_type IN ('tenant', 'cluster', 'namespace')),
  cluster_id uuid,
  namespace text,
  version_id uuid NOT NULL,
  activated_by text NOT NULL CHECK (length(activated_by) BETWEEN 1 AND 512),
  activated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, activation_id, revision),
  FOREIGN KEY (tenant_id, version_id, kind, logical_name)
    REFERENCES platform.registry_versions (tenant_id, version_id, kind, logical_name),
  FOREIGN KEY (tenant_id, cluster_id)
    REFERENCES platform.cluster_registrations (tenant_id, cluster_id),
  CHECK ((scope_type = 'tenant' AND cluster_id IS NULL AND namespace IS NULL)
      OR (scope_type = 'cluster' AND cluster_id IS NOT NULL AND namespace IS NULL)
      OR (scope_type = 'namespace' AND cluster_id IS NOT NULL AND namespace IS NOT NULL))
);
CREATE INDEX registry_activation_history_resolve_idx
  ON platform.registry_activation_history
    (tenant_id, kind, logical_name, scope_type, cluster_id, namespace, activated_at DESC, revision DESC);
ALTER TABLE platform.registry_activation_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform.registry_activation_history FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON platform.registry_activation_history
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- Keep draft payloads editable only while they remain drafts. A draft can become published
-- only after its immutable, signed version row has been inserted in the same transaction.
-- +goose StatementBegin
CREATE FUNCTION platform.guard_registry_draft_lifecycle() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status <> 'draft' THEN
      RAISE EXCEPTION 'published registry history cannot be deleted' USING ERRCODE = '23514';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.tenant_id IS DISTINCT FROM NEW.tenant_id
     OR OLD.draft_id IS DISTINCT FROM NEW.draft_id
     OR OLD.kind IS DISTINCT FROM NEW.kind
     OR OLD.logical_name IS DISTINCT FROM NEW.logical_name
     OR NEW.revision <= OLD.revision THEN
    RAISE EXCEPTION 'registry draft identity is immutable and revisions must increase' USING ERRCODE = '23514';
  END IF;
  IF OLD.status = 'retired' THEN
    RAISE EXCEPTION 'retired registry drafts are immutable' USING ERRCODE = '23514';
  ELSIF OLD.status = 'published' THEN
    IF NEW.status <> 'retired' OR NEW.content IS DISTINCT FROM OLD.content OR NOT EXISTS (
      SELECT 1 FROM platform.registry_versions v
      WHERE v.tenant_id = OLD.tenant_id AND v.draft_id = OLD.draft_id
        AND v.retired_at IS NOT NULL
    ) THEN
      RAISE EXCEPTION 'published registry drafts are immutable until their version is retired' USING ERRCODE = '23514';
    END IF;
  ELSIF NEW.status = 'published' AND NOT EXISTS (
    SELECT 1 FROM platform.registry_versions v
    WHERE v.tenant_id = NEW.tenant_id AND v.draft_id = NEW.draft_id
      AND v.kind = NEW.kind AND v.logical_name = NEW.logical_name
  ) THEN
    RAISE EXCEPTION 'registry draft cannot publish without an immutable version' USING ERRCODE = '23514';
  ELSIF NEW.status NOT IN ('draft', 'published', 'retired') THEN
    RAISE EXCEPTION 'unsupported registry draft state' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER registry_draft_lifecycle_guard
  BEFORE UPDATE OR DELETE ON platform.registry_drafts
  FOR EACH ROW EXECUTE FUNCTION platform.guard_registry_draft_lifecycle();

-- Published version content and signature are immutable. The only permitted change is
-- a one-way retirement timestamp, which leaves the signed publication bytes unchanged.
-- +goose StatementBegin
CREATE FUNCTION platform.guard_registry_version_immutability() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF ROW(NEW.tenant_id, NEW.version_id, NEW.draft_id, NEW.kind, NEW.logical_name,
         NEW.version_number, NEW.content, NEW.digest, NEW.signature, NEW.signer_key_id,
         NEW.published_by, NEW.published_at)
     IS DISTINCT FROM
     ROW(OLD.tenant_id, OLD.version_id, OLD.draft_id, OLD.kind, OLD.logical_name,
         OLD.version_number, OLD.content, OLD.digest, OLD.signature, OLD.signer_key_id,
         OLD.published_by, OLD.published_at)
     OR OLD.retired_at IS NOT NULL OR NEW.retired_at IS NULL THEN
    RAISE EXCEPTION 'published registry version content and retirement are immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER registry_version_immutability_guard
  BEFORE UPDATE ON platform.registry_versions
  FOR EACH ROW EXECUTE FUNCTION platform.guard_registry_version_immutability();

REVOKE DELETE, TRUNCATE ON platform.registry_drafts FROM PUBLIC, api_runtime_role, worker_runtime_role, ops_readonly_role;
REVOKE UPDATE, DELETE, TRUNCATE ON platform.registry_versions FROM PUBLIC, api_runtime_role, worker_runtime_role, ops_readonly_role;
GRANT UPDATE (retired_at) ON platform.registry_versions TO api_runtime_role;
GRANT SELECT, INSERT ON platform.registry_activation_history TO api_runtime_role;
GRANT SELECT ON platform.registry_activation_history TO worker_runtime_role;
REVOKE UPDATE, DELETE, TRUNCATE ON platform.registry_activation_history FROM PUBLIC, api_runtime_role, worker_runtime_role, ops_readonly_role;

RESET ROLE;
