package integration

import (
	"context"
	"crypto/sha256"
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

// historicalDownSections are the applied migrations that still carry a Goose Down
// section. Their bytes are frozen: the exact digest of every file is recorded
// here, so an edit of an applied migration is refused by the gate.
//
// The kind states what the Down section actually does:
//   - comments_only: the section documents that the migration is forward-only
//     and holds no statement at all.
//   - raise_exception_guard: the section actively blocks a rollback.
//   - drop_column / drop_read_function: the historical section really drops an
//     object. It is admitted only because the file is byte identical to the
//     applied migration and because the migration CLI has no Down path at all.
//
// A new or edited Down section is not admitted by this list.
var historicalDownSections = map[string]struct {
	kind   string
	digest string
}{
	"00038_sp08_safe_step_input_summary.sql":         {kind: "drop_column", digest: "114ed90f37b58b6bc83187c0793b2fee48d91a3545ed8c1e3a5e28d57358a05a"},
	"00047_native_authorization.sql":                 {kind: "comments_only", digest: "2a5a37693445be3b433b3f8ceeec945cc299cff5cdf4b061fae312027f032974"},
	"00048_native_authorization_found_fix.sql":       {kind: "comments_only", digest: "a0d050c80ef04a58dd3143d87faf817a7d4f0a8d2839826d3b527fe344cf9b32"},
	"00049_native_authorization_policy_restore.sql":  {kind: "comments_only", digest: "b0bd44a407c9eec9650d316f5f9270ce8b57cc638efe1a5fbaa9f214165128f8"},
	"00050_native_authorization_session_binding.sql": {kind: "comments_only", digest: "c8d3886c0988028eeffb1d2d151382c314829132959763ec8dc2cc560d393dfa"},
	"00051_native_mfa_stepup.sql":                    {kind: "comments_only", digest: "f166fd75335a31b22a7fbf297def86c8d078ea4ef5e2220d60c4c239e827526e"},
	"00052_native_mfa_lifecycle.sql":                 {kind: "comments_only", digest: "e45332919ba8b098628ab6c1a120ba93713822db07ee635684b05757d171f44d"},
	"00053_native_stepup_resolution.sql":             {kind: "comments_only", digest: "46f317e7fe2c64fc88f12db27d034974e6a5e2ed10305fff76babef862f7aab0"},
	"00054_resource_projection_revision.sql":         {kind: "comments_only", digest: "59e4c3895d31c61bfc31aa2954499e69ddd65384659f1089dd78a44911974eec"},
	"00055_native_user_administration.sql":           {kind: "raise_exception_guard", digest: "9bc7b4fa73ec59a4f94b18a5f4b22aa1d33eefdbc306facddf30f433d27c6166"},
	"00056_native_subject_inventory.sql":             {kind: "raise_exception_guard", digest: "5b9be139240eba257daf5efbed76cf984a41f75648e2ede44085239005c9cff0"},
	"00057_native_grant_scope_limits.sql":            {kind: "raise_exception_guard", digest: "4ae99de5dbf74d9937905c3c90c064f6e9b9347fb275bbb2b34571b2315ede4b"},
	"00058_native_historical_read.sql":               {kind: "drop_read_function", digest: "95aa520bfeac63dc2fa2bbf2a417cc27f351da9daa0ceff75c8b156e8c4ca031"},
	"00059_native_operator_job_authority.sql":        {kind: "comments_only", digest: "26e462b26f055a754ed5def804e3a732f69c38b9b40337f26246826cf36402a9"},
	"00060_native_first_tenant.sql":                  {kind: "comments_only", digest: "fe696b1f3d3b16de4209a73efe6a39809e69b83ef90d26cb14f60cfe3dc49f97"},
	"00061_native_job_authority_locks.sql":           {kind: "comments_only", digest: "371743940c5fe6c49fd72aa26ad8817b89bc416d4448bede87a6d3469585f761"},
	"00062_action_native_authority.sql":              {kind: "comments_only", digest: "3dbdee6decd629532d371c8a67a4bc574b3777a65dde48df77d817eccfd3d5e1"},
	"00063_native_authority_share_locks.sql":         {kind: "comments_only", digest: "a1d42391f37dc1ef7927f03b2063e53e152a75124d08807b053c9d3db2032c6c"},
}

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
		// The applied migrations keep their exact historical bytes: a Down section
		// is only admitted when the file still hashes to the recorded digest of the
		// applied migration. A new or edited Down section is refused.
		historical, admitted := historicalDownSections[file.Name()]
		if admitted && fmt.Sprintf("%x", sha256.Sum256(source)) != historical.digest {
			t.Errorf("migration %s no longer matches its applied digest", file.Name())
		}
		if !strings.HasPrefix(text, "-- +goose Up\n") || (strings.Contains(text, "-- +goose Down") && !admitted) {
			t.Errorf("migration %s must provide only a forward Goose Up section", file.Name())
		}
		version++
	}
	if version == 1 {
		t.Fatal("no SQL migrations found")
	}
	// The delivery baseline has to name the real migration set.
	baseline, err := os.ReadFile("../../docs/release/delivery-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	latest := version - 1
	if !strings.Contains(string(baseline), fmt.Sprintf(`"latest": %d`, latest)) || !strings.Contains(string(baseline), fmt.Sprintf(`"count": %d`, latest)) {
		t.Errorf("the delivery baseline must name the real migration set: latest=%d count=%d", latest, latest)
	}
}

