package graph

import (
	"context"
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/contract"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestPublishedGraphMatchesVersionedContract(t *testing.T) {
	tenant := "f3e05c72-03c7-46ce-9752-aa217368a5da"
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New(tenant, "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	pod := object("Pod", "apps", "web", "p1", nil)
	pod.SetAnnotations(map[string]string{"ops.internal/source-id": "003058b8-ddd8-4f0a-9bd8-e719332de1ae", "ops.internal/observed-at": time.Now().UTC().Format(time.RFC3339Nano)})
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{pod}, State: state}); err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "p1"}.String()
	for _, kind := range []string{"entity", "neighbors", "impact", "diagnostic", "list"} {
		q := Query{QueryKind: kind, CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 2, MaxNodes: 200, MaxEdges: 400, Scope: Scope{Tenant: tenant, Cluster: "cluster-a", Namespaces: []string{"apps"}, ClusterScoped: true, AuthorizationRevision: "test"}}
		result, err := g.Query(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := contract.Validate("https://ops.local/schemas/resource-graph/v2", raw); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}
