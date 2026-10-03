package graph

import (
	"context"
	"ops-platform/internal/resource"
	"slices"
	"testing"
	"time"
)

func TestDIMMFaultFollowsPhysicalHostAndScheduledPodInBoundedKernel(t *testing.T) {
	g, q := semanticGraph(t)
	q.MaxDepth, q.MaxNodes, q.MaxEdges = 2, 20, 40
	now := time.Now().Add(-time.Second)
	canonical := func(kind, uid string) string {
		return resource.CanonicalID{Domain: "hardware", Tenant: q.Scope.Tenant, Scope: q.Scope.Cluster, APIGroup: "redfish", Kind: kind, StableID: uid}.String()
	}
	server, dimm := canonical("PhysicalServer", "550e8400-e29b-41d4-a716-446655440000"), canonical("DIMM", "550e8400-e29b-41d4-a716-446655440000/DIMM1")
	const source = "a1dd5819-8016-4f4a-9c59-3ae628c517b3"
	if err := g.ReplaceHardware(context.Background(), source, []resource.Entity{{CanonicalID: server, Kind: "PhysicalServer", Name: "server", UpdatedAt: now, Attributes: map[string]any{}}, {CanonicalID: dimm, Kind: "DIMM", Name: "DIMM1", UpdatedAt: now, Attributes: map[string]any{}}}, now); err != nil {
		t.Fatal(err)
	}
	edge := func(from, to, kind string) resource.Relation {
		return resource.Relation{From: from, To: to, Kind: kind, ObservedAt: now, ValidFrom: now, TTLSeconds: 600, Confidence: 1, Provenance: resource.Provenance{SourceRegistrationID: source, RuleVersion: "frozen-sp05-hardware/v1", ObservedAt: now}}
	}
	node := q.CanonicalID
	if err := g.ReplaceExternalSource(g.OwnerEpoch(), source, []resource.Relation{edge(dimm, server, "component_of"), edge(server, node, "hosts")}); err != nil {
		t.Fatal(err)
	}
	q.CanonicalID = dimm
	got, err := g.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	pod := resource.CanonicalID{Domain: "k8s", Tenant: q.Scope.Tenant, Scope: q.Scope.Cluster, APIGroup: "core", Kind: "Pod", StableID: "pod-a"}.String()
	if !slices.Contains(got.DirectlyAffected, server) || !slices.Contains(got.IndirectlyAffected, node) {
		t.Fatalf("physical fault propagation missing: direct=%v indirect=%v", got.DirectlyAffected, got.IndirectlyAffected)
	}
	q.CanonicalID = node
	got, err = g.Query(context.Background(), q)
	if err != nil || !slices.Contains(got.DependencyOnly, server) || !slices.Contains(got.DependencyOnly, dimm) || !slices.Contains(got.DirectlyAffected, pod) {
		t.Fatalf("Node hardware upstream missing: dependencies=%v err=%v", got.DependencyOnly, err)
	}
}
