package graph

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"os"
	"sort"
	"testing"
	"time"
)

func object(kind, ns, name, uid string, spec map[string]any) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": kind, "metadata": map[string]any{"namespace": ns, "name": name, "uid": uid, "resourceVersion": "a"}, "spec": spec}}
}
func TestGraphScopeGenerationAndFencing(t *testing.T) {
	g := New("tenant-a", "cluster-a", "instance-a", []kubernetes.GVR{{Group: "", Version: "v1", Resource: "pods"}, {Group: "", Version: "v1", Resource: "nodes"}})
	g.SetOwner(4, time.Now().Add(time.Minute))
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}
	g.Replace(context.Background(), 4, kubernetes.Snapshot{GVR: kubernetes.GVR{Group: "", Version: "v1", Resource: "pods"}, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "pod-a", map[string]any{"nodeName": "node-a"})}, State: state})
	g.Replace(context.Background(), 4, kubernetes.Snapshot{GVR: kubernetes.GVR{Group: "", Version: "v1", Resource: "nodes"}, Objects: []unstructured.Unstructured{object("Node", "", "node-a", "node-a", nil)}, State: state})
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-a"}.String()
	q := Query{CanonicalID: id, ExpectedOwnerEpoch: 4, MaxDepth: 2, MaxNodes: 200, MaxEdges: 400, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
	got, err := g.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 1 || len(got.Edges) != 0 || !got.Partial {
		t.Fatalf("cluster scope leaked: %+v", got)
	}
	q.Scope.ClusterScoped = true
	got, err = g.Query(context.Background(), q)
	if err != nil || len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Fatalf("upstream relation missing: %+v %v", got, err)
	}
	q.ExpectedOwnerEpoch = 3
	if _, err = g.Query(context.Background(), q); err == nil {
		t.Fatal("stale owner queried")
	}
	if err = g.Replace(context.Background(), 3, kubernetes.Snapshot{}); err == nil {
		t.Fatal("stale owner wrote")
	}
	q.ExpectedOwnerEpoch = 4
	q.Scope.Tenant = "tenant-b"
	if _, err = g.Query(context.Background(), q); err == nil {
		t.Fatal("cross tenant")
	}
	g.SetOwner(5, time.Now().Add(time.Minute))
	q.ExpectedOwnerEpoch = 5
	q.Scope.Tenant = "tenant-a"
	q.CursorRevision = &got.GraphRevision
	if _, err = g.Query(context.Background(), q); err == nil {
		t.Fatal("old cursor accepted")
	}
}
func TestGraphRebuildDoesNotClaimFresh(t *testing.T) {
	g := New("tenant-a", "cluster-a", "instance-a", []kubernetes.GVR{{Group: "", Version: "v1", Resource: "pods"}})
	g.SetOwner(1, time.Now().Add(time.Minute))
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "p1"}.String()
	g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: kubernetes.GVR{Group: "", Version: "v1", Resource: "pods"}, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "p1", nil)}, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchContinuous: true, WatchConnected: true, LastConnectivityProbeAt: time.Now()}})
	g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: kubernetes.GVR{Group: "", Version: "v1", Resource: "pods"}, State: kubernetes.GVRState{LastError: "410_rebuilding"}})
	got, err := g.Query(context.Background(), Query{CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 5, MaxEdges: 5, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}})
	if err != nil || got.Freshness == "fresh" || !got.Partial || len(got.Nodes) != 1 {
		t.Fatalf("old snapshot falsely fresh or lost: %+v %v", got, err)
	}
}

