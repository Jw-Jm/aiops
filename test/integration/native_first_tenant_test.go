package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"ops-platform/internal/identity"
)

// The native cold start must be able to create its first tenant and first
// administrator without OIDC, and only under the dedicated migration-duty LOGIN.
// This guarded test runs the real repository against a disposable database that
// applies migrations 1..60.
func TestNativeFirstTenantBootstrap(t *testing.T) {
	ctx, db, dir, dsn := newMigrationDatabaseWithTimeout(t, 10*time.Minute)
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `GRANT CREATE ON DATABASE "`+database+`" TO schema_owner`); err != nil {
		t.Fatalf("grant schema duty on the disposable database: %v", err)
	}
	goose.SetDialect("postgres")
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatalf("apply remaining migrations: %v", err)
	}
	bootstrapPool := nativeIdentityNewLoginPool(t, ctx, db, dsn, "bootstrap", "migration_role", "")
	defer bootstrapPool.Close()
	repository, err := identity.NewRepository(bootstrapPool)
	if err != nil {
		t.Fatal(err)
	}

	input := identity.FirstTenantInput{
		Slug: "primary", DisplayName: "Primary Tenant",
		AdminLoginName: "root-admin", AdminDisplayName: "Root Administrator",
		Password: "native-first-tenant-password",
	}
	result, err := repository.CreateFirstTenant(ctx, input)
	if err != nil {
		t.Fatalf("create first tenant: %v", err)
	}
	if result.TenantID == uuid.Nil || result.Administrator.UserID == [16]byte{} || result.Administrator.LoginName != "root-admin" {
		t.Fatalf("first tenant result = %+v", result)
	}
	var status, slug string
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT status,slug,revision FROM platform.tenants WHERE tenant_id=$1`, result.TenantID).Scan(&status, &slug, &revision); err != nil {
		t.Fatal(err)
	}
	if status != "active" || slug != "primary" || revision != 1 {
		t.Fatalf("tenant row = %s/%s/%d", status, slug, revision)
	}
	var accountStatus, passwordState string
	var mustChange bool
	if err := db.QueryRowContext(ctx, `SELECT status,password_state,must_change_password FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, result.TenantID, result.Administrator.UserID).Scan(&accountStatus, &passwordState, &mustChange); err != nil {
		t.Fatal(err)
	}
	if accountStatus != "active" || passwordState != "change_required" || !mustChange {
		t.Fatalf("first account = %s/%s/%v", accountStatus, passwordState, mustChange)
	}
	var role, grantStatus string
	if err := db.QueryRowContext(ctx, `SELECT role,status FROM identity.role_grants WHERE tenant_id=$1 AND user_id=$2`, result.TenantID, result.Administrator.UserID).Scan(&role, &grantStatus); err != nil {
		t.Fatal(err)
	}
	if role != "platform_admin" || grantStatus != "active" {
		t.Fatalf("first grant = %s/%s", role, grantStatus)
	}
	var markers, tenantAudits, adminAudits int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM identity.administrator_initializations WHERE tenant_id=$1`, result.TenantID).Scan(&markers); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='identity.first_tenant.initialized'`, result.TenantID).Scan(&tenantAudits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='identity.first_administrator.initialized'`, result.TenantID).Scan(&adminAudits); err != nil {
		t.Fatal(err)
	}
	if markers != 1 || tenantAudits != 1 || adminAudits != 1 {
		t.Fatalf("bootstrap markers=%d tenantAudits=%d adminAudits=%d", markers, tenantAudits, adminAudits)
	}

	// The initial credential is usable by the login lookup and still requires the
	// first password change; no OIDC realm, issuer or token was involved.
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	sessions, err := identity.NewSessionRepository(apiPool)
	if err != nil {
		t.Fatal(err)
	}
	account, err := sessions.FindLoginAccount(ctx, "root-admin")
	if err != nil {
		t.Fatalf("login lookup: %v", err)
	}
	if account.TenantID != result.TenantID || account.PasswordState != identity.PasswordChangeRequired || account.Status != "active" {
		t.Fatalf("login account = %+v", account)
	}

	// Repeating the bootstrap never adopts or rewrites the existing tenant.
	if _, err := repository.CreateFirstTenant(ctx, input); !errors.Is(err, identity.ErrTenantExists) {
		t.Fatalf("repeat first tenant error = %v", err)
	}
	var tenants int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform.tenants`).Scan(&tenants); err != nil {
		t.Fatal(err)
	}
	if tenants != 1 {
		t.Fatalf("repeat bootstrap left %d tenants", tenants)
	}

	// An ordinary API login cannot create a tenant.
	apiRepository, err := identity.NewRepository(apiPool)
	if err != nil {
		t.Fatal(err)
	}
	other := input
	other.Slug = "second"
	if _, err := apiRepository.CreateFirstTenant(ctx, other); !errors.Is(err, identity.ErrBootstrapDuty) {
		t.Fatalf("api role first tenant error = %v", err)
	}

	// Concurrent identical bootstraps are linearizable: exactly one wins and the
	// loser leaves no partial tenant.
	concurrent := input
	concurrent.Slug = "concurrent"
	concurrent.AdminLoginName = "concurrent-admin"
	var wg sync.WaitGroup
	errs := make([]error, 2)
	results := make([]identity.FirstTenantResult, 2)
	for index := 0; index < 2; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = repository.CreateFirstTenant(context.Background(), concurrent)
		}(index)
	}
	wg.Wait()
	successes := 0
	for index, err := range errs {
		switch {
		case err == nil:
			successes++
			if results[index].TenantID == uuid.Nil {
				t.Fatalf("concurrent winner returned no tenant")
			}
		case errors.Is(err, identity.ErrTenantExists):
		default:
			t.Fatalf("concurrent bootstrap[%d] error = %v", index, err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent bootstrap succeeded %d times", successes)
	}
	var concurrentTenants int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform.tenants WHERE slug='concurrent'`).Scan(&concurrentTenants); err != nil {
		t.Fatal(err)
	}
	if concurrentTenants != 1 {
		t.Fatalf("concurrent bootstrap left %d tenants", concurrentTenants)
	}
}
