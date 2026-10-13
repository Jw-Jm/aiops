package integration

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
)

func TestAssistantVictoriaNodeReadyTrend(t *testing.T) {
	endpoint := os.Getenv("OPS_ASSISTANT_TEST_METRICS_URL")
	if endpoint == "" {
		t.Skip("owned Victoria fixture required")
	}
	now := time.Now().UTC().Truncate(time.Second)
	tenant := uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var body strings.Builder
	for i := 0; i < 3; i++ {
		for node, values := range map[string][]int{"one": {0, 1, 1}, "two": {0, 0, 1}} {
			fmt.Fprintf(&body, "kube_node_status_condition{tenant=%q,cluster=\"node-trend-fixture\",node=%q,condition=\"Ready\",status=\"true\"} %d %d\n", tenant, node, values[i], now.Add(time.Duration(i-2)*time.Minute).UnixMilli())
		}
		fmt.Fprintf(&body, "kube_node_status_condition{tenant=%q,cluster=\"outside\",node=\"outside\",condition=\"Ready\",status=\"true\"} 1 %d\n", tenant, now.Add(time.Duration(i-2)*time.Minute).UnixMilli())
	}
	request, _ := http.NewRequestWithContext(ctx, "POST", endpoint+"/api/v1/import/prometheus", strings.NewReader(body.String()))
	request.Header.Set("Content-Type", "text/plain")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 && response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	source := evidence.Binding{SourceType: "victoriametrics", Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, Endpoint: endpoint, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"node-trend-fixture"}}}}
	proof := analytics.Qualification{Operation: "trend", DatasetID: "metrics.node-ready", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("node-fixture-only"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Minute), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
	adapter, err := analytics.NewVictoria(source, http.DefaultClient, func(context.Context, evidence.Binding) error { return nil }, []analytics.Qualification{proof})
	if err != nil {
		t.Fatal(err)
	}
	binding := analytics.Binding{Subject: "operator", Grant: graph.Scope{Tenant: tenant, Cluster: "node-trend-fixture", ClusterScoped: true, AuthorizationRevision: "fixture"}, Selection: analytics.Selection{IncludeClusterScoped: true}}
	intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: proof.DatasetID, Operation: "trend", Metrics: []string{"ready_count"}, Dimensions: []string{"time"}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 50, StepSeconds: 60, TimeRange: &analytics.QueryTimeRange{From: now.Add(-2 * time.Minute), To: now, Timezone: "UTC"}}
	plan, err := adapter.Compile(intent, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Execute(ctx, plan)
	for i := 0; err == nil && result.Status == "no_data" && i < 10; i++ {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
		result, err = adapter.Execute(ctx, plan)
	}
	if err != nil || !result.Exact || len(result.Rows) != 3 {
		t.Fatalf("node trend: %+v %v", result, err)
	}
	for i, want := range []float64{0, 1, 2} {
		if result.Rows[i].Value != want {
			t.Fatalf("observed node zero or scope lost: %+v", result)
		}
	}
	binding.Grant.ClusterScoped = false
	binding.Grant.Namespaces = []string{"apps"}
	if _, err = adapter.Compile(intent, binding); err == nil {
		t.Fatal("namespace-only operator read node trend")
	}
}
