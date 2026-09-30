-- +goose Up
SET ROLE schema_owner;

CREATE TABLE platform.role_bindings (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  binding_id uuid NOT NULL,
  subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 512),
  role_name text NOT NULL CHECK (role_name IN ('operator', 'platform_admin')),
  cluster_scopes jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(cluster_scopes) = 'array'),
  namespace_scopes jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(namespace_scopes) = 'array'),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, binding_id),
  UNIQUE (tenant_id, subject, role_name, binding_id)
);
CREATE INDEX role_bindings_subject_idx ON platform.role_bindings (tenant_id, subject, status);

CREATE TABLE platform.cluster_registrations (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  cluster_id uuid NOT NULL,
  cluster_uid text NOT NULL CHECK (length(cluster_uid) BETWEEN 1 AND 512),
  display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 200),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, cluster_id),
  UNIQUE (tenant_id, cluster_uid)
);

CREATE TABLE platform.source_registrations (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  source_id uuid NOT NULL,
  source_type text NOT NULL CHECK (source_type IN ('victoriametrics', 'victorialogs', 'deepflow', 'kubernetes', 'redfish', 'ipmi', 'smart', 'object_storage', 'model')),
  instance_key text NOT NULL CHECK (length(instance_key) BETWEEN 1 AND 512),
  cluster_id uuid,
  auth_ref text NOT NULL CHECK (auth_ref ~ '^openbao://[A-Za-z0-9_./-]+$'),
  credential_revision bigint NOT NULL DEFAULT 1 CHECK (credential_revision > 0),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'rotated')),
  config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, source_id),
  UNIQUE (tenant_id, source_type, instance_key),
  FOREIGN KEY (tenant_id, cluster_id) REFERENCES platform.cluster_registrations (tenant_id, cluster_id)
);

CREATE TABLE platform.source_registration_revisions (
  tenant_id uuid NOT NULL,
  source_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  auth_ref text NOT NULL CHECK (auth_ref ~ '^openbao://[A-Za-z0-9_./-]+$'),
  credential_revision bigint NOT NULL CHECK (credential_revision > 0),
  status text NOT NULL CHECK (status IN ('active', 'disabled', 'rotated')),
  scope jsonb NOT NULL CHECK (jsonb_typeof(scope) = 'object'),
  actor_subject text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, source_id, revision),
  FOREIGN KEY (tenant_id, source_id) REFERENCES platform.source_registrations (tenant_id, source_id)
);

CREATE TABLE platform.registry_drafts (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  draft_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('policy', 'recipe', 'tool')),
  logical_name text NOT NULL CHECK (length(logical_name) BETWEEN 1 AND 200),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  content jsonb NOT NULL CHECK (jsonb_typeof(content) = 'object'),
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'retired')),
  updated_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, draft_id),
  UNIQUE (tenant_id, kind, logical_name, draft_id)
);

CREATE TABLE platform.registry_versions (
  tenant_id uuid NOT NULL,
  version_id uuid NOT NULL,
  draft_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('policy', 'recipe', 'tool')),
  logical_name text NOT NULL,
  version_number integer NOT NULL CHECK (version_number > 0),
  content jsonb NOT NULL CHECK (jsonb_typeof(content) = 'object'),
  digest text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
  signature bytea NOT NULL CHECK (octet_length(signature) > 0),
  signer_key_id text NOT NULL CHECK (length(signer_key_id) BETWEEN 1 AND 200),
  published_by text NOT NULL,
  published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  retired_at timestamptz,
  PRIMARY KEY (tenant_id, version_id),
  UNIQUE (tenant_id, kind, logical_name, version_number),
  FOREIGN KEY (tenant_id, draft_id) REFERENCES platform.registry_drafts (tenant_id, draft_id)
);

CREATE TABLE platform.registry_activations (
  tenant_id uuid NOT NULL,
  activation_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('policy', 'recipe', 'tool')),
  logical_name text NOT NULL,
  scope_type text NOT NULL CHECK (scope_type IN ('tenant', 'cluster', 'namespace')),
  cluster_id uuid,
  namespace text,
  version_id uuid NOT NULL,
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  activated_by text NOT NULL,
  activated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, activation_id),
  UNIQUE NULLS NOT DISTINCT (tenant_id, kind, logical_name, scope_type, cluster_id, namespace),
  FOREIGN KEY (tenant_id, version_id) REFERENCES platform.registry_versions (tenant_id, version_id),
  FOREIGN KEY (tenant_id, cluster_id) REFERENCES platform.cluster_registrations (tenant_id, cluster_id),
  CHECK ((scope_type = 'tenant' AND cluster_id IS NULL AND namespace IS NULL)
      OR (scope_type = 'cluster' AND cluster_id IS NOT NULL AND namespace IS NULL)
      OR (scope_type = 'namespace' AND cluster_id IS NOT NULL AND namespace IS NOT NULL))
);

CREATE TABLE platform.execution_profile_versions (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  profile_version_id uuid NOT NULL,
  logical_name text NOT NULL CHECK (length(logical_name) BETWEEN 1 AND 200),
  version_number integer NOT NULL CHECK (version_number > 0),
  content jsonb NOT NULL CHECK (jsonb_typeof(content) = 'object'),
  digest text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
  signature bytea NOT NULL CHECK (octet_length(signature) > 0),
  signer_key_id text NOT NULL,
  status text NOT NULL CHECK (status IN ('published', 'retired')),
  published_by text NOT NULL,
  published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, profile_version_id),
  UNIQUE (tenant_id, logical_name, version_number)
);

CREATE TABLE platform.step_up_sessions (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  session_id uuid NOT NULL,
  subject text NOT NULL,
  keycloak_sid text NOT NULL,
  acr text NOT NULL,
  auth_time timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  last_used_at timestamptz NOT NULL,
  revoked_at timestamptz,
  PRIMARY KEY (tenant_id, session_id),
  UNIQUE (tenant_id, subject, keycloak_sid)
);

GRANT USAGE ON SCHEMA platform TO api_runtime_role, worker_runtime_role, ops_readonly_role;
GRANT SELECT, INSERT, UPDATE, DELETE ON platform.role_bindings TO api_runtime_role;
GRANT SELECT ON platform.role_bindings TO worker_runtime_role;
GRANT SELECT, INSERT, UPDATE, DELETE ON platform.cluster_registrations TO api_runtime_role;
GRANT SELECT ON platform.cluster_registrations TO worker_runtime_role;
GRANT SELECT, INSERT, UPDATE, DELETE ON platform.source_registrations TO api_runtime_role;
GRANT SELECT ON platform.source_registrations TO worker_runtime_role;
GRANT SELECT, INSERT ON platform.source_registration_revisions TO api_runtime_role;
GRANT SELECT ON platform.source_registration_revisions TO worker_runtime_role;
GRANT SELECT, INSERT, UPDATE, DELETE ON platform.registry_drafts TO api_runtime_role;
GRANT SELECT, INSERT ON platform.registry_versions TO api_runtime_role;
GRANT SELECT ON platform.registry_versions TO worker_runtime_role;
GRANT SELECT, INSERT, UPDATE ON platform.registry_activations TO api_runtime_role;
GRANT SELECT ON platform.registry_activations TO worker_runtime_role;
GRANT SELECT, INSERT ON platform.execution_profile_versions TO api_runtime_role;
GRANT SELECT ON platform.execution_profile_versions TO worker_runtime_role;
GRANT SELECT, INSERT, UPDATE ON platform.step_up_sessions TO api_runtime_role;

RESET ROLE;
