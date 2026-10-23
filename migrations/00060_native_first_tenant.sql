-- +goose Up
-- The native first-tenant cold start creates its tenant under the dedicated
-- migration-duty LOGIN. That duty previously had SELECT only on platform.tenants,
-- so a native cold start could not exist without OIDC or the broader API role.
-- This grants exactly INSERT on the tenant table: no UPDATE, no DELETE and no
-- other platform relation.
SET ROLE schema_owner;
GRANT INSERT ON platform.tenants TO migration_role;
RESET ROLE;

-- +goose Down
-- Forward-only.
