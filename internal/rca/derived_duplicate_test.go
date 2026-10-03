package rca

import (
	"testing"
	"time"
)

func TestDerivedDuplicateDoesNotInvalidatePresentNativeProof(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	recipe, _ := Builtin("node-failure")
	es, g := frozenEvidence(recipe.Name, clock)
	derived := es[1]
	derived.EvidenceID = "derived-kernel-candidate"
	derived.DerivationEvidenceRefs = []string{es[1].EvidenceID}
	input := Input{ResourceCanonicalID: es[0].ResourceCanonicalID, Evidence: append(es, derived), Graph: g, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock}
	out, err := Evaluate(recipe, input)
	if err != nil || out.Status != "confirmed" || len(out.Bundle.Supporting) != 2 || len(out.Bundle.Ranked) != 2 {
		t.Fatalf("derived duplicate invalidated or reweighted actual native proof: %+v %v", out, err)
	}
	input.Evidence = input.Evidence[0:1]
	input.Evidence = append(input.Evidence, derived)
	out, err = Evaluate(recipe, input)
	if err != nil || out.Status == "confirmed" {
		t.Fatalf("derived evidence substituted for absent native proof: %+v %v", out, err)
	}
}
