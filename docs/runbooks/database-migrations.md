# PostgreSQL migration runbook

SP-03 migrations are numbered, Goose-managed SQL files in `migrations/`. The supported execution path is forward-only. New migrations must not contain a `Down` section. Historical migration `00038` retains its original Down text solely to preserve the applied artifact; the static gate permits only its exact recorded file digest, and this does not authorize executing Down. Keep the six roles and tenant schemas in `00001` as the privilege boundary for later migrations.

## First database bootstrap

The first migration creates database roles, so apply version 1 from the deployment's controlled bootstrap identity. Supply its DSN from the deployment secret store through `SP03_MIGRATION_DATABASE_URL`; never place a DSN in a file committed to Git or print it in diagnostics.

```sh
SP03_MIGRATION_DATABASE_URL="$SP03_BOOTSTRAP_DSN" go run ./cmd/db-migrate -dir migrations -to 1
```

After version 1 and before advancing, run the formal account bootstrap with three distinct private LOGIN credentials:

```sh
OPS_BOOTSTRAP_DATABASE_URL="$SP03_BOOTSTRAP_DSN" go run ./cmd/opsctl bootstrap database-logins --secrets-file "$SP03_DATABASE_LOGINS_FILE"
```

This guarded version-1 transaction creates the three restricted LOGINs and grants `CREATE` on the current database only to the non-login `schema_owner`. This is schema creation within that database, not the PostgreSQL `CREATEDB` role attribute. Later migrations create additional schemas (including `identity` in version 44). API and Worker duties receive no database CREATE privilege and cannot assume `schema_owner`.

For version 2 and later, use a login explicitly granted membership in the `NOLOGIN` `migration_role`, with the connection startup option `-c role=migration_role`. The migration role can set `schema_owner` for DDL and can update Goose's version table; API and Worker roles cannot. The migration utility does not echo the DSN.

```sh
SP03_MIGRATION_DATABASE_URL="$SP03_MIGRATOR_DSN" go run ./cmd/db-migrate -dir migrations
```

For an existing version-1 database whose three LOGINs are not yet provisioned, perform the account bootstrap above before advancing. Do not repeat account initialization for an existing installation. When upgrading an older installation that predates this schema-creation grant, the controlled database administrator must first grant `CREATE` on the exact deployment database to `schema_owner`; do not grant it to runtime LOGINs, edit an applied migration, or reset version history. A database already at version 70 can then advance through 71/72 using only its migration LOGIN. The migration process is independent of the API and Worker runtime credentials.

## Failure recovery

Each current SQL migration is transactional. If a statement fails, Goose rolls the migration transaction back and leaves its version unapplied. Read the sanitized PostgreSQL error, correct the unapplied migration before the first deployment of that schema, then rerun the same forward command. Once a migration has been applied to a shared environment, preserve it and add a new forward migration for corrections; do not edit an applied file, run Goose `down`, or manually change `goose_db_version`.

Before applying migrations to a persistent environment, take the deployment's normal PostgreSQL backup/PITR checkpoint and record the database identity and current Goose version. Verify the result with the migration utility's exit status and a read-only check of `public.goose_db_version`. Restore from the recorded checkpoint only when the failed migration committed non-transactional side effects or the database fails its post-migration integrity checks; none of the current SP-03 SQL migrations uses non-transactional DDL.
