package graph

import (
	"context"
	"errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestResponseRevalidatesGenerationAndRebuildFreshness(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}
	objects := []unstructured.Unstructured{object("Pod", "apps", "web", "pod-1", nil)}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: state}); err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-1"}.String()
	result, err := g.Query(context.Background(), Query{CanonicalID: id, MaxDepth: 1, MaxNodes: 5, MaxEdges: 5, ExpectedOwnerEpoch: 1, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, State: kubernetes.GVRState{LastError: "410_rebuilding"}}); err != nil {
		t.Fatal(err)
	}
	if err := g.RevalidateResponse(&result); err != nil || result.Freshness == "fresh" || !result.Partial {
		t.Fatalf("old response declared fresh after rebuild started: %+v %v", result, err)
	}
	objects[0].SetResourceVersion("2")
	objects[0].SetLabels(map[string]string{"revision": "2"})
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: state}); err != nil {
		t.Fatal(err)
	}
	if err := g.RevalidateResponse(&result); !errors.Is(err, ErrStale) {
		t.Fatalf("old generation response survived atomic swap: %v", err)
	}
}