func TestInvalidReplacementCannotPublishFreshState(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	stale := kubernetes.GVRState{LastListCompletedAt: time.Now(), LastError: "rebuilding"}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "p1", nil)}, State: stale}); err != nil {
		t.Fatal(err)
	}
	fresh := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "", nil)}, State: fresh}); err == nil {
		t.Fatal("invalid immutable ID accepted")
	}
	if g.Qualified(time.Now()) {
		t.Fatal("failed replacement published fresh state")
	}
}
func TestExternalMutationInvalidatesCursorAndRejectsForeignCluster(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	s := kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "p1", nil)}, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}}
	if err := g.Replace(context.Background(), 1, s); err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "p1"}.String()
	q := Query{QueryKind: "entity", CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 5, MaxEdges: 5, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
	r, err := g.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	edge := resource.Relation{From: id, To: id, Kind: "communicates_with", ObservedAt: time.Now(), TTLSeconds: 10}
	if err = g.AddExternal(1, []resource.Relation{edge}); err != nil {
		t.Fatal(err)
	}
	q.CursorRevision = &r.GraphRevision
	if _, err = g.Query(context.Background(), q); err == nil {
		t.Fatal("external mutation reused cursor generation")
	}
	foreign := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-b", APIGroup: "core", Kind: "Pod", StableID: "p2"}.String()
	edge.To = foreign
	if err = g.AddExternal(1, []resource.Relation{edge}); err == nil {
		t.Fatal("foreign cluster overlay accepted")
	}
}

func TestSourceFailurePreservesPublishedObjectsAndInvalidatesFreshness(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, State: state, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "p1", nil)}}); err != nil {
		t.Fatal(err)
	}
	state.LastError = "identity_or_metadata_unavailable"
	if err := g.UpdateSourceState(1, gvr, state); err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "p1"}.String()
	got, err := g.Query(context.Background(), Query{QueryKind: "entity", CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 5, MaxEdges: 5, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}})
	if err != nil || len(got.Nodes) != 1 || !got.Partial || got.Freshness != "stale" {
		t.Fatalf("failed source lost old graph or claimed fresh: %+v %v", got, err)
	}
	if err := g.UpdateSourceState(0, gvr, state); err != ErrStale {
		t.Fatal("stale owner changed state")
	}
}

func TestHealthProbeDoesNotRebuildGenerationOrReTimestampFacts(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	observed := time.Now().Add(-time.Minute).UTC()
	o := object("Pod", "apps", "web", "p1", nil)
	o.SetAnnotations(map[string]string{"ops.internal/observed-at": observed.Format(time.RFC3339Nano)})
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}
	s := kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{o}, State: state}
	if err := g.Replace(context.Background(), 1, s); err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "p1"}.String()
	q := Query{QueryKind: "entity", CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 5, MaxEdges: 5, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
	before, err := g.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	s.State.LastConnectivityProbeAt = time.Now()
	if err := g.Replace(context.Background(), 1, s); err != nil {
		t.Fatal(err)
	}
	after, err := g.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if before.GraphRevision != after.GraphRevision || !after.Nodes[0].UpdatedAt.Equal(observed) {
		t.Fatalf("probe changed fact version/time: before=%+v after=%+v", before, after)
	}
}

func TestPublishedGraphQueriesContinueDuringLargeRebuild(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}
	seed := object("Pod", "apps", "web", "p1", nil)
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, State: state, Objects: []unstructured.Unstructured{seed}}); err != nil {
		t.Fatal(err)
	}
	objects := []unstructured.Unstructured{seed}
	for i := 0; i < 3000; i++ {
		objects = append(objects, object("Pod", "load", fmt.Sprintf("pod-%d", i), fmt.Sprintf("uid-%d", i), nil))
	}
	done := make(chan error, 1)
	go func() {
		done <- g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, State: state, Objects: objects})
	}()
	// The pinned structural resolver performs reverse-owner resolution over a
	// large batch; this exercises the actual upstream engine, without a stub.
	time.Sleep(30 * time.Millisecond)
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "p1"}.String()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	started := time.Now()
	result, err := g.Query(ctx, Query{QueryKind: "entity", CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 5, MaxEdges: 5, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}})
	cancel()
	elapsed := time.Since(started)
	rebuildErr := <-done
	if rebuildErr != nil {
		t.Fatal(rebuildErr)
	}
	if err != nil || elapsed > 100*time.Millisecond || result.Freshness != "stale" || !result.Partial {
		t.Fatalf("published generation blocked or claimed fresh during rebuild: elapsed=%s result=%+v err=%v", elapsed, result, err)
	}
}

