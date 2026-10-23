-- +goose Up
-- Forward-only native account foundation for P2-01.
--
-- This migration adds storage only. It deliberately does not change the OIDC
-- HTTP path, grant business-table authority, or introduce a login route.
-- Existing duty roles are reused: api_runtime_role can execute only the fixed
-- SECURITY DEFINER functions; migration_role remains the controlled local
-- bootstrap duty. No later-created capability role is required, because
-- ordinary migrations cannot create roles after version 1. api_runtime_role
-- receives no direct identity-table grant and cannot read credential material,
-- token digests, or another tenant''s account.
--
-- The first-administrator CLI refuses a connection unless its LOGIN is a
-- member of migration_role and is not superuser or BYPASSRLS. This is the
-- controlled local bootstrap duty, not the HTTP API LOGIN.

SET ROLE schema_owner;

CREATE SCHEMA IF NOT EXISTS identity AUTHORIZATION schema_owner;
REVOKE ALL ON SCHEMA identity FROM PUBLIC;
GRANT USAGE ON SCHEMA identity TO migration_role, api_runtime_role;
GRANT USAGE ON SCHEMA platform, audit TO migration_role;
GRANT SELECT ON platform.tenants TO migration_role;
SET ROLE audit_append_owner;
GRANT EXECUTE ON FUNCTION audit.append_record_v2(uuid, uuid, text, text, uuid, text, jsonb, bytea, timestamptz) TO migration_role;
SET ROLE schema_owner;

CREATE TABLE identity.accounts (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  user_id uuid NOT NULL UNIQUE,
  login_name text NOT NULL CHECK (
    login_name ~ '^[a-z0-9][a-z0-9._-]{0,127}$' AND length(login_name) BETWEEN 1 AND 128
  ),
  display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 256),
  subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 256),
  status text NOT NULL CHECK (status IN ('active', 'disabled', 'archived')),
  password_state text NOT NULL CHECK (password_state IN ('change_required', 'active')),
  must_change_password boolean NOT NULL,
  account_revision integer NOT NULL CHECK (account_revision BETWEEN 1 AND 2147483647),
  credential_version integer NOT NULL CHECK (credential_version BETWEEN 1 AND 2147483647),
  authorization_version integer NOT NULL CHECK (authorization_version BETWEEN 1 AND 2147483647),
  session_version integer NOT NULL CHECK (session_version BETWEEN 1 AND 2147483647),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, user_id),
  UNIQUE (login_name),
  UNIQUE (tenant_id, subject),
  CHECK ((password_state = 'change_required') = must_change_password),
  CHECK (updated_at >= created_at)
);

CREATE INDEX accounts_tenant_idx ON identity.accounts (tenant_id, status);

CREATE TABLE identity.credentials (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  user_id uuid NOT NULL,
  credential_version integer NOT NULL CHECK (credential_version BETWEEN 1 AND 2147483647),
  password_hash text NOT NULL CHECK (
    length(password_hash) BETWEEN 80 AND 512
    AND password_hash ~ '^argon2id\$v=19\$m=[0-9]+,t=[0-9]+,p=[0-9]+\$[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}$'
  ),
  status text NOT NULL CHECK (status IN ('active', 'retired')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, user_id, credential_version),
  UNIQUE (user_id, credential_version),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);

CREATE TABLE identity.password_attempts (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  user_id uuid NOT NULL,
  failed_count integer NOT NULL CHECK (failed_count BETWEEN 0 AND 1000000),
  window_started_at timestamptz NOT NULL,
  locked_until timestamptz,
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, user_id),
  CHECK (locked_until IS NULL OR locked_until > window_started_at),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);

CREATE TABLE identity.sessions (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  session_id uuid PRIMARY KEY,
  user_id uuid NOT NULL,
  mode text NOT NULL CHECK (mode IN ('full', 'first_password_only')),
  token_digest bytea NOT NULL CHECK (octet_length(token_digest) = 32),
  csrf_digest bytea NOT NULL CHECK (octet_length(csrf_digest) = 32),
  issued_credential_version integer NOT NULL CHECK (issued_credential_version > 0),
  issued_authorization_version integer NOT NULL CHECK (issued_authorization_version > 0),
  issued_at timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  idle_expires_at timestamptz NOT NULL,
  absolute_expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  revoked_reason text CHECK (revoked_reason IS NULL OR length(revoked_reason) BETWEEN 1 AND 512),
  UNIQUE (token_digest),
  UNIQUE (csrf_digest),
  CHECK (idle_expires_at > issued_at),
  CHECK (absolute_expires_at > idle_expires_at),
  CHECK (last_seen_at BETWEEN issued_at AND idle_expires_at),
  CHECK (
    (revoked_at IS NULL AND revoked_reason IS NULL)
    OR (revoked_at IS NOT NULL AND revoked_reason IS NOT NULL AND revoked_at >= issued_at)
  ),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);

