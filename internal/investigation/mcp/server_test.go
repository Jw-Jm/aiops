package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"ops-platform/internal/analytics"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"testing"
	"time"
)

func TestExpandedReservationCannotFitOneRemainingRawQuery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	q := analytics.QueryIntent{DatasetID: "logs.records", Operation: "trend", StepSeconds: 60, TimeRange: &analytics.QueryTimeRange{From: now.Add(-49 * time.Minute), To: now}}
	raw, _ := json.Marshal(map[string]any{"intent": q})
	reserve, err := toolReservation(tools.Definition{Name: "execute_analysis_query", RawQuery: true}, raw)
	if err != nil || reserve.RawQueries != 50 {
		t.Fatal(reserve, err)
	}
	limit := investigation.DefaultBudget().Usage
	limit.RawQueries = 1
	if reserve.Within(limit) {
		t.Fatal("expanded query fits a one-query budget")
	}
	if _, err := toolReservation(tools.Definition{Name: "execute_analysis_query", RawQuery: true}, json.RawMessage(`{"intent":{}}`)); err == nil {
		t.Fatal("unknown expansion accepted")
	}
}
func TestGatewayRejectsUnauthenticatedRequestsBeforePersistence(t *testing.T) {
	h, err := (Gateway{}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/mcp", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
