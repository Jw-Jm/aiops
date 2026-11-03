package app

import (
	"strings"
	"testing"
	"time"

	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
)

func TestArchiveConflictSurvivesUnrelatedQueueCompletion(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := graph.New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	if err := g.Replace(t.Context(), 1, kubernetes.Snapshot{GVR: gvr, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}}); err != nil {
		t.Fatal(err)
	}
	q := &projectionArchiveQueue{graph: g, cluster: SP04Cluster{SourceID: "source-a"}, pending: map[string]bool{"new-resource/2": true}, failed: map[string]bool{}, conflicts: map[string]bool{"old-resource/1": true}}
	check := func() {
		t.Helper()
		q.healthLocked()
		result, err := g.Query(t.Context(), graph.Query{QueryKind: "list", MaxDepth: 1, MaxNodes: 10, MaxEdges: 10, ExpectedOwnerEpoch: 1, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Partial || !strings.Contains(strings.Join(result.Warnings, ","), "projection_archive_identity_conflict") || len(result.DegradedSources) != 1 || result.DegradedSources[0] != "source-a/archive" {
			t.Fatalf("conflict disappeared from resource response: %+v", result)
		}
	}
	check()
	delete(q.pending, "new-resource/2")
	check()
}
