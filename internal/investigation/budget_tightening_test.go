package investigation

import (
	"errors"
	"testing"
)

func TestTightenBudgetNeverWidens(t *testing.T) {
	base := DefaultBudget()
	// A client may only narrow the admission.
	narrowed, err := TightenBudget(base, BudgetTightening{ToolCalls: 5, DurationSeconds: 120})
	if err != nil {
		t.Fatal(err)
	}
	if narrowed.ToolCalls != 5 || narrowed.DurationSeconds != 120 {
		t.Fatalf("tightening was not applied: %+v", narrowed)
	}
	if narrowed.RawQueries != base.RawQueries || narrowed.ModelRequests != base.ModelRequests {
		t.Fatalf("untouched dimensions changed: %+v", narrowed)
	}
	// A request between the admitted dimension and the platform maximum is
	// clamped back to the admitted dimension.
	admitted := base
	admitted.ToolCalls = 5
	admitted.ModelRequests = 2
	wide, err := TightenBudget(admitted, BudgetTightening{ToolCalls: 20, ModelRequests: 6})
	if err != nil {
		t.Fatal(err)
	}
	if wide.ToolCalls != 5 || wide.ModelRequests != 2 {
		t.Fatalf("a client widened the admission: %+v", wide)
	}
	// A zero dimension is "not requested", never a zero limit.
	unset, err := TightenBudget(base, BudgetTightening{})
	if err != nil {
		t.Fatal(err)
	}
	if unset.ToolCalls != base.ToolCalls || unset.DurationSeconds != base.DurationSeconds {
		t.Fatalf("an empty tightening changed the budget: %+v", unset)
	}
}

func TestTightenBudgetRejectsInvalidInput(t *testing.T) {
	base := DefaultBudget()
	if _, err := TightenBudget(base, BudgetTightening{ToolCalls: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a negative limit was accepted: %v", err)
	}
	if _, err := TightenBudget(base, BudgetTightening{DurationSeconds: -30}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a negative duration was accepted: %v", err)
	}
	if _, err := TightenBudget(base, BudgetTightening{ToolCalls: base.ToolCalls + 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a limit above the platform maximum was accepted: %v", err)
	}
	if _, err := TightenBudget(Budget{}, BudgetTightening{ToolCalls: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an invalid base was accepted: %v", err)
	}
}
