package rca

import (
	"encoding/json"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"os"
	"testing"
	"time"
)

func TestRecipeFrozenGoldenConfirmation(t *testing.T) {
	for _, name := range []string{"dimm-failure", "pvc-csi-failure", "node-failure"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("../../test/fixtures/incidents/" + name + "/v1/expected.json")
			if err != nil {
				t.Fatal(err)
			}
			var expected struct {
				Expected struct {
					CandidateType string `json:"candidateType"`
					RCAStatus     string `json:"rcaStatus"`
					Missing       string `json:"missingStatus"`
					Conflict      string `json:"conflictStatus"`
					Degraded      string `json:"degradedStatus"`
				}
			}
			if json.Unmarshal(raw, &expected) != nil {
				t.Fatal("frozen fixture")
			}
			r, err := Builtin(name)
			if err != nil {
				t.Fatal(err)
			}
			clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
			es, g := frozenEvidence(name, clock)
			out, err := Evaluate(r, Input{ResourceCanonicalID: es[0].ResourceCanonicalID, Evidence: es, Graph: g, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock})
			if err != nil || out.Status != expected.Expected.RCAStatus || out.Candidates[0].Type != expected.Expected.CandidateType {
				t.Fatalf("valid %+v %v", out, err)
			}
			for _, mode := range []string{"missing", "conflict", "degraded", "time-unreliable", "duplicate", "expired", "cpu-high", "recent-change"} {
				t.Run(mode, func(t *testing.T) {
					input := Input{ResourceCanonicalID: es[0].ResourceCanonicalID, Evidence: append([]evidence.Evidence(nil), es...), Graph: g, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock}
					switch mode {
					case "missing":
						input.Evidence = input.Evidence[:1]
					case "conflict":
						e := es[0]
						e.EvidenceID = "conflict"
						e.Data = []byte(`{"health":"OK","phase":"Bound","ready":true}`)
						e.ContentDigest = evidence.Digest(e.Data)
						e.IndependenceGroup = "independent-healthy-observation"
						input.Evidence = append(input.Evidence, e)
					case "degraded":
						input.Graph.Partial = true
						input.Graph.DegradedSources = []string{"required-source"}
					case "time-unreliable":
						input.Evidence[0].TimeReliable = false
					case "expired":
						input.Evidence[0].ObservedTo = clock.Add(-time.Hour)
					case "duplicate":
						input.Evidence = append(input.Evidence, es...)
					case "cpu-high", "recent-change":
						input.Evidence = []evidence.Evidence{{EvidenceID: "counterexample", TimeReliable: true, ObservedTo: clock, ReplayState: "archived_verified", Data: []byte(`{"cpu":99,"recentChange":true}`)}}
					}
					actual, err := Evaluate(r, input)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "duplicate" {
						if actual.Status != out.Status || len(actual.Bundle.Ranked) != len(out.Bundle.Ranked) {
							t.Fatal("duplicate weighting")
						}
					} else if actual.Status == "confirmed" {
						t.Fatalf("unsafe confirmed %s", mode)
					}
					if mode == "conflict" && (len(actual.Bundle.Contradicting) == 0 || len(actual.Bundle.Conflicts) == 0) {
						t.Fatal("healthy evidence did not exercise actual conflict admission")
					}
				})
			}
		})
	}
}
func frozenEvidence(name string, clock time.Time) ([]evidence.Evidence, graph.Result) {
	payloads := map[string][]string{"dimm-failure": {`{"health":"Critical"}`, `{"uncorrectableECC":true}`}, "pvc-csi-failure": {`{"phase":"Pending"}`, `{"reason":"ProvisioningFailed","reportingController":"csi.fixture.example"}`}, "node-failure": {`{"ready":false}`, `{"reason":"KernelOops"}`}}
	out := []evidence.Evidence{}
	for index, data := range payloads[name] {
		out = append(out, evidence.Evidence{EvidenceID: []string{"e-health", "e-cause"}[index], ResourceCanonicalID: "root", SourceSystem: map[string]string{"dimm-failure": "redfish", "pvc-csi-failure": "kubernetes", "node-failure": "kubernetes"}[name], Type: []string{"resource_state", "event"}[index], QueryTemplateVersion: "sp05-signal/v1", TimeReliable: true, ObservedFrom: clock, ObservedTo: clock, EvaluatedAt: clock, IndependenceGroup: []string{"resource", "event"}[index], ReplayState: "archived_verified", ContentDigest: evidence.Digest([]byte(data)), Data: []byte(data)})
	}
	if name == "node-failure" {
		out[1].SourceSystem = "victorialogs"
		out[1].Type = "log"
		out[1].QueryTemplateVersion = "node-kernel-logs/v1"
		out[1].Data, _ = json.Marshal([]map[string]string{{"_TRANSPORT": "kernel", "_time": clock.Format(time.RFC3339Nano), "_msg": "BUG: unable to handle kernel NULL pointer dereference at 0000"}})
		out[1].ContentDigest = evidence.Digest(out[1].Data)
	}
	primaryKind := map[string]string{"dimm-failure": "DIMM", "pvc-csi-failure": "PersistentVolumeClaim", "node-failure": "Node"}[name]
	canonical := func(kind, uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: kind, StableID: uid}.String()
	}
	primary := canonical(primaryKind, "root")
	for n := range out {
		out[n].ResourceCanonicalID = primary
	}
	node := canonical("Node", "node-a")
	pod := canonical("Pod", "pod-a")
	server := canonical("PhysicalServer", "server-a")
	sc := canonical("StorageClass", "sc-a")
	g := graph.Result{Freshness: "fresh", DirectlyAffected: []string{pod}, Nodes: []resource.Entity{{CanonicalID: primary, Kind: primaryKind}, {CanonicalID: pod, Kind: "Pod", Namespace: "apps"}}}
	edge := func(from, to, kind string) resource.Relation {
		return resource.Relation{From: from, To: to, Kind: kind, Confidence: 1, ObservedAt: clock, ValidFrom: clock, Provenance: resource.Provenance{SourceRegistrationID: "00000000-0000-0000-0000-000000000001", RuleVersion: "fixture/v1", ObservedAt: clock}}
	}
	switch name {
	case "node-failure":
		g.Edges = []resource.Relation{edge(pod, primary, "scheduled_on")}
	case "pvc-csi-failure":
		g.Nodes = append(g.Nodes, resource.Entity{CanonicalID: sc, Kind: "StorageClass"})
		driver := canonical("CSIDriver", "driver-a")
		g.Nodes = append(g.Nodes, resource.Entity{CanonicalID: driver, Kind: "CSIDriver", Name: "csi.fixture.example"})
		g.Edges = []resource.Relation{edge(pod, primary, "mounts_pvc"), edge(primary, sc, "uses_storage_class"), edge(sc, driver, "provisioned_by_csi_driver")}
	case "dimm-failure":
		g.Nodes = append(g.Nodes, resource.Entity{CanonicalID: server, Kind: "PhysicalServer"}, resource.Entity{CanonicalID: node, Kind: "Node"})
		g.Edges = []resource.Relation{edge(primary, server, "component_of"), edge(server, node, "hosts"), edge(pod, node, "scheduled_on")}
	}
	return out, g
}
