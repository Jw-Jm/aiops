package integration

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net/http"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"os"
	"strings"
	"testing"
	"time"
)

type nativeLeaseContractTransport struct {
	base  http.RoundTripper
	token string
	t     *testing.T
}

func (tr nativeLeaseContractTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copied := request.Clone(request.Context())
	copied.Header = request.Header.Clone()
	copied.Header.Set("Authorization", "Bearer "+tr.token)
	response, err := tr.base.RoundTrip(copied)
	if err == nil && request.Method == "PUT" && response.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
		var status struct{ Reason, Message string }
		json.Unmarshal(body, &status)
		tr.t.Logf("actual native Lease PUT status=%d reason=%s message=%s", response.StatusCode, status.Reason, status.Message)
	}
	return response, err
}

func TestSP04NativeWorkerLeaseCASWireContract(t *testing.T) {
	if os.Getenv("SP04_TEST_ORBSTACK") != "1" {
		t.Skip("owned OrbStack native Lease opt-in required")
	}
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	tenant, cluster, source := uuid.New(), uuid.New(), uuid.New()
	collector, ns := sp04OwnedKubernetes(t, ctx, tenant.String(), source.String())
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,$2,'native-lease')`, tenant, tenant.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'native-lease')`, tenant, cluster, collector.ClusterUID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{
		map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": "ops-worker", "namespace": ns}},
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": map[string]any{"name": "native-worker-lease", "namespace": ns}, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "lease"}, "subjects": []any{map[string]string{"kind": "ServiceAccount", "name": "ops-worker", "namespace": ns}}},
	}})
	sp04Kubectl(t, ctx, raw, "create", "-f", "-")
	token := strings.TrimSpace(string(sp04Kubectl(t, ctx, nil, "create", "token", "ops-worker", "-n", ns, "--duration=10m")))
	ca, err := os.ReadFile(collector.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer transport.CloseIdleConnections()
	client, err := kubernetes.NewClient(collector.Endpoint, &http.Client{Transport: nativeLeaseContractTransport{transport, token, t}}, 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := graph.New(tenant.String(), collector.ClusterUID, uuid.NewString(), []kubernetes.GVR{gvr})
	now := time.Now()
	if err = g.Replace(ctx, 0, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{}, State: kubernetes.GVRState{LastListCompletedAt: now, LastConnectivityProbeAt: now, WatchConnected: true, WatchContinuous: true}}); err != nil {
		t.Fatal(err)
	}
	lease := &graph.Lease{Client: client, Graph: g, Mirror: graph.Repository{Pool: pool}, Namespace: ns, Name: "sp04-graph", Endpoint: "https://127.0.0.1:9444"}
	if err = lease.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if g.OwnerEpoch() != 1 {
		t.Fatal("native Lease acquisition did not establish epoch1")
	}
	if err = lease.Confirm(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err = lease.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log("real native Worker SA scoped GET/PUT acquired, confirmed and renewed Kubernetes Lease; PG only routing mirror; synthetic source readiness is explicit, this is native wire contract not collector/Pod E2E")
}
