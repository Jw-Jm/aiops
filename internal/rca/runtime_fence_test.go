package rca

import (
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestConfirmedCommitRequiresActualArchiveAndGraph(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	recipe, _ := Builtin("node-failure")
	es, g := frozenEvidence(recipe.Name, clock)
	input := Input{ResourceCanonicalID: es[0].ResourceCanonicalID, Evidence: es, Graph: g, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock}
	preview, err := Evaluate(recipe, input)
	if err != nil || preview.Status != "confirmed" {
		t.Fatal("test does not exercise confirmation")
	}
	defer func() {
		if recover() != nil {
			t.Fatal("confirmed input reached database without archive/graph attestation")
		}
	}()
	_, err = (Repository{}).Commit(t.Context(), uuid.New(), uuid.NewString(), "worker", "attestation-missing", 1, 0, nil, recipe, input)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("unattested confirmation accepted: %v", err)
	}
}
