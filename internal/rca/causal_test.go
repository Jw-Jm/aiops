package rca

import (
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestRecipeCannotConfirmFromDisconnectedOrDerivedFacts(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, name := range []string{"dimm-failure", "pvc-csi-failure", "node-failure"} {
		t.Run(name, func(t *testing.T) {
			recipe, _ := Builtin(name)
			es, g := frozenEvidence(name, clock)
			input := Input{ResourceCanonicalID: es[0].ResourceCanonicalID, Evidence: es, Graph: g, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock}
			// A single edge touching the subject is not the frozen complete diagnostic
			// path; it can be an unrelated/wrong-kind resource with invented provenance.
			input.Graph.Edges = []resource.Relation{{From: "unrelated", To: es[0].ResourceCanonicalID, Kind: map[string]string{"dimm-failure": "component_of", "pvc-csi-failure": "mounts_pvc", "node-failure": "scheduled_on"}[name]}}
			out, err := Evaluate(recipe, input)
			if err != nil || out.Status == "confirmed" {
				t.Fatalf("invented path confirmed: %+v %v", out, err)
			}
			input.Graph = g
			input.Evidence = append([]evidence.Evidence(nil), es...)
			input.Evidence[1].DerivationEvidenceRefs = []string{es[0].EvidenceID}
			out, err = Evaluate(recipe, input)
			if err != nil || out.Status == "confirmed" {
				t.Fatalf("derivation counted independent: %+v %v", out, err)
			}
			input.Evidence = es
			input.EvaluatedAt = clock.Add(time.Hour)
			out, err = Evaluate(recipe, input)
			if err != nil || out.Status == "confirmed" {
				t.Fatalf("frozen query window concealed current expiry: %+v %v", out, err)
			}
			input.Graph = graph.Result{Freshness: "fresh", Partial: true}
			out, _ = Evaluate(recipe, input)
			if out.Status == "confirmed" {
				t.Fatal("partial")
			}
		})
	}
}