CREATE INDEX sessions_active_idx ON identity.sessions (tenant_id, user_id, absolute_expires_at) WHERE revoked_at IS NULL;

CREATE TABLE identity.role_grants (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  user_id uuid NOT NULL,
  binding_id uuid NOT NULL,
  role text NOT NULL CHECK (role IN ('operator', 'platform_admin')),
  scope jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(scope) = 'object'),
  authorization_version integer NOT NULL CHECK (authorization_version BETWEEN 1 AND 2147483647),
  status text NOT NULL CHECK (status IN ('active', 'revoked')),
  granted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  revoked_at timestamptz,
  revoked_reason text CHECK (revoked_reason IS NULL OR length(revoked_reason) BETWEEN 1 AND 512),
  PRIMARY KEY (tenant_id, binding_id),
  CHECK (
    (status = 'active' AND revoked_at IS NULL AND revoked_reason IS NULL)
    OR (status = 'revoked' AND revoked_at IS NOT NULL AND revoked_reason IS NOT NULL AND revoked_at >= granted_at)
  ),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);

CREATE UNIQUE INDEX role_grants_active_unique_idx
  ON identity.role_grants (tenant_id, user_id, role) WHERE status = 'active';

CREATE TABLE identity.subject_mappings (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  mapping_id uuid PRIMARY KEY,
  legacy_subject text NOT NULL CHECK (length(legacy_subject) BETWEEN 1 AND 256),
  user_id uuid NOT NULL,
  basis text NOT NULL CHECK (basis = 'verified_inventory'),
  evidence_digest bytea NOT NULL CHECK (octet_length(evidence_digest) = 32),
  mapped_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (tenant_id, legacy_subject),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);

CREATE TABLE identity.administrator_initializations (
  tenant_id uuid PRIMARY KEY REFERENCES platform.tenants (tenant_id),
  user_id uuid NOT NULL,
  initialized_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  actor_subject text NOT NULL CHECK (actor_subject = 'bootstrap:first-administrator'),
  FOREIGN KEY (tenant_id, user_id) REFERENCES identity.accounts (tenant_id, user_id)
);

-- The pre-auth gate is not an authorization credential supplied by a client.
-- Every SECURITY DEFINER function first resets app.identity_preauth, then sets
-- the exact operation name. Direct table access is not granted to API/Worker;
-- policy additionally requires the security-definer user (schema_owner) and an
-- api-duty session user. A lingering local GUC alone cannot read these tables.
-- +goose StatementBegin
CREATE FUNCTION identity.preauth_context_active(p_operation text)
RETURNS boolean
LANGUAGE sql
STABLE
SET search_path = pg_catalog, identity
AS $$
  SELECT current_user = 'schema_owner'
    AND session_user <> current_user
    AND pg_has_role(session_user, 'api_runtime_role', 'member')
    AND current_setting('app.identity_preauth', true) = p_operation;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION identity.preauth_context_active(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.preauth_context_active(text) TO api_runtime_role, migration_role;

-- Every business identity table is tenant-scoped and cannot bypass RLS.
ALTER TABLE identity.accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.accounts
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('find_login_account')
    OR identity.preauth_context_active('record_password_failure')
    OR identity.preauth_context_active('clear_password_failure')
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('record_password_failure')
  );

ALTER TABLE identity.credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.credentials
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('find_login_account')
    OR identity.preauth_context_active('record_password_failure')
    OR identity.preauth_context_active('clear_password_failure')
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('record_password_failure')
  );

ALTER TABLE identity.password_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.password_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.password_attempts
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('find_login_account')
    OR identity.preauth_context_active('record_password_failure')
    OR identity.preauth_context_active('clear_password_failure')
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR identity.preauth_context_active('record_password_failure')
  );

ALTER TABLE identity.sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.sessions
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  );

ALTER TABLE identity.role_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.role_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.role_grants
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  );

ALTER TABLE identity.subject_mappings ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.subject_mappings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.subject_mappings
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  );

ALTER TABLE identity.administrator_initializations ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.administrator_initializations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON identity.administrator_initializations
  USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  );

-- No credential, token, mapping, or grant table is granted to API/Worker.
-- The controlled migration/bootstrap duty receives only these explicit table
-- grants and remains subject to FORCE RLS; it cannot bypass tenant isolation.
GRANT SELECT, INSERT, UPDATE ON identity.accounts,
  identity.credentials, identity.password_attempts,
  identity.sessions, identity.role_grants,
  identity.administrator_initializations
  TO migration_role;
