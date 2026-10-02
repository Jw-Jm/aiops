package integration

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/graph"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSP04LeaseRecoveryNativeAuthorityAndAudit(t *testing.T) {
	if os.Getenv("SP04_TEST_ORBSTACK") != "1" {
		t.Skip("owned OrbStack environment required")
	}
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	tenant, source, cluster := uuid.New(), uuid.New(), uuid.New()
	collector, namespace := sp04OwnedKubernetes(t, ctx, tenant.String(), source.String())
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp04-recovery','Recovery')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'Owned recovery')`, tenant, cluster, collector.ClusterUID); err != nil {
		t.Fatal(err)
	}
	config := runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role")
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var original graph.LeaseDocument
	if json.Unmarshal(sp04Kubectl(t, ctx, nil, "get", "lease", "sp04-graph", "-n", namespace, "-o", "json"), &original) != nil {
		t.Fatal("owned Lease unavailable")
	}
	// Seed a historical epoch floor; real native authority recovery must exceed it.
	sp04Kubectl(t, ctx, []byte(`{"metadata":{"annotations":{"ops.platform/owner-epoch":"7"}}}`), "patch", "lease", "sp04-graph", "-n", namespace, "--type=merge", "--patch-file=/dev/stdin")
	repo := graph.Repository{Pool: pool}
	if err := repo.Record(ctx, graph.OwnershipMirror{Tenant: tenant.String(), Cluster: collector.ClusterUID, Epoch: 7, Instance: "old-process", Endpoint: "https://127.0.0.1:9444", LeaseUID: original.Metadata.UID, ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	options, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]string{"uid": original.Metadata.UID}})
	sp04Kubectl(t, ctx, options, "delete", "--raw", "/apis/coordination.k8s.io/v1/namespaces/"+namespace+"/leases/sp04-graph", "-f", "-")
	// The recovery operator gets create permission only in this owned namespace;
	// ordinary Worker RBAC remains get/update and has no delete/create authority.
	resources := []any{
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role", "metadata": map[string]any{"name": "recovery-operator", "namespace": namespace}, "rules": []any{map[string]any{"apiGroups": []string{"coordination.k8s.io"}, "resources": []string{"leases"}, "verbs": []string{"create"}}}},
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": map[string]any{"name": "recovery-operator", "namespace": namespace}, "roleRef": map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "recovery-operator"}, "subjects": []any{map[string]any{"kind": "ServiceAccount", "name": "collector", "namespace": namespace}}},
	}
	raw, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": resources})
	sp04Kubectl(t, ctx, raw, "create", "-f", "-")
	patch, _ := json.Marshal([]any{map[string]any{"op": "add", "path": "/rules/-", "value": map[string]any{"apiGroups": []string{""}, "resources": []string{"namespaces"}, "resourceNames": []string{"kube-system"}, "verbs": []string{"get"}}}})
	sp04Kubectl(t, ctx, patch, "patch", "clusterrole", namespace, "--type=json", "--patch-file=/dev/stdin")
	private := filepath.Join(t.TempDir(), "recovery.json")
	request := graph.LeaseRecoveryRequest{Tenant: tenant.String(), Cluster: collector.ClusterUID, Namespace: namespace, Name: "sp04-graph", ExpectedPreviousLeaseUID: original.Metadata.UID, Ticket: "owned-SP04-native-recovery"}
	raw, _ = json.Marshal(map[string]any{"DatabaseURL": config.ConnConfig.ConnString(), "Endpoint": collector.Endpoint, "CAFile": collector.CAFile, "TokenFile": collector.TokenFile, "Request": request})
	if err := os.WriteFile(private, raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "go", "run", "../../cmd/opsctl", "graph", "lease-recover", "--config", private)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("operator recovery failed: %v %s", err, out)
	}
	var receipt graph.LeaseRecoveryReceipt
	if json.Unmarshal(out, &receipt) != nil || receipt.EpochFloor <= 7 || receipt.LeaseUID == original.Metadata.UID {
		t.Fatalf("recovery receipt invalid: %s", out)
	}
	mirror, err := repo.Load(ctx, tenant.String(), collector.ClusterUID)
	if err != nil || mirror.Epoch != receipt.EpochFloor || mirror.LeaseUID != receipt.LeaseUID || mirror.ExpiresAt.After(time.Now()) || mirror.Instance != "recovery_pending" {
		t.Fatalf("recovery elected a database owner: %+v %v", mirror, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type IN ('graph.lease_recovery_requested','graph.lease_recovery_applied')`, tenant).Scan(&count); err != nil || count != 2 {
		t.Fatalf("recovery audit missing count=%d err=%v", count, err)
	}
	// Repeating a stale expected UID cannot overwrite the repaired authority.
	if _, err := exec.CommandContext(ctx, "go", "run", "../../cmd/opsctl", "graph", "lease-recover", "--config", private).CombinedOutput(); err == nil {
		t.Fatal("stale recovery witness reused")
	}
	t.Log(fmt.Sprintf("native UID-guarded deletion -> authenticated operator CLI -> monotonic authority epoch floor %d -> two durable audit records; mirror route remains expired", receipt.EpochFloor))
}
