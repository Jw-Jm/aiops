package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
)

func TestReadResponseUsesCurrentSnapshotAndFencesSerialization(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}
	objects := []unstructured.Unstructured{object("Pod", "apps", "web", "pod-1", nil)}
	put := func() error {
		return g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: state})
	}
	if err := put(); err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-1"}.String()
	q := Query{CanonicalID: id, QueryKind: "entity", MaxDepth: 1, MaxNodes: 5, MaxEdges: 5, ExpectedOwnerEpoch: 1, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
	old, err := g.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	objects[0].SetResourceVersion("2")
	if err = put(); err != nil {
		t.Fatal(err)
	}
	if err = g.RevalidateResponse(&old); !errors.Is(err, ErrStale) {
		t.Fatalf("old snapshot must remain stale: %v", err)
	}
	var encoded Revision
	writerDone := make(chan error, 1)
	started := make(chan struct{})
	err = g.QueryReadResponse(context.Background(), q, func(current Result) error {
		if current.GraphRevision == old.GraphRevision || current.Partial {
			t.Fatal("final serialization did not refresh the current complete snapshot")
		}
		encoded = current.GraphRevision
		objects[0].SetResourceVersion("3")
		go func() { close(started); writerDone <- put() }()
		<-started
		select {
		case e := <-writerDone:
			t.Fatalf("publication crossed serialization fence: %v", e)
		case <-time.After(20 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = <-writerDone; err != nil {
		t.Fatal(err)
	}
	q.CursorRevision = &encoded
	if err = g.QueryReadResponse(context.Background(), q, func(Result) error { t.Fatal("stale paged cursor encoded"); return nil }); !errors.Is(err, ErrStale) {
		t.Fatalf("paged revision bypassed: %v", err)
	}
	q.CursorRevision = nil
	q.ExpectedOwnerEpoch = 2
	if err = g.QueryReadResponse(context.Background(), q, func(Result) error { t.Fatal("foreign epoch encoded"); return nil }); !errors.Is(err, ErrNotReady) {
		t.Fatalf("owner fence bypassed: %v", err)
	}
}
