package graph

import (
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"slices"
	"testing"
	"time"
)

func semanticGraph(t *testing.T) (*Graph, Query) {
	g := New("tenant-a", "cluster-a", "worker-a", []kubernetes.GVR{{Version: "v1", Resource: "pods"}})
	g.SetOwner(1, time.Now().Add(time.Minute))
	pod := object("Pod", "apps", "web", "pod-a", map[string]any{"nodeName": "node-a"})
	pod.SetLabels(map[string]string{"app": "web"})
	service := object("Service", "apps", "web", "service-a", map[string]any{"selector": map[string]any{"app": "web"}})
	objects := []unstructured.Unstructured{pod, service, object("Node", "", "node-a", "node-a", nil)}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: kubernetes.GVR{Version: "v1", Resource: "pods"}, Objects: objects, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}}); err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Node", StableID: "node-a"}.String()
	return g, Query{CanonicalID: id, QueryKind: "impact", MaxDepth: 2, MaxNodes: 10, MaxEdges: 10, ExpectedOwnerEpoch: 1, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, ClusterScoped: true}}
}
func TestImpactProjectsUpstreamDirectIndirectAndDependencies(t *testing.T) {
	g, q := semanticGraph(t)
	got, err := g.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	pod := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-a"}.String()
	service := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Service", StableID: "service-a"}.String()
	if !slices.Contains(got.DirectlyAffected, pod) || !slices.Contains(got.IndirectlyAffected, service) {
		t.Fatalf("impact classes missing: %+v", got)
	}
	q.CanonicalID = pod
	got, err = g.Query(context.Background(), q)
	if err != nil || !slices.Contains(got.DependencyOnly, resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Node", StableID: "node-a"}.String()) {
		t.Fatalf("dependency class missing: %+v %v", got, err)
	}
	q.Scope.ClusterScoped = false
	got, err = g.Query(context.Background(), q)
	if err != nil || len(got.DependencyOnly) != 0 || !got.Partial {
		t.Fatalf("cluster scope leaked: %+v %v", got, err)
	}
}
func TestNeighborsDirectionFilterDepthAndBudget(t *testing.T) {
	g, q := semanticGraph(t)
	q.QueryKind = "neighbors"
	q.Direction = "out"
	got, err := g.Query(context.Background(), q)
	if err != nil || len(got.Nodes) != 1 {
		t.Fatalf("out direction ignored: %+v %v", got, err)
	}
	q.Direction = "in"
	q.MaxDepth = 1
	got, err = g.Query(context.Background(), q)
	if err != nil || len(got.Nodes) != 2 {
		t.Fatalf("in direction ignored: %+v %v", got, err)
	}
	q.MaxDepth = 2
	got, err = g.Query(context.Background(), q)
	if err != nil || len(got.Nodes) != 3 {
		t.Fatalf("depth ignored: %+v %v", got, err)
	}
	q.RelationKinds = []string{"selects_pod"}
	got, err = g.Query(context.Background(), q)
	if err != nil || len(got.Nodes) != 1 {
		t.Fatalf("kind filter ignored: %+v %v", got, err)
	}
	q.RelationKinds = nil
	q.MaxNodes = 1
	got, err = g.Query(context.Background(), q)
	if err != nil || len(got.Nodes) > 1 || !got.Partial {
		t.Fatalf("node budget ignored: %+v %v", got, err)
	}
}
