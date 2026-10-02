package graph

import (
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"testing"
	"time"
)

func TestFilteredSortedScopedPages(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	objects := []unstructured.Unstructured{}
	for _, spec := range []struct{ name, uid, ns, phase string }{{"a", "03", "apps", "Running"}, {"b", "02", "apps", "Running"}, {"c", "01", "apps", "Failed"}, {"foreign", "00", "foreign", "Running"}} {
		o := object("Pod", spec.ns, spec.name, spec.uid, nil)
		o.SetLabels(map[string]string{"app": "web"})
		o.Object["status"] = map[string]any{"phase": spec.phase}
		objects = append(objects, o)
	}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	q := Query{QueryKind: "list", ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 1, MaxEdges: 2, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}, Health: "normal", Label: "app=web", Sort: "name", Order: "desc"}
	first, err := g.Query(context.Background(), q)
	if err != nil || len(first.Nodes) != 1 || first.Nodes[0].Name != "b" || first.NextCanonicalID == "" {
		t.Fatalf("first page %+v %v", first, err)
	}
	q.AfterCanonicalID = first.NextCanonicalID
	q.CursorRevision = &first.GraphRevision
	second, err := g.Query(context.Background(), q)
	if err != nil || len(second.Nodes) != 1 || second.Nodes[0].Name != "a" || second.NextCanonicalID != "" {
		t.Fatalf("second page %+v %v", second, err)
	}
	changed := q
	changed.Label = "app=other"
	if QueryDigest(q) == QueryDigest(changed) {
		t.Fatal("filters not bound into cursor")
	}
	q.Sort = "arbitrary SQL"
	if _, err := g.Query(context.Background(), q); err == nil {
		t.Fatal("unknown sort accepted")
	}
}
