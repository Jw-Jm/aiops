package httpapi

import (
	"net/http/httptest"
	"ops-platform/internal/investigation"
	"strings"
	"testing"
)

func TestInvestigationBudgetHTTPContract(t *testing.T) {
	w := httptest.NewRecorder()
	investigationError(w, investigation.ErrBudget, "request-id")
	if w.Code != 429 {
		t.Fatalf("budget status=%d, want 429", w.Code)
	}
}

func TestInvestigationIdempotencyConflictHTTPContract(t *testing.T) {
	w := httptest.NewRecorder()
	investigationError(w, investigation.ErrConflict, "request-id")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("conflict response=%d %s", w.Code, w.Body.String())
	}
}
