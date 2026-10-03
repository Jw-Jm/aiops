package rca

import (
	"testing"
	"time"
)

func TestFutureRequiredEvidenceCannotConfirm(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	r, _ := Builtin("node-failure")
	es, g := frozenEvidence("node-failure", clock)
	es[0].ObservedTo = clock.Add(10 * time.Minute)
	out, err := Evaluate(r, Input{ResourceCanonicalID: es[0].ResourceCanonicalID, Evidence: es, Graph: g, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock})
	if err != nil || out.Status == "confirmed" || !out.Partial {
		t.Fatalf("future evidence confirmed: %+v %v", out, err)
	}
}
