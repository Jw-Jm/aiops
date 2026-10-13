package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
)

func TestTrendAdmissionBoundsAndSourceResponses(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	window := &QueryTimeRange{From: now.Add(-3 * time.Minute), To: now, Timezone: "Asia/Shanghai"}
	point := func(offset int, value string) []any {
		return []any{window.From.Add(time.Duration(offset) * time.Second).Unix(), value}
	}
	for _, test := range []struct {
		name     string
		points   [][]any
		labels   map[string]string
		status   string
		rejected bool
	}{
		{"complete", [][]any{point(180, "400"), point(0, "100"), point(60, "200"), point(120, "300")}, map[string]string{}, "complete", false},
		{"gap stays absent", [][]any{point(0, "100"), point(180, "400")}, map[string]string{}, "partial", false},
		{"empty is not zero", [][]any{}, map[string]string{}, "no_data", false},
		{"off grid", [][]any{point(61, "100")}, map[string]string{}, "", true},
		{"duplicate", [][]any{point(0, "100"), point(0, "100")}, map[string]string{}, "", true},
		{"outside window", [][]any{point(240, "100")}, map[string]string{}, "", true},
		{"nan", [][]any{point(0, "NaN")}, map[string]string{}, "", true},
		{"negative", [][]any{point(0, "-1")}, map[string]string{}, "", true},
		{"unexpected labels", [][]any{point(0, "100")}, map[string]string{"namespace": "private"}, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				if r.URL.Path != "/api/v1/query_range" || r.Form.Get("step") != "60" || r.Form.Get("deny_partial_response") != "1" || !strings.Contains(r.Form.Get("query"), `namespace=~"apps"`) || strings.Contains(r.Form.Get("query"), "by (time)") {
					t.Errorf("unbounded query: %s %v", r.URL.Path, r.Form)
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": test.labels, "values": test.points}}}})
			}))
			defer server.Close()
			tenant := uuid.NewString()
			source := evidence.Binding{SourceType: "victoriametrics", Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, Endpoint: server.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps", "private"}}}}
			proof := Qualification{Operation: "trend", DatasetID: "metrics.pod-memory", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("trend-fixture"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
			adapter, _ := NewVictoria(source, server.Client(), func(context.Context, evidence.Binding) error { return nil }, []Qualification{proof})
			binding := Binding{Subject: "operator", Grant: graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}, AuthorizationRevision: "grant"}}
			intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: proof.DatasetID, Operation: "trend", Metrics: []string{"memory_bytes"}, Dimensions: []string{"time"}, Filters: []Filter{}, Sort: []Sort{}, Limit: 50, TimeRange: window, StepSeconds: 60}
			plan, err := adapter.Compile(intent, binding)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Execute(t.Context(), plan)
			if test.rejected {
				if err == nil {
					t.Fatal("invalid source point accepted")
				}
				return
			}
			if err != nil || result.Status != test.status || result.Exact != (test.status == "complete") || len(result.Rows) != len(test.points) || result.StepSeconds != 60 {
				t.Fatalf("result %+v %v", result, err)
			}
			if len(result.Rows) > 0 && result.Rows[0].Dimension != window.From.Format(time.RFC3339) {
				t.Fatal("trend reordered by value")
			}
			proof.Operation = ""
			legacy, _ := NewVictoria(source, server.Client(), func(context.Context, evidence.Binding) error { return nil }, []Qualification{proof})
			if _, err = legacy.Compile(intent, binding); err == nil {
				t.Fatal("aggregate proof admitted trend")
			}
			for _, change := range []string{"too_many_points", "wrong_dimension", "missing_step", "value_sort", "subsecond"} {
				bad := intent
				bad.TimeRange = copyQueryTimeRange(window)
				switch change {
				case "too_many_points":
					bad.Limit = 3
				case "wrong_dimension":
					bad.Dimensions = []string{"namespace"}
				case "missing_step":
					bad.StepSeconds = 0
				case "value_sort":
					bad.Sort = []Sort{{Field: "memory_bytes", Direction: "desc"}}
				case "subsecond":
					bad.TimeRange.From = bad.TimeRange.From.Add(time.Millisecond)
				}
				if _, err = adapter.Compile(bad, binding); err == nil {
					t.Fatalf("unbounded trend admitted: %s", change)
				}
			}
		})
	}
}
