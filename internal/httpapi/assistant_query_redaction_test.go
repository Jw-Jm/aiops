package httpapi

import (
	"encoding/json"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"strings"
	"testing"
)

func TestLogSampleDigestSurvivesMCPDisclosure(t *testing.T) {
	var result analytics.QueryResult
	if json.Unmarshal([]byte(`{"schemaVersion":"query-result/v1","datasetId":"logs.records","planDigest":"plan","rows":[],"samples":[{"timestamp":"2026-11-09T00:00:00Z","message":" {\"level\":\"info\", \"credential\":\"private-value\", \"msg\":\"started\"} "}]}`), &result) != nil {
		t.Fatal("fixture")
	}
	stored, err := buildSanitizedQueryResult(result)
	if err != nil {
		t.Fatal(err)
	}
	disclosed, err := tools.Sanitize(tools.Result{Data: map[string]any{"storedQueryResult": stored}}, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(disclosed.Data)
	if strings.Contains(string(raw), "private-value") || !strings.Contains(string(raw), "REDACTED") {
		t.Fatalf("sample was not redacted: %s", raw)
	}
	var envelope struct {
		Stored investigation.StoredQueryResult `json:"storedQueryResult"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("disclosure")
	}
	digest := envelope.Stored.ContentDigest
	envelope.Stored.ContentDigest = ""
	if graph.ScopeDigest(envelope.Stored) != digest || digest != stored.ContentDigest {
		t.Fatal("MCP changed the representation after its immutable digest was computed")
	}
	if !strings.Contains(string(rawJSON(result)), "private-value") {
		t.Fatal("changed original source result")
	}
}
