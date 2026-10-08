package integration

import (
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/bootstrap"
)

func TestPreSP07FirstTenantBootstrapIsAtomicAndOneTime(t *testing.T) {
	if os.Getenv("SP03_TEST_DATABASE_URL") == "" {
		t.Fatal("actual isolated PostgreSQL required")
	}
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	api := runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role")
	low, err := pgx.ConnectConfig(ctx, api.ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer low.Close(ctx)
	first := bootstrap.FirstTenant{TenantID: uuid.New(), Slug: "first-installation", DisplayName: "First installation", AdminSubject: "verified-bootstrap-subject"}
	if _, err = bootstrap.ProvisionFirstTenant(ctx, low, first); !errors.Is(err, bootstrap.ErrIdentity) {
		t.Fatalf("runtime login performed bootstrap: %v", err)
	}
	one, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close(ctx)
	two, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close(ctx)
	second := first
	second.TenantID = uuid.New()
	second.Slug = "second-installation"
	errs := make(chan error, 2)
	var group sync.WaitGroup
	group.Go(func() { _, err := bootstrap.ProvisionFirstTenant(ctx, one, first); errs <- err })
	group.Go(func() { _, err := bootstrap.ProvisionFirstTenant(ctx, two, second); errs <- err })
	group.Wait()
	close(errs)
	success, blocked := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, bootstrap.ErrInitialized):
			blocked++
		default:
			t.Fatalf("bootstrap: %v", err)
		}
	}
	if success != 1 || blocked != 1 {
		t.Fatalf("concurrent initializations success=%d blocked=%d", success, blocked)
	}
	var tenants, bindings, audits int
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM platform.tenants),(SELECT count(*) FROM platform.role_bindings WHERE role_name='platform_admin' AND cluster_scopes='[]' AND namespace_scopes='[]'),(SELECT count(*) FROM audit.records WHERE event_type='installation.first_tenant_bootstrapped')`).Scan(&tenants, &bindings, &audits); err != nil {
		t.Fatal(err)
	}
	if tenants != 1 || bindings != 1 || audits != 1 {
		t.Fatalf("bootstrap atomicity/scope: %d %d %d", tenants, bindings, audits)
	}
	if _, err = bootstrap.ProvisionFirstTenant(ctx, one, first); !errors.Is(err, bootstrap.ErrInitialized) {
		t.Fatalf("existing DB accepted bootstrap: %v", err)
	}
	t.Log("actual forward migration, runtime bootstrap denial, concurrent one-time bootstrap, empty operator scopes and same-transaction Audit verified")
}
