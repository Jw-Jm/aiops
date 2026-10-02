package integration

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
	"testing"
	"time"
)

func TestSP04IdentityConflictFindingAndTerminalTombstone(t *testing.T) {
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	tenant, cluster, source := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp04-identity','Identity');`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,'cluster-a','Identity')`, tenant, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id) VALUES($1,$2,$3,'kubernetes','identity','openbao://test/kubernetes','kubernetes-a')`, tenant, source, cluster); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := resourcestore.Repository{Pool: pool}
	ref := resource.SourceRef{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster-a", APIGroup: "core", Kind: "Node", UID: "node-a", UUID: "550e8400-e29b-41d4-a716-446655440000", Name: "same-name", Serial: "duplicate-serial", SourceRegistrationID: source.String(), ObservedAt: time.Now().UTC()}
	matched, err := repo.Resolve(ctx, ref)
	if err != nil || matched.Status != resource.Matched {
		t.Fatalf("first identity: %+v %v", matched, err)
	}
	ref.UUID = "550e8400-e29b-41d4-a716-446655440001"
	ref.ObservedAt = time.Now().UTC()
	conflict, err := repo.Resolve(ctx, ref)
	if err != nil || conflict.Status != resource.Conflicted {
		t.Fatalf("changed immutable UUID: %+v %v", conflict, err)
	}
	if _, err = repo.Resolve(ctx, ref); err != nil {
		t.Fatal(err)
	}
	var count int
	var raw []byte
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1`, tenant).Scan(&count); err != nil || count != 1 {
		t.Fatalf("conflict Finding count %d: %v", count, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT payload FROM finding.records WHERE tenant_id=$1`, tenant).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = contract.Validate("https://ops.local/schemas/finding-envelope/v1", raw); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	json.Unmarshal(raw, &payload)
	if payload["resourceCanonicalId"] != matched.CanonicalID.String() {
		t.Fatal("conflict merged or lost observed identity")
	}
	if err = repo.Retire(ctx, tenant, matched.CanonicalID.String()); err != nil {
		t.Fatal(err)
	}
	ref.UUID = "550e8400-e29b-41d4-a716-446655440000"
	if _, err = repo.Resolve(context.Background(), ref); err == nil {
		t.Fatal("terminal UID resurrected")
	}
	ref.UID = "node-recreated"
	ref.ObservedAt = time.Now().UTC()
	replacement, err := repo.Resolve(ctx, ref)
	if err != nil || replacement.Status != resource.Matched || replacement.CanonicalID == matched.CanonicalID {
		t.Fatalf("name/serial merged recreated UID: %+v %v", replacement, err)
	}
	older := ref
	older.ObservedAt = ref.ObservedAt.Add(-time.Hour)
	older.Name = "lagging-standby-name"
	if resolved, err := repo.Resolve(ctx, older); err != nil || resolved.CanonicalID != replacement.CanonicalID {
		t.Fatalf("lagging standby blocked by newer observation: %+v %v", resolved, err)
	}
	var retainedName string
	if err := db.QueryRowContext(ctx, `SELECT name FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2`, tenant, replacement.CanonicalID.String()).Scan(&retainedName); err != nil || retainedName != ref.Name {
		t.Fatalf("older observation replaced current projection: %s %v", retainedName, err)
	}

	grant, _ := json.Marshal([]map[string]string{{"clusterId": cluster.String(), "namespace": "apps"}})
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,namespace_scopes) VALUES($1,$2,'namespace-only','operator',$3)`, tenant, uuid.New(), grant); err != nil {
		t.Fatal(err)
	}
	scope, err := (graph.Authorization{Pool: pool}).Effective(ctx, tenant.String(), "namespace-only", "cluster-a")
	podID := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-scope"}.String()
	if err != nil || scope.ClusterScoped || !scope.Allows(podID, "apps") || scope.Allows(replacement.CanonicalID.String(), "") || scope.Allows(podID, "foreign") {
		t.Fatalf("namespace grant denied or expanded to cluster resources: %+v %v", scope, err)
	}

}
