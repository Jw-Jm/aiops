package integration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/persistence"
)

func TestMigrationScriptsAreForwardOnlyAndContiguous(t *testing.T) {
	files, err := os.ReadDir("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	version := 1
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		wantPrefix := fmt.Sprintf("%05d_", version)
		if !strings.HasPrefix(file.Name(), wantPrefix) {
			t.Fatalf("migration order is not contiguous: got %q, expected prefix %q", file.Name(), wantPrefix)
		}
		source, err := os.ReadFile(filepath.Join("../../migrations", file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		if !strings.HasPrefix(text, "-- +goose Up\n") || strings.Contains(text, "-- +goose Down") {
			t.Errorf("migration %s must provide only a forward Goose Up section", file.Name())
		}
		version++
	}
	if version == 1 {
		t.Fatal("no SQL migrations found")
	}
}

func TestMigrationsAndRoles(t *testing.T) {
	ctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply privileged role bootstrap to empty database: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply schema migrations as migration_role: %v", err)
	}

	for _, role := range []string{
		"schema_owner",
		"migration_role",
		"api_runtime_role",
		"worker_runtime_role",
		"audit_append_owner",
		"ops_readonly_role",
	} {
		var login, superuser, bypassRLS bool
		if err := db.QueryRowContext(ctx, `SELECT rolcanlogin, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = $1`, role).
			Scan(&login, &superuser, &bypassRLS); err != nil {
			t.Fatalf("role %q is missing: %v", role, err)
		}
		if login || superuser || bypassRLS {
			t.Errorf("role %q must be NOLOGIN, NOSUPERUSER, and NOBYPASSRLS; got login=%v superuser=%v bypassrls=%v", role, login, superuser, bypassRLS)
		}
	}

	rows, err := db.QueryContext(ctx, `
		SELECT n.nspname, c.relname, c.relrowsecurity, c.relforcerowsecurity,
		       EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE (n.nspname, c.relname) IN (
			('platform', 'role_bindings'), ('platform', 'source_registrations'),
			('platform', 'cluster_registrations'), ('platform', 'registry_versions'),
			('finding', 'records'), ('incident', 'records'),
			('investigation', 'jobs'), ('action', 'executions'), ('audit', 'records'), ('audit', 'signed_segments')
		)
		ORDER BY n.nspname, c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var schema, table string
		var rls, forceRLS, tenantID bool
		if err := rows.Scan(&schema, &table, &rls, &forceRLS, &tenantID); err != nil {
			t.Fatal(err)
		}
		key := schema + "." + table
		seen[key] = true
		if !tenantID || !rls || !forceRLS {
			t.Errorf("%s must have tenant_id and ENABLE/FORCE ROW LEVEL SECURITY", key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"platform.role_bindings", "platform.source_registrations", "platform.cluster_registrations",
		"platform.registry_versions", "finding.records", "incident.records",
		"investigation.jobs", "action.executions", "audit.records", "audit.signed_segments",
	} {
		if !seen[key] {
			t.Errorf("required tenant table %s does not exist", key)
		}
	}

	var badForeignKeys int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE c.contype = 'f'
		  AND n.nspname IN ('platform', 'finding', 'incident', 'investigation', 'action', 'audit')
		  AND position('tenant_id' in pg_get_constraintdef(c.oid)) = 0`).Scan(&badForeignKeys); err != nil {
		t.Fatal(err)
	}
	if badForeignKeys != 0 {
		t.Errorf("%d tenant-local foreign keys omit tenant_id", badForeignKeys)
	}
	var unsafeBusinessTables, nonCompositeBusinessForeignKeys, rlsMissing int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname IN ('platform', 'finding', 'incident', 'investigation', 'action', 'audit')
		  AND c.relname <> 'tenants'
		  AND NOT EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped)`).
		Scan(&unsafeBusinessTables); err != nil {
		t.Fatal(err)
	}
	if unsafeBusinessTables != 0 {
		t.Errorf("%d business tables omit tenant_id", unsafeBusinessTables)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM pg_constraint c
		JOIN pg_class child ON child.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = child.relnamespace
		JOIN pg_class parent ON parent.oid = c.confrelid
		JOIN pg_namespace pn ON pn.oid = parent.relnamespace
		WHERE c.contype = 'f' AND n.nspname IN ('platform', 'finding', 'incident', 'investigation', 'action', 'audit')
		  AND NOT (pn.nspname = 'platform' AND parent.relname = 'tenants')
		  AND (cardinality(c.conkey) < 2 OR cardinality(c.confkey) < 2)`).
		Scan(&nonCompositeBusinessForeignKeys); err != nil {
		t.Fatal(err)
	}
	if nonCompositeBusinessForeignKeys != 0 {
		t.Errorf("%d tenant-local foreign keys are not tenant-composite", nonCompositeBusinessForeignKeys)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname IN ('platform', 'finding', 'incident', 'investigation', 'action', 'audit')
		  AND c.relname <> 'tenants'
		  AND (NOT c.relrowsecurity OR NOT c.relforcerowsecurity)`).Scan(&rlsMissing); err != nil {
		t.Fatal(err)
	}
	if rlsMissing != 0 {
		t.Errorf("%d business tables lack ENABLE/FORCE RLS", rlsMissing)
	}

	var updateAllowed, deleteAllowed bool
	if err := db.QueryRowContext(ctx, `
	SELECT has_table_privilege('api_runtime_role', 'audit.records', 'UPDATE'),
	       has_table_privilege('worker_runtime_role', 'audit.records', 'DELETE')`).
		Scan(&updateAllowed, &deleteAllowed); err != nil {
		t.Fatal(err)
	}
	if updateAllowed || deleteAllowed {
		t.Errorf("runtime roles must not mutate audit records: update=%v delete=%v", updateAllowed, deleteAllowed)
	}
	var apiCanReadWorkerQueue, workerCanReadExecution bool
	if err := db.QueryRowContext(ctx, `
		SELECT has_table_privilege('api_runtime_role', 'investigation.worker_queue', 'SELECT'),
		       has_table_privilege('worker_runtime_role', 'action.executions', 'SELECT')`).
		Scan(&apiCanReadWorkerQueue, &workerCanReadExecution); err != nil {
		t.Fatal(err)
	}
	if apiCanReadWorkerQueue || workerCanReadExecution {
		t.Errorf("runtime roles crossed table boundary: api_worker_queue=%v worker_execution=%v", apiCanReadWorkerQueue, workerCanReadExecution)
	}
	var apiCanCreate, workerCanCreate bool
	if err := db.QueryRowContext(ctx, `
		SELECT has_schema_privilege('api_runtime_role', 'platform', 'CREATE'),
		       has_schema_privilege('worker_runtime_role', 'platform', 'CREATE')`).
		Scan(&apiCanCreate, &workerCanCreate); err != nil {
		t.Fatal(err)
	}
	if apiCanCreate || workerCanCreate {
		t.Errorf("runtime role received schema DDL: api=%v worker=%v", apiCanCreate, workerCanCreate)
	}
}

func TestMigrationsUpgradeVersionOneData(t *testing.T) {
	ctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply first migration: %v", err)
	}
	tenantID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987201")
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, 'legacy', 'Legacy Tenant')`, tenantID); err != nil {
		t.Fatalf("seed version-one tenant: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("upgrade database containing version-one data as migration_role: %v", err)
	}
	var slug, name string
	if err := db.QueryRowContext(ctx, `SELECT slug, display_name FROM platform.tenants WHERE tenant_id = $1`, tenantID).Scan(&slug, &name); err != nil {
		t.Fatalf("read preserved version-one tenant: %v", err)
	}
	if slug != "legacy" || name != "Legacy Tenant" {
		t.Fatalf("version-one data changed during upgrade: slug=%q name=%q", slug, name)
	}
}

