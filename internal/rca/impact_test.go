package rca

import (
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"slices"
	"testing"
	"time"
)

func TestDIMMFixedPlanComposesRealKernelAndHonorsNamespaceScope(t *testing.T) {
	now := time.Now().Add(-time.Second)
	ctx := context.Background()
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := graph.New("tenant-a", "cluster-a", "worker-a", []kubernetes.GVR{gvr})
	g.SetOwner(1, now.Add(time.Minute))
	native := func(kind, name, uid, ns string, spec map[string]any) unstructured.Unstructured {
		return unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": kind, "metadata": map[string]any{"name": name, "uid": uid, "namespace": ns, "resourceVersion": "1"}, "spec": spec}}
	}
	if err := g.Replace(ctx, 1, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{native("Node", "node-a", "node-a", "", map[string]any{}), native("Pod", "pod-a", "pod-a", "apps", map[string]any{"nodeName": "node-a"})}, State: kubernetes.GVRState{LastListCompletedAt: now, LastConnectivityProbeAt: now, WatchConnected: true, WatchContinuous: true}}); err != nil {
		t.Fatal(err)
	}
	id := func(domain, kind, uid string) string {
		return resource.CanonicalID{Domain: domain, Tenant: "tenant-a", Scope: "cluster-a", APIGroup: map[string]string{"hardware": "redfish", "k8s": "core"}[domain], Kind: kind, StableID: uid}.String()
	}
	dimm, server, node, pod := id("hardware", "DIMM", "server/DIMM1"), id("hardware", "PhysicalServer", "server"), id("k8s", "Node", "node-a"), id("k8s", "Pod", "pod-a")
	if err := g.ReplaceHardware(ctx, "hardware", []resource.Entity{{CanonicalID: dimm, Kind: "DIMM", UpdatedAt: now, Attributes: map[string]any{}}, {CanonicalID: server, Kind: "PhysicalServer", UpdatedAt: now, Attributes: map[string]any{}}}, now); err != nil {
		t.Fatal(err)
	}
	edge := func(from, to, kind string) resource.Relation {
		return resource.Relation{From: from, To: to, Kind: kind, Confidence: 1, ObservedAt: now, ValidFrom: now, TTLSeconds: 600, Provenance: resource.Provenance{SourceRegistrationID: "hardware", RuleVersion: "fixed-input/v1", ObservedAt: now}}
	}
	if err := g.ReplaceExternalSource(1, "hardware", []resource.Relation{edge(dimm, server, "component_of"), edge(server, node, "hosts")}); err != nil {
		t.Fatal(err)
	}
	r, _ := Builtin("dimm-failure")
	q := graph.Query{CanonicalID: dimm, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", ClusterScoped: true, Namespaces: []string{"apps"}}}
	got, err := QueryImpact(ctx, g, r, q)
	if err != nil || got.Partial || !slices.Contains(got.DirectlyAffected, server) || !slices.Contains(got.IndirectlyAffected, node) || !slices.Contains(got.IndirectlyAffected, pod) || slices.Contains(got.DirectlyAffected, pod) || got.Budgets.MaxDepth != 2 {
		t.Fatalf("fixed plan: %+v %v", got, err)
	}
	q.Scope.Namespaces = nil
	got, err = QueryImpact(ctx, g, r, q)
	if err != nil || !got.Partial || slices.ContainsFunc(got.Nodes, func(n resource.Entity) bool { return n.CanonicalID == pod }) {
		t.Fatalf("scope leak: %+v %v", got, err)
	}
}
