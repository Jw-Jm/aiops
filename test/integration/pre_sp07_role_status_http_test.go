package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/auth"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/persistence"
	"ops-platform/internal/tenant"
)

// This regression exercises the complete router and real PostgreSQL transaction.
// Its preverified request context does not replace native Keycloak acceptance.
func TestOperatorRoleStatusHTTPTransactionAndCurrentStepUpReplay(t *testing.T) {
	ctx, db, dir, url := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, url, dir); err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants (tenant_id,slug,display_name) VALUES ($1,$2,'Role router regression')`, tenantID, tenantID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings (tenant_id,binding_id,subject,role_name) VALUES ($1,$2,'router-admin','platform_admin')`, tenantID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	clusterID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations (tenant_id,cluster_id,cluster_uid,display_name) VALUES ($1,$2,'router-cluster','Router Cluster')`, tenantID, clusterID); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, url, "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service, err := tenant.NewService(pool)
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.RequestContext{RequestID: "role-router-regression", TenantID: tenantID, Subject: "router-admin", Roles: []auth.Role{auth.PlatformAdmin}, KeycloakSID: "verified-session", ACR: auth.StepUpACRLevel2, AuthTime: time.Now().UTC().Add(-time.Second).Truncate(time.Second)}
	var binding tenant.RoleBinding
	if err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if _, err := auth.RecordStepUpSession(ctx, tx, actor, uuid.New(), []string{auth.StepUpACRLevel2}); err != nil {
			return err
		}
		var err error
		binding, err = service.CreateRoleBinding(ctx, tx, actor, tenant.CreateRoleBindingInput{Subject: "router-operator", Role: auth.Operator, ClusterScopes: []uuid.UUID{clusterID}, NamespaceScopes: []auth.NamespaceScope{{ClusterID: clusterID, Namespace: "router-targets"}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	router, err := httpapi.NewTenantAdminRouter(service, pool)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/role-bindings/" + binding.ID.String() + "/status"
	call := func(body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r = r.WithContext(auth.WithRequestContext(r.Context(), actor))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	invalid := call(`{"expectedRevision":1,"status":"disabled","subject":"replacement"}`, "role-invalid")
	if invalid.Code != 400 {
		t.Fatalf("complete router must reject authority substitution with 400, got %d: %s", invalid.Code, invalid.Body.String())
	}
	body := `{"expectedRevision":1,"status":"disabled"}`
	first := call(body, "role-disable")
	if first.Code != 200 {
		t.Fatalf("transactional step-up status change: %d %s", first.Code, first.Body.String())
	}
	replay := call(body, "role-disable")
	if replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatal("same authenticated request did not replay its exact result")
	}
	var revision int64
	var status, subject string
	read := func() {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT revision,status,subject FROM platform.role_bindings WHERE tenant_id=$1 AND binding_id=$2`, tenantID, binding.ID).Scan(&revision, &status, &subject); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if revision != 2 || status != "disabled" || subject != "router-operator" {
		t.Fatal("replay altered binding identity or revision")
	}
	restore := call(fmt.Sprintf(`{"expectedRevision":%d,"status":"active"}`, revision), "role-restore")
	if restore.Code != 200 {
		t.Fatalf("restore: %d %s", restore.Code, restore.Body.String())
	}
	read()
	if revision != 3 || status != "active" {
		t.Fatal("restore did not advance the exact binding")
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.step_up_sessions SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND subject=$2`, tenantID, actor.Subject); err != nil {
		t.Fatal(err)
	}
	revoked := call(body, "role-disable")
	if revoked.Code != 403 {
		t.Fatalf("revoked current step-up replayed cached success: %d %s", revoked.Code, revoked.Body.String())
	}
	read()
	if revision != 3 || status != "active" || subject != "router-operator" {
		t.Fatal("rejected replay mutated the restored binding")
	}
}
