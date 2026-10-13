package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
)

func TestComparisonComputesDeltaOnlyFromCompleteAuthorizedInputs(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, test := range []struct {
		name, baseline, current, direction string
		delta                              float64
		partial                            bool
		rows                               int
	}{
		{"increase", "100", "400", "increase", 300, false, 3},
		{"decrease to observed zero", "100", "0", "decrease", 100, false, 3},
		{"observed zero baseline", "0", "400", "increase", 400, false, 3},
		{"unchanged", "0", "0", "unchanged", 0, false, 3},
		{"absent baseline", "", "400", "", 0, false, 1},
		{"both absent", "", "", "", 0, false, 0},
		{"partial baseline", "100", "400", "", 0, true, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				calls++
				at, err := time.Parse(time.RFC3339, r.Form.Get("time"))
				if err != nil {
					t.Error(err)
				}
				value := test.current
				partial := false
				if at.Equal(now.Add(-9 * time.Minute)) {
					value = test.baseline
					partial = test.partial
				}
				series := []any{}
				if value != "" {
					series = append(series, map[string]any{"metric": map[string]string{}, "value": []any{at.Unix(), value}})
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "success", "isPartial": partial, "data": map[string]any{"resultType": "vector", "result": series}})
			}))
			defer server.Close()
			tenant := uuid.NewString()
			source := evidence.Binding{SourceType: "victoriametrics", Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, Endpoint: server.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
			proof := Qualification{Operation: "compare", DatasetID: "metrics.pod-memory", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("compare-fixture"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
			adapter, _ := NewVictoria(source, server.Client(), func(context.Context, evidence.Binding) error { return nil }, []Qualification{proof})
			binding := Binding{Subject: "operator", Grant: graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}, AuthorizationRevision: "grant"}}
			intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: proof.DatasetID, Operation: "compare", Metrics: []string{"memory_bytes"}, Dimensions: []string{"comparison"}, Filters: []Filter{}, Sort: []Sort{}, Limit: 50, TimeRange: &QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}, ComparisonTimeRange: &QueryTimeRange{From: now.Add(-10 * time.Minute), To: now.Add(-9 * time.Minute), Timezone: "UTC"}}
			plan, err := adapter.Compile(intent, binding)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Execute(t.Context(), plan)
			if err != nil || len(result.Rows) != test.rows || result.Exact != (test.rows == 3) || calls != 2 || result.ComparisonTimeRange == nil {
				t.Fatalf("comparison: %+v %v calls=%d", result, err, calls)
			}
			if test.rows == 3 && (result.Rows[2].Dimension != test.direction || result.Rows[2].Value != test.delta) {
				t.Fatal("wrong platform delta")
			}
			bad := intent
			bad.ComparisonTimeRange = copyQueryTimeRange(intent.TimeRange)
			if _, err = adapter.Compile(bad, binding); err == nil {
				t.Fatal("overlapping windows admitted")
			}
			bad.ComparisonTimeRange = copyQueryTimeRange(intent.ComparisonTimeRange)
			bad.ComparisonTimeRange.From = bad.ComparisonTimeRange.From.Add(-time.Minute)
			if _, err = adapter.Compile(bad, binding); err == nil {
				t.Fatal("unequal windows admitted")
			}
			authCalls := 0
			adapter.authorize = func(context.Context, evidence.Binding) error {
				authCalls++
				if authCalls >= 3 {
					return ErrDenied
				}
				return nil
			}
			if _, err = adapter.Execute(t.Context(), plan); err == nil {
				t.Fatal("revoked comparison published")
			}
		})
	}
}
