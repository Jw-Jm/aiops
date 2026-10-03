package graph

import (
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestSP05IdenticalExternalReplayCannotInvalidateFrozenGraphRevision(t *testing.T) {
	now := time.Now().UTC()
	g := New("tenant-a", "cluster-a", "worker", nil)
	g.SetOwner(1, now.Add(time.Minute))
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: kubernetes.GVR{Version: "v1", Resource: "nodes"}, Objects: []unstructured.Unstructured{object("Node", "", "node-a", "node-a", nil)}, State: kubernetes.GVRState{LastListCompletedAt: now}}); err != nil {
		t.Fatal(err)
	}
	a := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Node", StableID: "node-a"}.String()
	b := resource.CanonicalID{Domain: "hardware", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "redfish", Kind: "PhysicalServer", StableID: "server-a"}.String()
	edges := []resource.Relation{{From: b, To: a, Kind: "hosts", ObservedAt: now, TTLSeconds: 300, Provenance: resource.Provenance{SourceRegistrationID: "hardware-a", RuleVersion: "hosts/v1"}}}
	if err := g.ReplaceExternalSource(1, "hardware-a", edges); err != nil {
		t.Fatal(err)
	}
	before := g.current.revision
	if err := g.ReplaceExternalSource(1, "hardware-a", edges); err != nil {
		t.Fatal(err)
	}
	if before != g.current.revision {
		t.Fatalf("identical trusted edge replay invalidated proof: before=%+v after=%+v", before, g.current.revision)
	}
	edges[0].TTLSeconds = 299
	if err := g.ReplaceExternalSource(1, "hardware-a", edges); err != nil {
		t.Fatal(err)
	}
	if before == g.current.revision {
		t.Fatal("real freshness/TTL change did not invalidate proof")
	}
}
