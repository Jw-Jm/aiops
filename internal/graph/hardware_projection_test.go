package graph

import (
	"context"
	"encoding/json"
	"ops-platform/internal/contract"
	"ops-platform/internal/inspection/hardware"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestHardwareProjectionAcceptsTypedMetal3Model(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("typed upstream model panicked in Graph publication: %v", recovered)
		}
	}()
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "worker", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	id := resource.CanonicalID{Domain: "hardware", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "redfish", Kind: "PhysicalServer", StableID: "550e8400-e29b-41d4-a716-446655440000"}.String()
	entity := resource.Entity{CanonicalID: id, Kind: "PhysicalServer", Name: "server", UpdatedAt: time.Now(), Attributes: map[string]any{"health": "normal", "metal3": hardware.Metal3Inventory{}}}
	if err := g.ReplaceHardware(context.Background(), "bmc-1", []resource.Entity{entity}, entity.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	result, err := g.Query(context.Background(), Query{QueryKind: "entity", CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 5, MaxEdges: 5, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", ClusterScoped: true}})
	if err != nil || len(result.Nodes) != 1 {
		t.Fatalf("hardware missing: %+v %v", result, err)
	}
}

func TestHardwareCapacityFitsVersionedEntityContract(t *testing.T) {
	const tenant = "f3e05c72-03c7-46ce-9752-aa217368a5da"
	g := New(tenant, "cluster-a", "worker", []kubernetes.GVR{{Version: "v1", Resource: "pods"}})
	g.SetOwner(1, time.Now().Add(time.Minute))
	id := resource.CanonicalID{Domain: "hardware", Tenant: tenant, Scope: "cluster-a", APIGroup: "redfish", Kind: "Disk", StableID: "550e8400-e29b-41d4-a716-446655440000/disk1"}.String()
	entity := resource.Entity{CanonicalID: id, Kind: "Disk", Name: "disk1", UpdatedAt: time.Now(), Attributes: map[string]any{"CapacityBytes": int64(1000000000000)}}
	if err := g.ReplaceHardware(context.Background(), "003058b8-ddd8-4f0a-9bd8-e719332de1ae", []resource.Entity{entity}, entity.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	result, err := g.Query(context.Background(), Query{QueryKind: "entity", CanonicalID: id, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 5, MaxEdges: 5, Scope: Scope{Tenant: tenant, Cluster: "cluster-a", ClusterScoped: true}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate("https://ops.local/schemas/resource-graph/v2", raw); err != nil {
		t.Fatalf("realistic hardware quantity rejected: %v", err)
	}
}
