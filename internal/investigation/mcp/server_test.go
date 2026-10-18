package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"ops-platform/internal/analytics"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"strings"
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

func TestAssistantContextReservationKeepsCommittedHistory(t *testing.T) {
	context, err := toolReservation(tools.Definition{Name: "get_assistant_context", Version: "v3"}, json.RawMessage(`{}`))
	if err != nil || context.ResultBytes != 1<<20 || context.RawQueries != 0 || context.ToolCalls != 1 {
		t.Fatal("context envelope still discards a complete follow-up", context, err)
	}
	original := tools.Result{SchemaVersion: "tool-response/v3", Tool: "get_assistant_context", State: "succeeded", Data: map[string]any{"message": "keep frozen question", "history": strings.Repeat("fact-reference ", 6000)}}
	preserved, err := tools.Sanitize(original, int(context.ResultBytes))
	if err != nil || preserved.State != "succeeded" || preserved.Data.(map[string]any)["history"] != original.Data.(map[string]any)["history"] {
		t.Fatal("committed history was truncated", err)
	}
	for _, name := range []string{"query_deepflow", "get_incident_context", "get_resource_context"} {
		source, err := toolReservation(tools.Definition{Name: name}, json.RawMessage(`{}`))
		if err != nil || source.ResultBytes != 64<<10 {
			t.Fatal("source capture bound expanded", name, source, err)
		}
	}
}
