package investigation

import (
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestOperationalRequestKindsAndExplicitNodeScope(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	scope := graph.Scope{Tenant: uuid.NewString(), Cluster: "cluster", Namespaces: []string{"apps"}}
	r := FaultChainRequest{SchemaVersion: "fault-chain-request/v2", Fault: analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}, MaxEdges: 4}
	for _, kind := range []string{"Pod", "PersistentVolumeClaim", "Node", "VirtualMachine"} {
		r.TargetCanonicalID = resource.CanonicalID{Domain: "k8s", Tenant: scope.Tenant, Scope: scope.Cluster, APIGroup: "core", Kind: kind, StableID: "uid"}.String()
		if r.Valid(scope, "UTC", now) != (kind == "Pod" || kind == "PersistentVolumeClaim") {
			t.Fatal(kind)
		}
		scope.ClusterScoped = true
		if r.Valid(scope, "UTC", now) != (kind != "VirtualMachine") {
			t.Fatal("cluster", kind)
		}
		scope.ClusterScoped = false
	}
}
func TestResourceHistoryRulesHonorHalfOpenWindowAndNormalCounterevidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := FaultChainRequest{Fault: analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}}
	ready := func(at time.Time, status string) ResourceObservation {
		return ResourceObservation{ObservedAt: at, Projection: map[string]any{"kind": "Node", "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": status}}}}}
	}
	h := ResourceHistory{CanonicalID: "uid", Records: []ResourceObservation{ready(r.Fault.From, "True"), ready(r.Fault.To, "False")}, MissingEvidence: []string{"history_gap"}}
	step := uuid.New()
	out := AssessResourceHistory(r, step, h)
	if len(out) != 1 || out[0].RuleID != "K8S/ready-counterevidence" || len(out[0].Conflicts) != 1 || len(out[0].Supports) != 0 {
		t.Fatal(out)
	}
	h.Records = nil
	if len(AssessResourceHistory(r, step, h)) != 0 {
		t.Fatal("missing history became normal")
	}
}
