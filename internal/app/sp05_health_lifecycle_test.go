package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/graph"
	"ops-platform/internal/observability"
)

// The business health of the SP05 collection sources belongs to the running
// Worker: the registration exists after the startup returned, one source
// recovering does not clear another source, and the registration is released
// with the Worker when the run context ends.
func TestSP05WorkerOwnsTheBusinessHealthRegistration(t *testing.T) {
	directory := t.TempDir()
	trustFile := filepath.Join(directory, "trust.json")
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]string{"integration-key": base64.StdEncoding.EncodeToString(public)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trustFile, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLATFORM_REGISTRY_TRUST_FILE", trustFile)

	// The pool never answers: every collection pass fails, which is the state
	// the health has to report.
	pool, err := pgxpool.New(context.Background(), "postgres://127.0.0.1:1/unreachable?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	runtime, err := observability.NewRuntime(context.Background(), "sp05-health-test")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())

	clusters := []SP04Cluster{
		{Tenant: "11111111-1111-1111-1111-111111111111", ClusterUID: "cluster-a", SourceID: "22222222-2222-2222-2222-222222222222", BackendLogicalID: "kubernetes-a"},
		{Tenant: "11111111-1111-1111-1111-111111111111", ClusterUID: "cluster-b", SourceID: "33333333-3333-3333-3333-333333333333", BackendLogicalID: "kubernetes-b"},
	}
	config := &SP04Config{SP05: &SP05Config{Enabled: true}, Clusters: clusters}
	ctx, cancel := context.WithCancel(context.Background())
	group := &sync.WaitGroup{}
	if err := startSP05Worker(ctx, pool, nil, config, map[string]graph.InternalHandler{}, nil, runtime, group); err != nil {
		t.Fatalf("start the SP05 worker: %v", err)
	}
	// The startup returned: the registration of the configured sources must
	// still exist, otherwise every later report is dropped.
	for _, cluster := range clusters {
		for _, key := range sp05HealthKeys(cluster) {
			if !runtime.Metrics.BusinessComponentRegistered(key) {
				t.Fatalf("the health key %s is not registered after the startup", key)
			}
		}
	}
	// A failing collection pass reports the degraded state of its source.
	runtime.Metrics.SetBusinessDegraded(sp05WorkerHealthKey(clusters[0]), true)
	runtime.Metrics.SetBusinessDegraded(sp05HealthKeys(clusters[1])[1], true)
	if !runtime.Metrics.BusinessDegraded() {
		t.Fatal("the failing collection is not reported as degraded")
	}
	// One source recovers: the other source stays failed.
	runtime.Metrics.SetBusinessDegraded(sp05WorkerHealthKey(clusters[0]), false)
	if !runtime.Metrics.BusinessDegraded() {
		t.Fatal("the recovery of one source cleared the failure of another")
	}
	// All sources recover.
	runtime.Metrics.SetBusinessDegraded(sp05HealthKeys(clusters[1])[1], false)
	if runtime.Metrics.BusinessDegraded() {
		t.Fatal("the recovered sources are still degraded")
	}
	// The Worker stops: the registration is released with it.
	cancel()
	group.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !runtime.Metrics.BusinessDegraded() && len(sp05HealthKeys(clusters[0])) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// An unregistered key is refused, so a late report of a stopped Worker can
	// never create a failure.
	runtime.Metrics.SetBusinessDegraded(sp05WorkerHealthKey(clusters[0]), true)
	if runtime.Metrics.BusinessDegraded() {
		t.Fatal("a stopped Worker still owns a business health key")
	}
}