func TestWithTenantTxIsolatesTenantsAndUsesAppendOnlyAudit(t *testing.T) {
	ctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply privileged role bootstrap: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply schema migrations as migration_role: %v", err)
	}
	tenantA := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987201")
	tenantB := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987202")
	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, $2, $2)`, tenantID, tenantID.String()); err != nil {
			t.Fatalf("create test tenant: %v", err)
		}
	}

	poolConfig := runtimePoolConfig(t, ctx, db, dbURL, "api_runtime_role")
	poolConfig.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	bindingA, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name) VALUES ($1, $2, 'subject-a', 'operator')`, tenantA, bindingA)
		return err
	}); err != nil {
		t.Fatalf("insert tenant A row: %v", err)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantB, func(tx pgx.Tx) error {
		var visible int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM platform.role_bindings`).Scan(&visible); err != nil {
			return err
		}
		if visible != 0 {
			return fmt.Errorf("tenant B can see %d tenant A rows", visible)
		}
		if _, err := tx.Exec(ctx, `SAVEPOINT tenant_spoof_check`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name) VALUES ($1, $2, 'spoofed', 'operator')`, tenantA, uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987203"))
		if err == nil {
			return fmt.Errorf("tenant B inserted a tenant A row")
		}
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT tenant_spoof_check`); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("cross-tenant RLS check: %v", err)
	}

	var residualTenant string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(current_setting('app.tenant_id', true), '')`).Scan(&residualTenant); err != nil {
		t.Fatal(err)
	}
	if residualTenant != "" {
		t.Fatalf("tenant setting leaked after connection pool reuse: %q", residualTenant)
	}

	auditID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	var functionOwner string
	var canAppend, canUseAudit bool
	if err := db.QueryRowContext(ctx, `
		SELECT pg_get_userbyid(p.proowner), has_table_privilege('audit_append_owner', 'audit.records', 'INSERT'),
		       has_schema_privilege('audit_append_owner', 'audit', 'USAGE')
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'audit' AND p.proname = 'append_record'`).Scan(&functionOwner, &canAppend, &canUseAudit); err != nil {
		t.Fatal(err)
	}
	if functionOwner != "audit_append_owner" || !canAppend || !canUseAudit {
		t.Fatalf("append function privileges are not least-privilege: owner=%q insert=%v schema_usage=%v", functionOwner, canAppend, canUseAudit)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		var auditSeq, tenantSeq int64
		return tx.QueryRow(ctx, `SELECT audit_seq, tenant_seq FROM audit.append_record($1, $2, 'sp03.test', 'role_binding', $3, 'subject-a', '{"action":"create"}'::jsonb, $4)`, tenantA, auditID, bindingA, make([]byte, 32)).Scan(&auditSeq, &tenantSeq)
	}); err != nil {
		t.Fatalf("append audit record through fixed function: %v", err)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit.records WHERE record_id = $1`, auditID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("expected one appended audit record, got %d", count)
		}
		for index, query := range []string{
			`UPDATE audit.records SET subject = 'rewritten' WHERE record_id = $1`,
			`DELETE FROM audit.records WHERE record_id = $1`,
			`INSERT INTO audit.records (tenant_id, tenant_seq, record_id, event_type, entity_kind, subject, record, canonical_digest) VALUES ($1, 100, $2, 'forged', 'test', 'subject-a', '{}'::jsonb, $3)`,
		} {
			if _, err := tx.Exec(ctx, `SAVEPOINT audit_mutation_check`); err != nil {
				return err
			}
			var execErr error
			switch index {
			case 0, 1:
				_, execErr = tx.Exec(ctx, query, auditID)
			case 2:
				_, execErr = tx.Exec(ctx, query, tenantA, uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987204"), make([]byte, 32))
			}
			if execErr == nil {
				return fmt.Errorf("runtime role unexpectedly succeeded with audit mutation query %q", query)
			}
			if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT audit_mutation_check`); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("audit append-only check: %v", err)
	}
}

