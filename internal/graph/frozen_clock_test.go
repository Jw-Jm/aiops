package graph

import (
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kube "ops-platform/internal/integrations/kubernetes"
	"testing"
	"time"
)

func TestFrozenInputClockPreservesQualificationAndExpiry(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	gvr := kube.GVR{Version: "v1", Resource: "pods"}
	g := NewWithClock("tenant-a", "cluster-a", "frozen-instance", []kube.GVR{gvr}, func() time.Time { return clock })
	g.SetOwner(1, clock.Add(time.Minute))
	pod := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "web", "namespace": "apps", "uid": "pod-a", "annotations": map[string]any{"ops.internal/source-id": "source-a", "ops.internal/observed-at": clock.Format(time.RFC3339Nano)}}}}
	state := kube.GVRState{LastListCompletedAt: clock, LastConnectivityProbeAt: clock, WatchConnected: true, WatchContinuous: true}
	if err := g.Replace(context.Background(), 1, kube.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{pod}, State: state}); err != nil {
		t.Fatal(err)
	}
	q := Query{QueryKind: "list", ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 10, MaxEdges: 20, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "frozen/v1"}}
	out, err := g.Query(context.Background(), q)
	if err != nil || out.Freshness != "fresh" || !out.CollectedAt.Equal(clock) {
		t.Fatalf("frozen input evaluated against wall clock: %+v %v", out, err)
	}
	clock = clock.Add(2 * time.Minute)
	if _, err := g.Query(context.Background(), q); err == nil {
		t.Fatal("frozen clock incorrectly disables ownership expiry")
	}
}