// The migration CLI has no Down path: it only moves forward, so a historical
// Down section is never executed by the product.
func TestMigrationCLIHasNoDownPath(t *testing.T) {
	source, err := os.ReadFile("../../cmd/db-migrate/main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{"goose.Down", "goose.DownTo", "goose.Reset", `"down"`, "rollback"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the migration CLI must not offer a Down path: %s", forbidden)
		}
	}
	if !strings.Contains(text, "goose.UpToContext") && !strings.Contains(text, "goose.UpContext") {
		t.Error("the migration CLI must migrate forward")
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
	var apiCanReadWorkerQueue, workerCanReadExecution, workerCanInsertExecution, workerCanDeleteExecution bool
	if err := db.QueryRowContext(ctx, `
		SELECT has_table_privilege('api_runtime_role', 'investigation.worker_queue', 'SELECT'),
		       has_table_privilege('worker_runtime_role', 'action.executions', 'SELECT'),
		       has_table_privilege('worker_runtime_role', 'action.executions', 'INSERT'),
		       has_table_privilege('worker_runtime_role', 'action.executions', 'DELETE')`).
		Scan(&apiCanReadWorkerQueue, &workerCanReadExecution, &workerCanInsertExecution, &workerCanDeleteExecution); err != nil {
		t.Fatal(err)
	}
	// SP07 grants the Worker read/update access for dispatch, never creation or deletion.
	if apiCanReadWorkerQueue || !workerCanReadExecution || workerCanInsertExecution || workerCanDeleteExecution {
		t.Errorf("runtime role boundary: api_worker_queue=%v worker_execution_read=%v insert=%v delete=%v", apiCanReadWorkerQueue, workerCanReadExecution, workerCanInsertExecution, workerCanDeleteExecution)
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
	password := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	if _, err := adminDB.ExecContext(ctx, `CREATE ROLE "`+roleName+`" LOGIN PASSWORD '`+password+`'`); err != nil {
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
	parsed.User = url.UserPassword(roleName, password)
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
	return newMigrationDatabaseWithTimeout(t, 90*time.Second)
}

func newMigrationDatabaseWithTimeout(t *testing.T, timeout time.Duration) (context.Context, *sql.DB, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
		check, stop := context.WithTimeout(context.Background(), 5*time.Second)
		retain, reason := integrationDatabaseRetention(check, db)
		stop()
		_ = db.Close()
		if retain {
			t.Logf("retained owned integration database=%s test=%s reason=%s; no automatic protected cleanup", name, t.Name(), reason)
		} else {
			check, stop := context.WithTimeout(context.Background(), 5*time.Second)
			// Active/unknown consumers also refuse deletion. Never terminate a
			// connection or bypass Evidence/Audit protection to clean a test.
			if _, err := admin.ExecContext(check, "DROP DATABASE IF EXISTS \""+name+"\""); err != nil {
				t.Logf("retained owned integration database=%s test=%s reason=empty-database-delete-not-confirmed", name, t.Name())
			}
			stop()
		}
		_ = admin.Close()
	})
	goose.SetDialect("postgres")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return ctx, db, filepath.Clean(filepath.Join(wd, "../../migrations")), dbURL
}

func integrationDatabaseRetention(ctx context.Context, db *sql.DB) (bool, string) {
	if db == nil {
		return true, "database-unavailable"
	}
	// Even an expired Evidence sample may support a retained Investigation,
	// Audit or Legal Hold. Tests do not make source-retention deletion decisions.
	for _, table := range []string{"audit.records", "audit.signed_segments", "platform.evidence_metadata", "platform.evidence_archive_intents", "platform.evidence_dependencies", "platform.evidence_retention_references"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			return true, "retention-state-unavailable"
		}
		if !exists {
			continue
		}
		var records bool
		// Only fixed implementation-owned identifiers enter this statement.
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+")").Scan(&records); err != nil {
			return true, "retention-state-unavailable"
		}
		if records {
			return true, table
		}
	}
	return false, "no-audit-or-evidence-records"
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
