package integration

// P2-01 native identity database boundary test.
//
// This test is opt-in and task-owned. It never reuses the generic migration
// helper to select the target instance. Before any mutation it requires exact
// task metadata, a loopback client port, exact database name/owner/comment, and
// a non-superuser CREATEROLE owner. It creates NOINHERIT LOGINs and exercises
// the exact transaction duty path (SET LOCAL ROLE migration_role). A default
// skip is explicitly a skip, not a pass.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/identity"
	"ops-platform/internal/persistence"
)

const (
	nativeIdentityRunEnv       = "OPS_UI_V12_NATIVE_IDENTITY_DBTEST"
	nativeIdentityTaskEnv      = "OPS_UI_V12_NATIVE_IDENTITY_TASK"
	nativeIdentityTask         = "ui-v12-p2-01-r1"
	nativeIdentityDSNEnv       = "OPS_UI_V12_NATIVE_IDENTITY_ADMIN_DSN"
	nativeIdentityPortEnv      = "OPS_UI_V12_NATIVE_IDENTITY_DB_PORT"
	nativeIdentityBootstrapEnv = "OPS_UI_V12_NATIVE_IDENTITY_BOOTSTRAP_DSN"

	nativeIdentityDBHost   = "127.0.0.1"
	nativeIdentityDBName   = "ui_v12_native_identity"
	nativeIdentityDBUser   = "ui_v12_native_owner"
	nativeIdentityComment  = "ops.task=ui-v12-p2-01-r1; isolated native identity only"
	nativeIdentityMinPGNum = 150000
)

type nativeIdentityGuard struct {
	ctx           context.Context
	adminDB       *sql.DB
	adminURL      string
	migrationDir  string
	serverAddress string
}

func TestNativeIdentityGuardRejectsSharedOrOverriddenDSN(t *testing.T) {
	base := "postgres://" + nativeIdentityDBUser + ":password@" + nativeIdentityDBHost + ":15450/" + nativeIdentityDBName + "?sslmode=disable"
	if _, err := nativeIdentityValidateAdminDSN(base, "15450"); err != nil {
		t.Fatalf("valid task DSN rejected: %v", err)
	}
	cases := map[string]string{
		"wrong host":      strings.Replace(base, "@"+nativeIdentityDBHost, "@10.0.0.8", 1),
		"wrong database":  strings.Replace(base, "/"+nativeIdentityDBName, "/postgres", 1),
		"wrong user":      strings.Replace(base, nativeIdentityDBUser+":", "postgres:", 1),
		"host override":   base + "&hostaddr=10.0.0.8",
		"role override":   base + "&options=-c%20role=migration_role",
		"missing sslmode": strings.Replace(base, "?sslmode=disable", "", 1),
		"fragment":        base + "#shared",
		"no password":     strings.Replace(base, ":password@", "@", 1),
	}
	for name, dsn := range cases {
		if _, err := nativeIdentityValidateAdminDSN(dsn, "15450"); err == nil {
			t.Fatalf("%s DSN was accepted", name)
		}
	}
}