func runRemainingMigrationsAsMigrationRole(t *testing.T, ctx context.Context, adminDB *sql.DB, adminURL, migrationDir string) error {
	t.Helper()
	roleName := "sp03_migrator_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.ExecContext(ctx, `CREATE ROLE "`+roleName+`" LOGIN`); err != nil {
		return fmt.Errorf("create isolated migration login: %w", err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.ExecContext(context.Background(), `DROP ROLE IF EXISTS "`+roleName+`"`)
	})
	if _, err := adminDB.ExecContext(ctx, `GRANT migration_role TO "`+roleName+`"`); err != nil {
		return fmt.Errorf("grant migration role to isolated login: %w", err)
	}

	parsed, err := url.Parse(adminURL)
	if err != nil {
		return err
	}
	parsed.User = url.User(roleName)
	query := parsed.Query()
	query.Set("options", "-c role=migration_role")
	parsed.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	migrator, err := sql.Open("pgx", parsed.String())
	if err != nil {
		return fmt.Errorf("open migration-role connection: %w", err)
	}
	defer migrator.Close()
	if err := migrator.PingContext(ctx); err != nil {
		return fmt.Errorf("connect as migration_role: %w", err)
	}
	var currentRole string
	if err := migrator.QueryRowContext(ctx, `SELECT current_user`).Scan(&currentRole); err != nil {
		return err
	}
	if currentRole != "migration_role" {
		return fmt.Errorf("migration connection runs as %q, expected migration_role", currentRole)
	}
	if err := goose.UpContext(ctx, migrator, migrationDir); err != nil {
		return err
	}
	return nil
}

func newMigrationDatabase(t *testing.T) (context.Context, *sql.DB, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	adminURL := os.Getenv("SP03_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("SP03_TEST_DATABASE_URL must point at a disposable PostgreSQL test instance")
	}
	admin, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("connect to disposable PostgreSQL: %v", err)
	}
	name := "sp03_mig_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE \""+name+"\""); err != nil {
		admin.Close()
		t.Fatalf("create isolated database: %v", err)
	}
	dbURL, err := databaseURLForDatabase(adminURL, name)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Fatalf("connect to isolated database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS \""+name+"\" WITH (FORCE)")
		_ = admin.Close()
	})
	goose.SetDialect("postgres")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return ctx, db, filepath.Clean(filepath.Join(wd, "../../migrations")), dbURL
}

func databaseURLForDatabase(raw, database string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse test database URL: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", fmt.Errorf("SP03_TEST_DATABASE_URL must use postgres:// or postgresql://")
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}
