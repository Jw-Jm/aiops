package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net/http"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSP04OrbStackNonVirtualListWatch(t *testing.T) {
	endpoint := os.Getenv("SP04_KUBERNETES_URL")
	if endpoint == "" {
		t.Skip("SP04_KUBERNETES_URL must point to this run's local OrbStack proxy")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := func(input string, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		cmd.Stdin = strings.NewReader(input)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("kubectl %v: %v %s", args, err, raw)
		}
		return raw
	}
	cluster := strings.TrimSpace(string(command("", "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")))
	ns := "ops-sp04-" + uuid.NewString()[:8]
	raw := command(fmt.Sprintf(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":%q,"labels":{"ops.platform/test":"sp04-20261001"}}}`, ns), "create", "-f", "-", "-o", "json")
	var namespace unstructured.Unstructured
	if err := json.Unmarshal(raw, &namespace); err != nil {
		t.Fatal(err)
	}
	nsUID := namespace.GetUID()
	defer func() {
		cmd := exec.Command("kubectl", "get", "namespace", ns, "-o", "json")
		b, err := cmd.Output()
		if err != nil {
			return
		}
		var current unstructured.Unstructured
		if json.Unmarshal(b, &current) != nil || current.GetUID() != nsUID || current.GetLabels()["ops.platform/test"] != "sp04-20261001" {
			t.Error("owned namespace cleanup identity mismatch")
			return
		}
		if err := exec.Command("kubectl", "delete", "namespace", ns, "--wait=false").Run(); err != nil {
			t.Error(err)
		}
	}()
	tenant := uuid.NewString()
	client, err := kubernetes.NewClient(endpoint, &http.Client{}, 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	g := graph.New(tenant, cluster, "owned-e2e", kubernetes.CoreRequiredGVRs())
	g.SetOwner(1, time.Now().Add(4*time.Minute))
	var group sync.WaitGroup
	var firstErr error
	var errorsMu sync.Mutex
	for _, gvr := range kubernetes.CoreRequiredGVRs() {
		gvr := gvr
		group.Add(1)
		go func() {
			defer group.Done()
			err := client.Run(ctx, gvr, func(s kubernetes.Snapshot) error { return g.Replace(ctx, 1, s) })
			if err != nil && ctx.Err() == nil {
				errorsMu.Lock()
				firstErr = err
				errorsMu.Unlock()
			}
		}()
	}
	defer func() { cancel(); group.Wait() }()
	wait := func(predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if predicate() {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(25 * time.Millisecond):
			}
		}
		errorsMu.Lock()
		defer errorsMu.Unlock()
		t.Fatalf("convergence deadline exceeded; collector error=%v states=%+v", firstErr, g.SourceStates())
	}
	wait(func() bool { return g.Qualified(time.Now()) })
	scope := graph.Scope{Tenant: tenant, Cluster: cluster, Namespaces: []string{ns}, ClusterScoped: true, AuthorizationRevision: "live-test"}
	convergence := []time.Duration{}
	latency := []time.Duration{}
	for n := 0; n < 30; n++ {
		started := time.Now()
		raw = command(fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"fixture-%d","namespace":%q,"labels":{"app":"sp04"}},"spec":{"containers":[{"name":"pause","image":"registry.k8s.io/pause:3.10.1"}]}}`, n, ns), "create", "-f", "-", "-o", "json")
		var pod unstructured.Unstructured
		if err := json.Unmarshal(raw, &pod); err != nil {
			t.Fatal(err)
		}
		id := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: cluster, APIGroup: "core", Kind: "Pod", StableID: string(pod.GetUID())}.String()
		q := graph.Query{QueryKind: "diagnostic", CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 2, MaxNodes: 200, MaxEdges: 400, Scope: scope}
		wait(func() bool {
			got, err := g.Query(ctx, q)
			return err == nil && len(got.Nodes) > 0 && got.Freshness == "fresh"
		})
		convergence = append(convergence, time.Since(started))
		for repeat := 0; repeat < 10; repeat++ {
			start := time.Now()
			r, err := g.Query(ctx, q)
			latency = append(latency, time.Since(start))
			if err != nil || len(r.Nodes) > 200 {
				t.Fatalf("query: %v", err)
			}
			for _, node := range r.Nodes {
				if !scope.Allows(node.CanonicalID, node.Namespace) {
					t.Fatal("resource scope leaked")
				}
			}
		}
		command("", "delete", "pod", pod.GetName(), "-n", ns, "--force", "--grace-period=0", "--wait=false")
		wait(func() bool { _, err := g.Query(ctx, q); return err != nil })
	}
	p95 := func(v []time.Duration) time.Duration {
		sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
		return v[(len(v)*95+99)/100-1]
	}
	if p95(convergence) > 60*time.Second || p95(latency) > time.Second {
		t.Fatalf("latency gate: convergence %v graph %v", p95(convergence), p95(latency))
	}
	t.Logf("scale=one OrbStack cluster, 30 sequential owned Pods; GVRs=%d; convergence samples=%d P95=%s; Graph depth=2/maxNodes=200 samples=%d P95=%s; actual subgraphs small, not production-scale qualification", len(kubernetes.CoreRequiredGVRs()), len(convergence), p95(convergence), len(latency), p95(latency))
}