func TestNativeIdentityRepositoryAndRoles(t *testing.T) {
	if os.Getenv(nativeIdentityRunEnv) != "1" {
		t.Skipf("%s=1 is required; a default skip is not a database pass", nativeIdentityRunEnv)
	}
	if os.Getenv(nativeIdentityTaskEnv) != nativeIdentityTask {
		t.Fatalf("%s must be exactly %q", nativeIdentityTaskEnv, nativeIdentityTask)
	}
	port := os.Getenv(nativeIdentityPortEnv)
	rawDSN := os.Getenv(nativeIdentityDSNEnv)
	dsn, err := nativeIdentityValidateAdminDSN(rawDSN, port)
	if err != nil {
		t.Fatalf("task-owned DSN guard rejected before any mutation: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	guard, err := nativeIdentityProbeDatabase(ctx, adminDB)
	if err != nil {
		t.Fatalf("task-owned database guard failed before mutation: %v", err)
	}
	_ = guard

	// PostgreSQL 17 role attributes in migration 1 require the instance
	// bootstrap identity. This identity is used only for migration 1 and the
	// one-time setup grants; all later migrations run as NOINHERIT migration
	// LOGIN, and runtime/API/Worker roles never receive it.
	bootstrapDSN, err := nativeIdentityValidateBootstrapDSN(
		os.Getenv(nativeIdentityBootstrapEnv), port,
	)
	if err != nil {
		t.Fatalf("instance bootstrap DSN guard failed: %v", err)
	}
	bootstrapDB, err := sql.Open("pgx", bootstrapDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrapDB.Close()
	if err := bootstrapDB.PingContext(ctx); err != nil {
		t.Fatalf("instance bootstrap ping: %v", err)
	}
	migrationDir := "../../migrations"
	goose.SetDialect("postgres")
	if err := goose.UpToContext(ctx, bootstrapDB, migrationDir, 1); err != nil {
		t.Fatalf("apply role bootstrap: %v", err)
	}
	if _, err := bootstrapDB.ExecContext(ctx, `
		GRANT CREATE ON DATABASE ui_v12_native_identity TO schema_owner;
		GRANT schema_owner TO ui_v12_native_owner WITH ADMIN OPTION;
		GRANT migration_role TO ui_v12_native_owner WITH ADMIN OPTION;
	`); err != nil {
		t.Fatalf("grant controlled setup duties: %v", err)
	}
	migrator := newNativeIdentityMigrator(t, ctx, bootstrapDB, dsn, migrationDir)
	defer migrator.Close()
	if err := goose.UpContext(ctx, migrator, migrationDir); err != nil {
		t.Fatalf("apply native identity migrations: %v", err)
	}
	nativeIdentityAssertMigrationsAndRoles(t, ctx, adminDB, migrator)

	tenantA, tenantB := nativeIdentityCreateTenants(t, ctx, adminDB)
	bootstrapPool := nativeIdentityNewLoginPool(t, ctx, adminDB, dsn,
		"bootstrap", "migration_role", "")
	defer bootstrapPool.Close()
	apiPool := nativeIdentityNewLoginPool(t, ctx, bootstrapDB, dsn,
		"api", "api_runtime_role", "api_runtime_role")
	defer apiPool.Close()
	workerPool := nativeIdentityNewLoginPool(t, ctx, bootstrapDB, dsn,
		"worker", "worker_runtime_role", "worker_runtime_role")
	defer workerPool.Close()

	if err := identity.RequireNativeBootstrapDuty(ctx, bootstrapPool); err != nil {
		t.Fatalf("NOINHERIT migration-duty LOGIN failed exact CLI duty check: %v", err)
	}
	for name, pool := range map[string]*pgxpool.Pool{"api": apiPool, "worker": workerPool} {
		if err := identity.RequireNativeBootstrapDuty(ctx, pool); err == nil {
			t.Fatalf("%s LOGIN passed bootstrap duty check", name)
		}
	}
	bootstrapRepository, err := identity.NewRepository(bootstrapPool)
	if err != nil {
		t.Fatal(err)
	}
	for name, pool := range map[string]*pgxpool.Pool{"api": apiPool, "worker": workerPool} {
		repository, repositoryErr := identity.NewRepository(pool)
		if repositoryErr != nil {
			t.Fatal(repositoryErr)
		}
		if _, err := repository.CreateFirstAdministrator(ctx, identity.FirstAdministratorInput{
			TenantID: tenantA, LoginName: "denied.admin", DisplayName: "Denied Administrator",
			Password: "native-initial-password",
		}); !errors.Is(err, identity.ErrBootstrapDuty) {
			t.Fatalf("%s bootstrap error = %v, want ErrBootstrapDuty", name, err)
		}
	}

	// Each run uses a fresh globally unique login so the concurrency race is
	// meaningful even when a previous round left its successful fixture in place.
	testLogin := "first.admin." + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	input := identity.FirstAdministratorInput{
		TenantID: tenantA, LoginName: testLogin,
		DisplayName: "Native First Administrator",
		Password:    "native-initial-password",
	}
	results := make([]identity.FirstAdministratorResult, 2)
	errs := make([]error, 2)
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	for index := range results {
		go func(index int) {
			defer done.Done()
			ready.Done()
			ready.Wait()
			results[index], errs[index] = bootstrapRepository.CreateFirstAdministrator(ctx, input)
		}(index)
	}
	done.Wait()
	successes := 0
	var winner identity.FirstAdministratorResult
	for index := range errs {
		switch {
		case errs[index] == nil:
			successes++
			winner = results[index]
		case errors.Is(errs[index], identity.ErrAlreadyInitialized),
			errors.Is(errs[index], identity.ErrLoginNameUnavailable):
		default:
			t.Fatalf("initialization race[%d] failed unexpectedly: %v", index, errs[index])
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent initialization succeeded %d times, want 1", successes)
	}
	if _, err := bootstrapRepository.CreateFirstAdministrator(ctx, input); !errors.Is(err, identity.ErrAlreadyInitialized) {
		t.Fatalf("repeat initialization error = %v, want ErrAlreadyInitialized", err)
	}
	tenantBInput := input
	tenantBInput.TenantID = tenantB
	if _, err := bootstrapRepository.CreateFirstAdministrator(ctx, tenantBInput); !errors.Is(err, identity.ErrLoginNameUnavailable) {
		t.Fatalf("cross-tenant login reuse error = %v, want ErrLoginNameUnavailable", err)
	}
	nativeIdentityAssertCompositeAccountForeignKeys(t, ctx, adminDB)

	// A tenant-local row with a foreign account UUID must fail at the FK, and a
	// row claiming tenant A under tenant B must fail RLS before it can persist.
	if err := persistence.WithTenantTx(ctx, bootstrapPool, tenantB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO identity.credentials
				(tenant_id,user_id,credential_version,password_hash,status)
			VALUES ($1,$2,1,$3,'active')
		`, tenantB, winner.UserID, winner.CredentialVersion)
		return err
	}); err == nil {
		t.Fatal("tenant B attached a credential to tenant A account")
	} else if !nativeIdentityIsForeignKeyOrRLSError(err) {
		t.Fatalf("cross-account credential rejected for an unexpected reason: %v", err)
	}
	if err := persistence.WithTenantTx(ctx, bootstrapPool, tenantB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO identity.sessions
				(tenant_id,session_id,user_id,mode,token_digest,csrf_digest,
				 issued_credential_version,issued_authorization_version,issued_at,last_seen_at,
				 idle_expires_at,absolute_expires_at)
			VALUES ($1,$2,$3,'full',gen_random_bytes(32),gen_random_bytes(32),1,1,
			        clock_timestamp(),clock_timestamp(),
			        clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '8 hours')
		`, tenantA, uuid.New(), winner.UserID)
		return err
	}); err == nil {
		t.Fatal("tenant B context wrote a tenant A session")
	} else if !nativeIdentityIsForeignKeyOrRLSError(err) {
		t.Fatalf("cross-tenant session rejected for an unexpected reason: %v", err)
	}

	apiRepository, err := identity.NewRepository(apiPool)
	if err != nil {
		t.Fatal(err)
	}
	var directCount int
	if err := apiPool.QueryRow(ctx, `SELECT count(*) FROM identity.accounts`).Scan(&directCount); err == nil {
		t.Fatal("API LOGIN directly read identity.accounts")
	}
	if err := apiPool.QueryRow(ctx, `SELECT count(*) FROM identity.credentials`).Scan(&directCount); err == nil {
		t.Fatal("API LOGIN directly read identity.credentials")
	}
	// A spoofed pre-auth GUC is not table authorization and cannot become the
	// bootstrap duty. Every security-definer function resets it first.
	if _, err := apiPool.Exec(ctx,
		`SELECT set_config('app.identity_preauth','find_login_account',true),
		        set_config('app.tenant_id',$1,true)`, winner.TenantID.String()); err != nil {
		t.Fatal(err)
	}
	if err := apiPool.QueryRow(ctx, `SELECT count(*) FROM identity.accounts`).Scan(&directCount); err == nil {
		t.Fatal("spoofed pre-auth GUC allowed API direct identity read")
	}
	if _, err := apiPool.Exec(ctx, `SET ROLE schema_owner`); err == nil {
		t.Fatal("API LOGIN switched to schema_owner")
	}
	if _, err := apiPool.Exec(ctx,
		`SELECT set_config('app.identity_preauth','disabled',true),
		        set_config('app.tenant_id','',true)`); err != nil {
		t.Fatal(err)
	}
	account, err := apiRepository.FindLoginAccount(ctx, " "+testLogin+" ")
	if err != nil {
		t.Fatalf("restricted pre-auth lookup failed: %v", err)
	}
	if account.UserID != winner.UserID || account.PasswordHash == "" ||
		!account.MustChangePassword || account.PasswordState != identity.PasswordChangeRequired {
		t.Fatalf("restricted lookup returned wrong account: %+v", account)
	}

	// Failure state is bound to the real account and durable.
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := apiRepository.RecordPasswordFailure(ctx, tenantA, winner.UserID, &now, nil); err != nil {
		t.Fatalf("record first failure: %v", err)
	}
	if _, err := apiRepository.FindLoginAccount(ctx, testLogin); err != nil {
		t.Fatal(err)
	}
	if err := apiRepository.ClearPasswordFailure(ctx, tenantA, winner.UserID); err != nil {
		t.Fatalf("clear unlocked failure: %v", err)
	}
	if _, err := apiRepository.FindLoginAccount(ctx, testLogin); err != nil {
		t.Fatal(err)
	}
	future := now.Add(time.Minute)
	if err := apiRepository.RecordPasswordFailure(ctx, tenantA, winner.UserID, &now, &future); err != nil {
		t.Fatalf("record locked failure: %v", err)
	}
	if err := apiRepository.RecordPasswordFailure(ctx, tenantA, winner.UserID, &now, nil); err != nil {
		t.Fatalf("record later failure without lowering lock: %v", err)
	}
	account, err = apiRepository.FindLoginAccount(ctx, testLogin)
	if err != nil {
		t.Fatal(err)
	}
	if account.LockedUntil == nil || !account.LockedUntil.Equal(future) {
		t.Fatalf("later failure lowered lock: %v", account.LockedUntil)
	}
	if err := apiRepository.ClearPasswordFailure(ctx, tenantA, winner.UserID); err != nil {
		t.Fatalf("clear locked failure returned: %v", err)
	}
	account, err = apiRepository.FindLoginAccount(ctx, testLogin)
	if err != nil {
		t.Fatal(err)
	}
	if account.LockedUntil == nil || !account.LockedUntil.Equal(future) {
		t.Fatalf("clear bypassed a valid lock: %v", account.LockedUntil)
	}

	// Verification reads use the isolated instance bootstrap identity. This is
	// not a runtime grant and does not bypass the production paths under test.
	var auditCount int
	if err := bootstrapDB.QueryRowContext(ctx,
		`SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='identity.first_administrator.initialized'`,
		tenantA).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("first administrator audit records = %d, want 1", auditCount)
	}
	var adminCount, operatorCount int
	if err := bootstrapDB.QueryRowContext(ctx,
		`SELECT count(*) FILTER (WHERE role='platform_admin' AND status='active'),
		        count(*) FILTER (WHERE role='operator' AND status='active')
		 FROM identity.role_grants WHERE tenant_id=$1`, tenantA).
		Scan(&adminCount, &operatorCount); err != nil {
		t.Fatal(err)
	}
	if adminCount != 1 || operatorCount != 0 {
		t.Fatalf("first administrator grants admin=%d operator=%d", adminCount, operatorCount)
	}
}

func nativeIdentityValidateAdminDSN(rawDSN, wantPort string) (string, error) {
	if rawDSN == "" || wantPort == "" {
		return "", fmt.Errorf("task DSN and %s are required", nativeIdentityPortEnv)
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		return "", errors.New("DSN is invalid")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", errors.New("DSN must use postgres:// or postgresql://")
	}
	if parsed.User == nil || parsed.User.Username() != nativeIdentityDBUser {
		return "", fmt.Errorf("DSN must use owner %q", nativeIdentityDBUser)
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword || password == "" {
		return "", errors.New("DSN must include a private password")
	}
	if parsed.Hostname() != nativeIdentityDBHost || parsed.Port() != wantPort {
		return "", fmt.Errorf("DSN must use loopback %s and the task-owned port %q", nativeIdentityDBHost, wantPort)
	}
	if parsed.Path != "/"+nativeIdentityDBName || parsed.RawPath != "" {
		return "", fmt.Errorf("DSN must target database %q", nativeIdentityDBName)
	}
	if parsed.Fragment != "" {
		return "", errors.New("DSN must not contain a fragment")
	}
	query := parsed.Query()
	for key, values := range query {
		if key != "sslmode" || len(values) != 1 {
			return "", fmt.Errorf("DSN must not override %q", key)
		}
	}
	if query.Get("sslmode") != "disable" {
		return "", errors.New("task loopback DSN must use sslmode=disable")
	}
	return rawDSN, nil
}

func nativeIdentityValidateBootstrapDSN(rawDSN, wantPort string) (string, error) {
	if rawDSN == "" || wantPort == "" {
		return "", fmt.Errorf("instance bootstrap DSN and %s are required", nativeIdentityPortEnv)
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		return "", errors.New("instance bootstrap DSN is invalid")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", errors.New("instance bootstrap DSN must use postgres:// or postgresql://")
	}
	if parsed.User == nil || parsed.User.Username() != "postgres" {
		return "", errors.New("instance bootstrap DSN must use the instance bootstrap postgres LOGIN")
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword || password == "" {
		return "", errors.New("instance bootstrap DSN must include a private password")
	}
	if parsed.Hostname() != nativeIdentityDBHost || parsed.Port() != wantPort {
		return "", errors.New("instance bootstrap DSN must use loopback and the task-owned port")
	}
	if parsed.Path != "/"+nativeIdentityDBName || parsed.RawPath != "" || parsed.Fragment != "" {
		return "", errors.New("instance bootstrap DSN must target the exact task database")
	}
	query := parsed.Query()
	for key, values := range query {
		if key != "sslmode" || len(values) != 1 {
			return "", fmt.Errorf("instance bootstrap DSN must not override %q", key)
		}
	}
	if query.Get("sslmode") != "disable" {
		return "", errors.New("instance bootstrap DSN must use sslmode=disable")
	}
	return rawDSN, nil
}

func nativeIdentityProbeDatabase(ctx context.Context, db *sql.DB) (map[string]string, error) {
	var database, sessionUser, currentUser, owner, comment, serverAddress string
	var serverPort int
	var serverVersion int
	var superuser, bypassRLS, createRole, createDB bool
	err := db.QueryRowContext(ctx, `
		SELECT current_database(), session_user, current_user,
		       pg_get_userbyid(d.datdba),
		       COALESCE(shobj_description(d.oid,'pg_database'),''),
		       host(inet_server_addr()), COALESCE(inet_server_port()::text,''),
		       current_setting('server_version_num')::int,
		       r.rolsuper, r.rolbypassrls, r.rolcreaterole, r.rolcreatedb
		FROM pg_database d
		JOIN pg_roles r ON r.rolname = session_user
		WHERE d.datname = current_database()
	`).Scan(&database, &sessionUser, &currentUser, &owner, &comment,
		&serverAddress, &serverPort, &serverVersion,
		&superuser, &bypassRLS, &createRole, &createDB)
	if err != nil {
		return nil, fmt.Errorf("probe task database: %w", err)
	}
	if database != nativeIdentityDBName || sessionUser != currentUser ||
		sessionUser != nativeIdentityDBUser || owner != nativeIdentityDBUser ||
		comment != nativeIdentityComment || serverPort != 5432 ||
		serverAddress == "" || serverVersion < nativeIdentityMinPGNum ||
		superuser || bypassRLS || !createRole || createDB {
		return nil, fmt.Errorf("task database guard failed: database=%q session=%q current=%q owner=%q comment=%q serverPort=%d version=%d",
			database, sessionUser, currentUser, owner, comment, serverPort, serverVersion)
	}
	return map[string]string{
		"database": database, "owner": owner, "comment": comment,
		"serverAddress": serverAddress, "serverPort": fmt.Sprint(serverPort),
	}, nil
}

func newNativeIdentityMigrator(t *testing.T, ctx context.Context, admin *sql.DB, adminURL, migrationDir string) *sql.DB {
	t.Helper()
	name, dsn := nativeIdentityCreateLogin(t, ctx, admin, adminURL, "migration", "migration_role", "migration_role")
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	migrator, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrator.Close() })
	if err := migrator.PingContext(ctx); err != nil {
		t.Fatalf("migration LOGIN ping: %v", err)
	}
	var sessionUser, currentUser string
	var inherit bool
	if err := pool.QueryRow(ctx, `
		SELECT session_user, current_user, r.rolinherit
		FROM pg_roles r WHERE r.rolname = session_user
	`).Scan(&sessionUser, &currentUser, &inherit); err != nil {
		t.Fatal(err)
	}
	if currentUser != "migration_role" || sessionUser == currentUser || inherit {
		t.Fatalf("migration connection role path invalid: session=%q current=%q inherit=%v", sessionUser, currentUser, inherit)
	}
	_ = name
	return migrator
}

func nativeIdentityCreateLogin(t *testing.T, ctx context.Context, admin *sql.DB, adminURL, label, capability, activeRole string) (string, string) {
	t.Helper()
	login := "native_id_" + label + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(
		`CREATE ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '%s'; GRANT %s TO %s`,
		pgx.Identifier{login}.Sanitize(), password,
		pgx.Identifier{capability}.Sanitize(), pgx.Identifier{login}.Sanitize())); err != nil {
		t.Fatalf("create %s LOGIN: %v", label, err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %s`, pgx.Identifier{login}.Sanitize()))
	})
	parsed, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(login, password)
	if activeRole != "" {
		query := parsed.Query()
		query.Set("options", "-c role="+activeRole)
		parsed.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	}
	return login, parsed.String()
}

func nativeIdentityNewLoginPool(t *testing.T, ctx context.Context, admin *sql.DB, adminURL, label, capability, activeRole string) *pgxpool.Pool {
	t.Helper()
	_, dsn := nativeIdentityCreateLogin(t, ctx, admin, adminURL, label, capability, activeRole)
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("%s LOGIN ping: %v", label, err)
	}
	var sessionUser, currentUser string
	var canLogin, superuser, inherit, bypass bool
	if err := pool.QueryRow(ctx, `
		SELECT session_user, current_user, r.rolcanlogin, r.rolsuper,
		       r.rolinherit, r.rolbypassrls
		FROM pg_roles r WHERE r.rolname = session_user
	`).Scan(&sessionUser, &currentUser, &canLogin, &superuser, &inherit, &bypass); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	if !canLogin || superuser || inherit || bypass {
		pool.Close()
		t.Fatalf("%s LOGIN is not NOINHERIT/NOSUPERUSER/NOBYPASSRLS", label)
	}
	if activeRole != "" && currentUser != activeRole {
		pool.Close()
		t.Fatalf("%s LOGIN current role = %q, want %q", label, currentUser, activeRole)
	}
	if activeRole == "" && sessionUser != currentUser {
		pool.Close()
		t.Fatalf("%s LOGIN unexpectedly changed current_user", label)
	}
	return pool
}

func nativeIdentityCreateTenants(t *testing.T, ctx context.Context, db *sql.DB) (uuid.UUID, uuid.UUID) {
	t.Helper()
	tenantA, tenantB := uuid.New(), uuid.New()
	tenants := []struct {
		id   uuid.UUID
		slug string
	}{
		{tenantA, "native-identity-a-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]},
		{tenantB, "native-identity-b-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]},
	}
	for _, tenant := range tenants {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE schema_owner`); err != nil {
			_ = tx.Rollback()
			t.Fatalf("setup role switch for synthetic tenant: %v", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,$2,$2)`,
			tenant.id, tenant.slug); err != nil {
			_ = tx.Rollback()
			t.Fatalf("create native identity tenant: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	return tenantA, tenantB
}

func nativeIdentityAssertMigrationsAndRoles(t *testing.T, ctx context.Context, admin *sql.DB, migrator *sql.DB) {
	t.Helper()
	var version int64
	if err := migrator.QueryRowContext(ctx, `SELECT max(version_id) FROM public.goose_db_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	migrations, err := goose.CollectMigrations("../../migrations", 0, goose.MaxVersion)
	if err != nil || len(migrations) == 0 {
		t.Fatalf("collect current migrations: %v", err)
	}
	if latest := migrations[len(migrations)-1].Version; version != latest {
		t.Fatalf("native identity schema version = %d, want %d", version, latest)
	}
	for _, role := range []string{"migration_role", "api_runtime_role", "worker_runtime_role"} {
		var login, superuser, inherit, bypass bool
		if err := admin.QueryRowContext(ctx,
			`SELECT rolcanlogin, rolsuper, rolinherit, rolbypassrls FROM pg_roles WHERE rolname=$1`, role).
			Scan(&login, &superuser, &inherit, &bypass); err != nil {
			t.Fatalf("capability role %q missing: %v", role, err)
		}
		if login || superuser || inherit || bypass {
			t.Fatalf("role %q must be NOLOGIN/NOSUPERUSER/NOINHERIT/NOBYPASSRLS", role)
		}
	}
	var migrationUsage, apiUsage bool
	if err := admin.QueryRowContext(ctx,
		`SELECT has_schema_privilege('migration_role','identity','USAGE'),
		        has_schema_privilege('api_runtime_role','identity','USAGE')`).
		Scan(&migrationUsage, &apiUsage); err != nil {
		t.Fatal(err)
	}
	if !migrationUsage || !apiUsage {
		t.Fatalf("identity schema USAGE migration=%v api=%v", migrationUsage, apiUsage)
	}
	for _, table := range []string{
		"identity.accounts", "identity.credentials", "identity.password_attempts",
		"identity.sessions", "identity.role_grants", "identity.subject_mappings",
		"identity.administrator_initializations",
	} {
		var tenantID, rls, forceRLS bool
		var apiSelect, apiInsert, workerSelect bool
		if err := admin.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attname='tenant_id' AND NOT a.attisdropped),
			       c.relrowsecurity, c.relforcerowsecurity,
			       has_table_privilege('api_runtime_role', c.oid, 'SELECT'),
			       has_table_privilege('api_runtime_role', c.oid, 'INSERT'),
			       has_table_privilege('worker_runtime_role', c.oid, 'SELECT')
			FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname='identity' AND c.relname=split_part($1,'.',2)
		`, table).Scan(&tenantID, &rls, &forceRLS, &apiSelect, &apiInsert, &workerSelect); err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if !tenantID || !rls || !forceRLS || apiSelect || apiInsert || workerSelect {
			t.Fatalf("%s isolation mismatch: tenant=%v rls=%v force=%v apiSelect=%v apiInsert=%v workerSelect=%v",
				table, tenantID, rls, forceRLS, apiSelect, apiInsert, workerSelect)
		}
	}
}

func nativeIdentityAssertCompositeAccountForeignKeys(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	tables := []string{
		"credentials", "password_attempts", "sessions", "role_grants",
		"subject_mappings", "administrator_initializations",
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM (
		SELECT c.conrelid
		FROM pg_constraint c
		JOIN pg_class parent ON parent.oid=c.confrelid
		JOIN pg_namespace pns ON pns.oid=parent.relnamespace
		JOIN pg_class child ON child.oid=c.conrelid
		JOIN pg_namespace cns ON cns.oid=child.relnamespace
		WHERE c.contype='f' AND pns.nspname='identity' AND parent.relname='accounts'
		  AND cns.nspname='identity'
		  AND child.relname IN ('credentials','password_attempts','sessions','role_grants','subject_mappings','administrator_initializations')
		  AND pg_get_constraintdef(c.oid) LIKE 'FOREIGN KEY (tenant_id, user_id)%'
		GROUP BY c.conrelid
	) foreign_keys`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(tables) {
		t.Fatalf("composite tenant/user account FKs = %d, want %d", count, len(tables))
	}
	var singleColumn int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class parent ON parent.oid=c.confrelid
		JOIN pg_namespace pns ON pns.oid=parent.relnamespace
		WHERE c.contype='f' AND pns.nspname='identity' AND parent.relname='accounts'
		  AND array_length(c.conkey,1)=1
	`).Scan(&singleColumn); err != nil {
		t.Fatal(err)
	}
	if singleColumn != 0 {
		t.Fatalf("found %d single-column identity account FKs", singleColumn)
	}
}

func nativeIdentityIsForeignKeyOrRLSError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503", "42501":
			return true
		}
	}
	return strings.Contains(err.Error(), "new row violates row-level security")
}
