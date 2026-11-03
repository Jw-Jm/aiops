package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestMetricRequestRejectsRawQueriesAndNonMetricOperations(t *testing.T) {
	valid := map[string]any{"canonicalId": "pod", "intent": map[string]any{"schemaVersion": "query-intent/v1", "datasetId": "metrics.pod-memory", "operation": "trend", "metrics": []string{"memory_bytes"}, "dimensions": []string{"time"}, "filters": []any{}, "sort": []any{}, "limit": 20, "stepSeconds": 60, "timeRange": map[string]any{"from": "2026-11-03T00:00:00Z", "to": "2026-11-03T00:01:00Z", "timezone": "UTC"}}}
	raw, _ := json.Marshal(valid)
	if _, err := decodeMetricRequest(raw); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"promql", "dataset", "operation", "trailing"} {
		var q map[string]any
		json.Unmarshal(raw, &q)
		switch change {
		case "promql":
			q["query"] = "arbitrary"
		case "dataset":
			q["intent"].(map[string]any)["datasetId"] = "logs.records"
		case "operation":
			q["intent"].(map[string]any)["operation"] = "samples"
		}
		b, _ := json.Marshal(q)
		if change == "trailing" {
			b = append(b, []byte("{}")...)
		}
		if _, err := decodeMetricRequest(b); err == nil {
			t.Fatal("accepted unsafe request", change)
		}
	}
}
func TestMetricsRequiresNativeSessionBeforeUsingOwner(t *testing.T) {
	h := SP04Handlers{}
	w := httptest.NewRecorder()
	h.serveMetrics(w, httptest.NewRequest("GET", "/api/v1/metrics/capabilities?clusterUid=cluster", nil))
	if w.Code != 403 {
		t.Fatalf("native proof guard: %d", w.Code)
	}
}
