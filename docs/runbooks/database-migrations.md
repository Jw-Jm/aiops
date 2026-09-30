# PostgreSQL migration runbook

SP-03 migrations are numbered, Goose-managed SQL files in `migrations/`. They are forward-only; no migration contains a `Down` section. Keep the six roles and tenant schemas in `00001` as the privilege boundary for later migrations.

## First database bootstrap

The first migration creates database roles, so apply version 1 from the deployment's controlled bootstrap identity. Supply its DSN from the deployment secret store through `SP03_MIGRATION_DATABASE_URL`; never place a DSN in a file committed to Git or print it in diagnostics.

```sh
SP03_MIGRATION_DATABASE_URL="$SP03_BOOTSTRAP_DSN" go run ./cmd/db-migrate -dir migrations -to 1
```

For version 2 and later, use a login explicitly granted membership in the `NOLOGIN` `migration_role`, with the connection startup option `-c role=migration_role`. The migration role can set `schema_owner` for DDL and can update Goose's version table; API and Worker roles cannot. The migration utility does not echo the DSN.

```sh
SP03_MIGRATION_DATABASE_URL="$SP03_MIGRATOR_DSN" go run ./cmd/db-migrate -dir migrations
```

For an existing database already at version 1, run only the second command. The migration process is independent of the API and Worker runtime credentials.

## Failure recovery

Each current SQL migration is transactional. If a statement fails, Goose rolls the migration transaction back and leaves its version unapplied. Read the sanitized PostgreSQL error, correct the unapplied migration before the first deployment of that schema, then rerun the same forward command. Once a migration has been applied to a shared environment, preserve it and add a new forward migration for corrections; do not edit an applied file, run Goose `down`, or manually change `goose_db_version`.

Before applying migrations to a persistent environment, take the deployment's normal PostgreSQL backup/PITR checkpoint and record the database identity and current Goose version. Verify the result with the migration utility's exit status and a read-only check of `public.goose_db_version`. Restore from the recorded checkpoint only when the failed migration committed non-transactional side effects or the database fails its post-migration integrity checks; none of the current SP-03 SQL migrations uses non-transactional DDL.
