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
	for _, name := range []string{"dimm-failure", "pvc-csi-failure", "node-failure", "pod-image-pull-failure", "service-no-endpoints", "pod-crashloop-failure", "pod-oom-failure", "pod-probe-failure", "deployment-rollout-stuck", "pod-scheduling-failure"} {
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
						e.Data = []byte(map[string]string{
							"dimm-failure":              `{"health":"OK","phase":"Bound","ready":true}`,
							"pvc-csi-failure":           `{"health":"OK","phase":"Bound","ready":true}`,
							"node-failure":              `{"health":"OK","phase":"Bound","ready":true}`,
							"pod-image-pull-failure":    `{"nativeKind":"Pod","reason":"ImagePullFailure","imagePullFailing":false}`,
							"service-no-endpoints":      `{"nativeKind":"EndpointSlice","reason":"ServiceNoEndpoints","readyEndpoints":2}`,
							"pod-crashloop-failure":     `{"nativeKind":"Pod","reason":"CrashLoopBackOff","crashLoopFailing":false}`,
							"pod-oom-failure":           `{"nativeKind":"Pod","reason":"OOMKilled","oomKilled":false}`,
							"pod-probe-failure":         `{"nativeKind":"Pod","reason":"ProbeFailure","probeFailing":false}`,
							"deployment-rollout-stuck":  `{"nativeKind":"Deployment","reason":"RolloutStuck","rolloutStuck":false}`,
							"pod-scheduling-failure":    `{"nativeKind":"Pod","reason":"Unschedulable","unschedulable":false}`,
						}[name])
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
					// Intermittent faults (crash loop, OOM, probe) alternate by
					// nature: a passing window settles the occurrence lifecycle
					// without refuting the condition, so conflict admission does
					// not apply to them.
					intermittent := name == "pod-crashloop-failure" || name == "pod-oom-failure" || name == "pod-probe-failure"
					actual, err := Evaluate(r, input)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "duplicate" {
						if actual.Status != out.Status || len(actual.Bundle.Ranked) != len(out.Bundle.Ranked) {
							t.Fatal("duplicate weighting")
						}
					} else if actual.Status == "confirmed" && !(mode == "conflict" && intermittent) {
						// For intermittent faults the conflict-mode input is
						// equivalent to the valid one: the settle observation is
						// neutral, so confirmation stays correct and safe.
						t.Fatalf("unsafe confirmed %s", mode)
					}
					if mode == "conflict" && !intermittent && (len(actual.Bundle.Contradicting) == 0 || len(actual.Bundle.Conflicts) == 0) {
						t.Fatal("healthy evidence did not exercise actual conflict admission")
					}
				})
			}
		})
	}
}
func frozenEvidence(name string, clock time.Time) ([]evidence.Evidence, graph.Result) {
	payloads := map[string][]string{"dimm-failure": {`{"health":"Critical"}`, `{"uncorrectableECC":true}`}, "pvc-csi-failure": {`{"phase":"Pending"}`, `{"reason":"ProvisioningFailed","reportingController":"csi.fixture.example"}`}, "node-failure": {`{"ready":false}`, `{"reason":"KernelOops"}`}, "pod-image-pull-failure": {`{"nativeKind":"Pod","reason":"ImagePullFailure","imagePullFailing":true}`, `{"nativeKind":"Event","reason":"ImagePullError","reportingController":"kubelet"}`}, "service-no-endpoints": {`{"nativeKind":"EndpointSlice","reason":"ServiceNoEndpoints","readyEndpoints":0}`, `{"nativeKind":"Pod","reason":"ImagePullFailure","imagePullFailing":true}`}, "pod-crashloop-failure": {`{"nativeKind":"Pod","reason":"CrashLoopBackOff","crashLoopFailing":true}`, `{"nativeKind":"Event","reason":"ContainerBackOffError","reportingController":"kubelet"}`}, "pod-oom-failure": {`{"nativeKind":"Pod","reason":"OOMKilled","oomKilled":true,"lastExitCode":137}`}, "pod-probe-failure": {`{"nativeKind":"Event","reason":"ProbeFailure","reportingController":"kubelet","message":"Readiness probe failed: connection refused"}`}, "deployment-rollout-stuck": {`{"nativeKind":"Deployment","reason":"RolloutStuck","rolloutStuck":true}`, `{"nativeKind":"Pod","reason":"ImagePullFailure","imagePullFailing":true}`}, "pod-scheduling-failure": {`{"nativeKind":"Pod","reason":"Unschedulable","unschedulable":true}`, `{"nativeKind":"Event","reason":"FailedScheduling","reportingController":"default-scheduler","message":"0/1 nodes are available: 1 node(s) didn't have free ports for the requested pod ports."}`}}
	out := []evidence.Evidence{}
	for index, data := range payloads[name] {
		out = append(out, evidence.Evidence{EvidenceID: []string{"e-health", "e-cause"}[index], ResourceCanonicalID: "root", SourceSystem: map[string]string{"dimm-failure": "redfish", "pvc-csi-failure": "kubernetes", "node-failure": "kubernetes", "pod-image-pull-failure": "kubernetes", "service-no-endpoints": "kubernetes", "pod-crashloop-failure": "kubernetes", "pod-oom-failure": "kubernetes", "pod-probe-failure": "kubernetes", "deployment-rollout-stuck": "kubernetes", "pod-scheduling-failure": "kubernetes"}[name], Type: []string{"resource_state", "event"}[index], QueryTemplateVersion: "sp05-signal/v1", TimeReliable: true, ObservedFrom: clock, ObservedTo: clock, EvaluatedAt: clock, IndependenceGroup: []string{"resource", "event"}[index], ReplayState: "archived_verified", ContentDigest: evidence.Digest([]byte(data)), Data: []byte(data)})
	}
	if name == "node-failure" {
		out[1].SourceSystem = "victorialogs"
		out[1].Type = "log"
		out[1].QueryTemplateVersion = "node-kernel-logs/v1"
		out[1].Data, _ = json.Marshal([]map[string]string{{"_TRANSPORT": "kernel", "_time": clock.Format(time.RFC3339Nano), "_msg": "BUG: unable to handle kernel NULL pointer dereference at 0000"}})
		out[1].ContentDigest = evidence.Digest(out[1].Data)
	}
	primaryKind := map[string]string{"dimm-failure": "DIMM", "pvc-csi-failure": "PersistentVolumeClaim", "node-failure": "Node", "pod-image-pull-failure": "Pod", "service-no-endpoints": "Service", "pod-crashloop-failure": "Pod", "pod-oom-failure": "Pod", "pod-probe-failure": "Pod", "deployment-rollout-stuck": "Deployment", "pod-scheduling-failure": "Pod"}[name]
	canonical := func(kind, uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: kind, StableID: uid}.String()
	}
	primary := canonical(primaryKind, "root")
	for n := range out {
		out[n].ResourceCanonicalID = primary
	}
	backend := canonical("Pod", "pod-b")
	if name == "service-no-endpoints" || name == "deployment-rollout-stuck" {
		// The backend/blocking cause vote lives on the related Pod, not the target.
		out[1].ResourceCanonicalID = backend
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
	case "pod-image-pull-failure", "pod-crashloop-failure", "pod-oom-failure", "pod-probe-failure":
		g.Nodes = append(g.Nodes, resource.Entity{CanonicalID: node, Kind: "Node"})
		g.Edges = []resource.Relation{edge(primary, node, "scheduled_on")}
	case "deployment-rollout-stuck":
		replicaset := canonical("ReplicaSet", "rs-a")
		g.Nodes = append(g.Nodes, resource.Entity{CanonicalID: replicaset, Kind: "ReplicaSet"}, resource.Entity{CanonicalID: backend, Kind: "Pod", Namespace: "apps"})
		g.Edges = []resource.Relation{edge(backend, replicaset, "managed_by"), edge(replicaset, primary, "managed_by")}
	case "pod-scheduling-failure":
		// An unscheduled pod has no topology anchor; facts alone confirm.
	case "service-no-endpoints":
		g.Nodes = append(g.Nodes, resource.Entity{CanonicalID: backend, Kind: "Pod", Namespace: "apps"})
		selector := edge(primary, backend, "selects_pod")
		selector.Confidence = 0.95
		g.Edges = []resource.Relation{selector}
	}
	return out, g
}