GRANT INSERT, SELECT ON identity.subject_mappings TO migration_role;

-- +goose StatementBegin
CREATE FUNCTION identity.find_login_account(p_login_name text)
RETURNS TABLE (
  tenant_id uuid,
  user_id uuid,
  login_name text,
  display_name text,
  status text,
  password_state text,
  must_change_password boolean,
  account_revision integer,
  credential_version integer,
  authorization_version integer,
  session_version integer,
  password_hash text,
  failed_count integer,
  window_started_at timestamptz,
  locked_until timestamptz
)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  -- Reset first: a caller-supplied value is never accepted as trusted context.
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'find_login_account', true);
  IF p_login_name IS NULL
     OR p_login_name !~ '^[a-z0-9][a-z0-9._-]{0,127}$'
     OR length(p_login_name) NOT BETWEEN 1 AND 128 THEN
    RETURN;
  END IF;
  RETURN QUERY
  SELECT a.tenant_id, a.user_id, a.login_name, a.display_name, a.status,
         a.password_state, a.must_change_password, a.account_revision,
         a.credential_version, a.authorization_version, a.session_version,
         c.password_hash, COALESCE(attempt.failed_count, 0),
         attempt.window_started_at, attempt.locked_until
  FROM identity.accounts a
  JOIN identity.credentials c
    ON c.tenant_id = a.tenant_id
   AND c.user_id = a.user_id
   AND c.credential_version = a.credential_version
   AND c.status = 'active'
  LEFT JOIN identity.password_attempts attempt
    ON attempt.tenant_id = a.tenant_id
   AND attempt.user_id = a.user_id
  WHERE a.login_name = p_login_name
    AND a.status <> 'archived';
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.find_login_account(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.find_login_account(text) TO api_runtime_role;

-- +goose StatementBegin
CREATE FUNCTION identity.record_password_failure(
  p_tenant_id uuid,
  p_user_id uuid,
  p_failed_at timestamptz,
  p_locked_until timestamptz
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'record_password_failure', true);
  IF p_failed_at IS NULL
     OR (p_locked_until IS NOT NULL AND p_locked_until <= p_failed_at) THEN
    RAISE EXCEPTION 'invalid password failure record' USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM identity.accounts account
    WHERE account.tenant_id = p_tenant_id
      AND account.user_id = p_user_id
      AND account.status <> 'archived'
  ) THEN
    RAISE EXCEPTION 'password failure target account does not exist'
      USING ERRCODE = '23514';
  END IF;
  INSERT INTO identity.password_attempts AS attempts
    (tenant_id, user_id, failed_count, window_started_at, locked_until, updated_at)
  VALUES
    (p_tenant_id, p_user_id, 1, p_failed_at, p_locked_until, p_failed_at)
  ON CONFLICT (tenant_id, user_id) DO UPDATE
    SET failed_count = CASE
          WHEN attempts.window_started_at <= p_failed_at - interval '15 minutes'
            THEN 1
          ELSE attempts.failed_count + 1
        END,
        window_started_at = CASE
          WHEN attempts.window_started_at <= p_failed_at - interval '15 minutes'
            THEN p_failed_at
          ELSE attempts.window_started_at
        END,
        -- Never lower an existing lock, including when the caller sends NULL.
        locked_until = GREATEST(attempts.locked_until, p_locked_until),
        updated_at = p_failed_at;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION identity.clear_password_failure(p_tenant_id uuid, p_user_id uuid)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'clear_password_failure', true);
  IF NOT EXISTS (
    SELECT 1 FROM identity.accounts account
    WHERE account.tenant_id = p_tenant_id
      AND account.user_id = p_user_id
      AND account.status <> 'archived'
  ) THEN
    RAISE EXCEPTION 'password failure target account does not exist'
      USING ERRCODE = '23514';
  END IF;
  DELETE FROM identity.password_attempts
  WHERE tenant_id = p_tenant_id
    AND user_id = p_user_id
    AND (locked_until IS NULL OR locked_until <= clock_timestamp());
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.record_password_failure(uuid, uuid, timestamptz, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION identity.clear_password_failure(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.preauth_context_active(text) TO api_runtime_role, migration_role;
GRANT EXECUTE ON FUNCTION identity.record_password_failure(uuid, uuid, timestamptz, timestamptz) TO api_runtime_role;
GRANT EXECUTE ON FUNCTION identity.clear_password_failure(uuid, uuid) TO api_runtime_role;

RESET ROLE;