func TestEventUIDDoesNotRelateToRecreatedResource(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	events := kubernetes.GVR{Version: "v1", Resource: "events"}
	g := New("tenant-a", "cluster-a", "worker", []kubernetes.GVR{gvr, events})
	g.SetOwner(1, time.Now().Add(time.Minute))
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, State: state, Objects: []unstructured.Unstructured{object("Pod", "apps", "reused", "new-uid", nil)}}); err != nil {
		t.Fatal(err)
	}
	event := object("Event", "apps", "old-warning", "event-uid", nil)
	event.Object["involvedObject"] = map[string]any{"apiVersion": "v1", "kind": "Pod", "namespace": "apps", "name": "reused", "uid": "old-uid"}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: events, State: state, Objects: []unstructured.Unstructured{event}}); err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "new-uid"}.String()
	got, err := g.Query(context.Background(), Query{QueryKind: "neighbors", CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 10, MaxEdges: 10, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}})
	if err != nil || len(got.Edges) != 0 || len(got.Nodes) != 1 {
		t.Fatalf("old Event linked to recreated UID: %+v %v", got, err)
	}
}

func TestDiagnosticGraphAt200NodeBudgetLatency(t *testing.T) {
	if os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "pre-sp07-user-20261004" || os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp05-user-20261002" || os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp06-user-20261003" {
		t.Skip("SP05/SP06/pre-SP07 user waived dedicated performance measurements; not an acceptance pass")
	}
	pods := kubernetes.GVR{Version: "v1", Resource: "pods"}
	nodes := kubernetes.GVR{Version: "v1", Resource: "nodes"}
	g := New("tenant-a", "cluster-a", "worker", []kubernetes.GVR{pods, nodes})
	g.SetOwner(1, time.Now().Add(time.Minute))
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}
	objects := []unstructured.Unstructured{}
	for i := 0; i < 199; i++ {
		objects = append(objects, object("Pod", "apps", fmt.Sprintf("pod-%d", i), fmt.Sprintf("uid-%d", i), map[string]any{"nodeName": "node"}))
	}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: pods, State: state, Objects: objects}); err != nil {
		t.Fatal(err)
	}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: nodes, State: state, Objects: []unstructured.Unstructured{object("Node", "", "node", "node-uid", nil)}}); err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Node", StableID: "node-uid"}.String()
	q := Query{QueryKind: "impact", CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 2, MaxNodes: 200, MaxEdges: 400, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, ClusterScoped: true}}
	samples := []time.Duration{}
	for i := 0; i < 100; i++ {
		started := time.Now()
		got, err := g.Query(context.Background(), q)
		if err != nil || len(got.Nodes) != 200 || len(got.Edges) != 199 || got.Partial {
			t.Fatalf("dense diagnostic contract: nodes=%d edges=%d partial=%v err=%v", len(got.Nodes), len(got.Edges), got.Partial, err)
		}
		samples = append(samples, time.Since(started))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p95 := samples[94]
	if p95 > time.Second {
		t.Fatalf("P95 gate exceeded: %s", p95)
	}
	t.Logf("actual upstream ontology/Ariadne diagnostic depth=2 nodes=200 edges=199 samples=100 P95=%s; synthetic local fixture", p95)
}

func TestObservationOnlyPreservesPublishedFactsAndGeneration(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance-a", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "pod-a", nil)}, State: state}); err != nil {
		t.Fatal(err)
	}
	q := Query{CanonicalID: resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-a"}.String(), ExpectedOwnerEpoch: 1, QueryKind: "entity", MaxDepth: 1, MaxNodes: 10, MaxEdges: 10, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, ClusterScoped: true}}
	before, err := g.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	state.ProjectionQueueLag = 125 * time.Millisecond
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{ObservationOnly: true, GVR: gvr, State: state}); err != nil {
		t.Fatal(err)
	}
	after, err := g.Query(context.Background(), q)
	if err != nil || len(after.Nodes) != 1 || after.GraphRevision != before.GraphRevision {
		t.Fatalf("health snapshot erased facts or changed generation: %v %+v", err, after.GraphRevision)
	}
	if g.SourceStates()[gvr.Key()].ProjectionQueueLag != state.ProjectionQueueLag {
		t.Fatal("health observation lost")
	}
	if err := g.Replace(context.Background(), 0, kubernetes.Snapshot{ObservationOnly: true, GVR: gvr, State: state}); err != ErrStale {
		t.Fatal("health observation bypassed owner fencing")
	}
}
