package integration

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/app"
	"ops-platform/internal/auth"
	"ops-platform/internal/source"
)

// The Kubernetes registration contract must be identical to the collection
// model: create, update and rotation all reject a data scope that the collector
// would permanently fail on, instead of storing it and failing every later
// collection with source_scope_unverified.
func TestKubernetesSourceRegistrationMatchesCollectionScope(t *testing.T) {
	if testing.Short() || os.Getenv("SP03_TEST_DATABASE_URL") == "" {
		t.Skip("SP03_TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dbctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(dbctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply isolated PostgreSQL role bootstrap: %v", err)
	}
	var database string
	if err := db.QueryRowContext(dbctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(dbctx, `GRANT CREATE ON DATABASE "`+database+`" TO schema_owner`); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, dbctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply isolated platform migrations: %v", err)
	}
	tenantID := uuid.Must(uuid.NewV7())
	actor := auth.RequestContext{TenantID: tenantID, Subject: "k8s-scope-admin", Roles: []auth.Role{auth.PlatformAdmin}}
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, 'k8s-scope-a', 'K8s Scope A')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name) VALUES ($1, $2, 'k8s-scope-admin', 'platform_admin')`, tenantID, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatal(err)
	}
	login := "k8s_scope_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	if _, err := db.ExecContext(dbctx, `CREATE ROLE "`+login+`" LOGIN PASSWORD '`+password+`'; GRANT api_runtime_role TO "`+login+`"`); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(dbctx, `DROP ROLE "`+login+`"`)
	parsedURL, err := url.Parse(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	parsedURL.User = url.UserPassword(login, password)
	pool, err := app.OpenRuntimePool(dbctx, parsedURL.String(), "api_runtime_role")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service, err := source.NewService(pool, source.CredentialVerifierFunc(func(context.Context, string, source.SourceIdentity, source.FindingEnvelope) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := registerClusterInTenant(ctx, pool, service, actor, source.ClusterCommand{
		ClusterUID: "k8s-scope-cluster", DisplayName: "K8s Scope Cluster", APIEndpointRef: "openbao://kv/clusters/k8s-scope/endpoint",
		Distribution: "kubeadm", ActualVersions: map[string]string{"kubernetes": "v1.36.3"}, Capabilities: map[string]bool{"kubernetes": true},
	})
	if err != nil {
		t.Fatalf("register cluster: %v", err)
	}
	command := source.RegisterCommand{
		SourceType: "kubernetes", InstanceKey: "k8s-scope", ClusterID: &cluster.ClusterID,
		AuthRef: "openbao://kv/platform/sources/k8s-scope", AllowedSchemas: []string{"finding-envelope/v2"}, BackendLogicalID: "k8s-scope-backend",
		DataScopeMapping: source.DataScopeMapping{Scopes: map[string][]string{"cluster": {cluster.ClusterUID}, "namespace": {"ops-a"}}},
	}
	for name, mutate := range map[string]func(*source.RegisterCommand){
		"native tenant": func(c *source.RegisterCommand) { c.DataScopeMapping.NativeTenant = "account-a" },
		"no namespace": func(c *source.RegisterCommand) {
			c.DataScopeMapping.Scopes = map[string][]string{"cluster": {cluster.ClusterUID}}
		},
		"foreign cluster": func(c *source.RegisterCommand) { c.DataScopeMapping.Scopes["cluster"] = []string{"other-cluster"} },
		"no cluster id":   func(c *source.RegisterCommand) { c.ClusterID = nil },
		"account scope": func(c *source.RegisterCommand) {
			c.DataScopeMapping.Scopes["account"] = []string{"a"}
		},
	} {
		t.Run("register-"+strings.ReplaceAll(name, " ", "-"), func(t *testing.T) {
			invalid := command
			invalid.DataScopeMapping = source.DataScopeMapping{Scopes: cloneScopes(command.DataScopeMapping.Scopes)}
			mutate(&invalid)
			if _, err := registerSourceInTenant(ctx, pool, service, actor, invalid); err == nil {
				t.Fatal("incompatible Kubernetes data scope was registered")
			}
		})
	}
	registered, err := registerSourceInTenant(ctx, pool, service, actor, command)
	if err != nil {
		t.Fatalf("register compatible Kubernetes source: %v", err)
	}
	rotated, err := rotateSourceInTenant(ctx, pool, service, actor, registered.SourceID, source.CredentialRotationCommand{ExpectedRevision: registered.Revision, AuthRef: "openbao://kv/platform/sources/k8s-scope-v2"})
	if err != nil || rotated.CredentialRevision != registered.CredentialRevision+1 {
		t.Fatalf("rotate compatible Kubernetes source: %#v err=%v", rotated, err)
	}
	bad := source.DataScopeMapping{NativeTenant: "account-a", Scopes: map[string][]string{"cluster": {cluster.ClusterUID}, "namespace": {"ops-a"}}}
	if _, err := updateSourceInTenant(ctx, pool, service, actor, registered.SourceID, source.SourceUpdateCommand{ExpectedRevision: rotated.Revision, DataScopeMapping: &bad}); !errors.Is(err, source.ErrScopeIncompatible) {
		t.Fatalf("update accepted a native tenant for Kubernetes: %v", err)
	}

	// An existing stored declaration from before this rule is presented as an
	// incompatibility and the update path repairs it. Rotation must not promote
	// it back to active.
	legacyID := uuid.Must(uuid.NewV7())
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.source_registrations
		(tenant_id, source_id, cluster_id, source_type, instance_key, auth_ref, allowed_schemas, backend_logical_id, data_scope_mapping)
		VALUES ($1,$2,$3,'kubernetes','k8s-legacy','openbao://kv/platform/sources/k8s-legacy',ARRAY['finding-envelope/v2'],'k8s-legacy-backend',$4)`,
		tenantID, legacyID, cluster.ClusterID, []byte(`{"nativeTenant":"account-a","scopes":{"cluster":["`+cluster.ClusterUID+`"],"namespace":["ops-a"]}}`)); err != nil {
		t.Fatal(err)
	}
	legacy, err := listSourceForTest(ctx, pool, actor, service, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ScopeSupported() {
		t.Fatal("legacy incompatible declaration presented as supported")
	}
	disabled, err := updateSourceInTenant(ctx, pool, service, actor, legacyID, source.SourceUpdateCommand{ExpectedRevision: legacy.Revision, Status: "disabled"})
	if err != nil || disabled.Status != "disabled" || disabled.ScopeSupported() {
		t.Fatalf("status-only disable must remain possible without inventing compatible scope: %v", err)
	}
	if _, err := rotateSourceInTenant(ctx, pool, service, actor, legacyID, source.CredentialRotationCommand{ExpectedRevision: disabled.Revision, AuthRef: "openbao://kv/platform/sources/k8s-legacy-v2"}); !errors.Is(err, source.ErrScopeIncompatible) {
		t.Fatalf("rotation promoted an incompatible Kubernetes source: %v", err)
	}
	repaired, err := updateSourceInTenant(ctx, pool, service, actor, legacyID, source.SourceUpdateCommand{ExpectedRevision: disabled.Revision, DataScopeMapping: &command.DataScopeMapping})
	if err != nil || !repaired.ScopeSupported() {
		t.Fatalf("repair stored incompatible mapping: %v", err)
	}
	if _, err := rollbackSourceInTenant(ctx, pool, service, actor, legacyID, source.SourceRegistrationRollbackCommand{ExpectedRevision: repaired.Revision, TargetRevision: disabled.Revision}); !errors.Is(err, source.ErrScopeIncompatible) {
		t.Fatalf("rollback restored incompatible historical mapping: %v", err)
	}
	rolledBack, err := rollbackSourceInTenant(ctx, pool, service, actor, registered.SourceID, source.SourceRegistrationRollbackCommand{ExpectedRevision: rotated.Revision, TargetRevision: registered.Revision})
	if err != nil || !rolledBack.ScopeSupported() || rolledBack.Revision <= rotated.Revision {
		t.Fatalf("rollback compatible scope must append a revision: %v", err)
	}
}

func cloneScopes(in map[string][]string) map[string][]string {
	out := map[string][]string{}
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func listSourceForTest(ctx context.Context, pool *pgxpool.Pool, actor auth.RequestContext, service *source.Service, sourceID uuid.UUID) (source.SourceRegistration, error) {
	all, err := service.ListSources(ctx, actor)
	if err != nil {
		return source.SourceRegistration{}, err
	}
	for _, item := range all {
		if item.SourceID == sourceID {
			return item, nil
		}
	}
	return source.SourceRegistration{}, errors.New("source registration not found")
}
