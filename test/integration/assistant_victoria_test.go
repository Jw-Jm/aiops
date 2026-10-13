package integration

import (
	"context"
	"encoding/json"
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
	"ops-platform/internal/investigation"
)

// The sources are real, task-owned Victoria instances, with labelled fixture
// data. Passing this does not qualify a production collector or ingestion SLA.
func TestAssistantVictoriaFixtureAggregates(t *testing.T) {
	metrics, logs := os.Getenv("OPS_ASSISTANT_TEST_METRICS_URL"), os.Getenv("OPS_ASSISTANT_TEST_LOGS_URL")
	if metrics == "" || logs == "" {
		t.Skip("task-owned Victoria source URLs required")
	}
	tenant := uuid.NewString()
	cluster := "assistant-fixture"
	now := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	post := func(endpoint, contentType, body string) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", contentType)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 204 {
			t.Fatalf("fixture ingest HTTP %d", response.StatusCode)
		}
	}
	post(metrics+"/api/v1/import/prometheus", "text/plain", fmt.Sprintf("container_memory_working_set_bytes{tenant=%q,cluster=%q,namespace=\"apps\",uid=\"a\",container=\"app\"} 1024 %d\ncontainer_memory_working_set_bytes{tenant=%q,cluster=%q,namespace=\"apps\",uid=\"b\",container=\"app\"} 2048 %d\ncontainer_memory_working_set_bytes{tenant=%q,cluster=%q,namespace=\"private\",uid=\"p\",container=\"app\"} 8192 %d\ncontainer_memory_working_set_bytes{tenant=\"other\",cluster=%q,namespace=\"apps\",uid=\"outside\",container=\"app\"} 16384 %d\n", tenant, cluster, now.Add(-time.Minute).UnixMilli(), tenant, cluster, now.Add(-time.Minute).UnixMilli(), tenant, cluster, now.Add(-time.Minute).UnixMilli(), cluster, now.Add(-time.Minute).UnixMilli()))
	var lines strings.Builder
	for _, entry := range []struct {
		Tenant, Namespace string
		Count             int
	}{{tenant, "apps", 4}, {tenant, "private", 10}, {"other", "apps", 20}} {
		for i := 0; i < entry.Count; i++ {
			line, _ := json.Marshal(map[string]any{"_time": now.Add(-10 * time.Second).Format(time.RFC3339), "_msg": "test error", "tenant": entry.Tenant, "cluster": cluster, "namespace": entry.Namespace, "uid": "a", "level": "error"})
			lines.Write(line)
			lines.WriteByte('\n')
		}
	}
	post(logs+"/insert/jsonline?_stream_fields=tenant,cluster,namespace,uid", "application/stream+json", lines.String())
	grant := graph.Scope{Tenant: tenant, Cluster: cluster, Namespaces: []string{"apps"}, AuthorizationRevision: "fixture-grant"}
	for _, test := range []struct {
		Source, Endpoint, Dataset, Metric, Unit string
		Want                                    float64
	}{{"victoriametrics", metrics, "metrics.pod-memory", "memory_bytes", "bytes", 3072}, {"victorialogs", logs, "logs.records", "log_count", "logs", 4}} {
		t.Run(test.Dataset, func(t *testing.T) {
			binding := evidence.Binding{SourceType: test.Source, Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, BackendLogicalID: "task-fixture", Endpoint: test.Endpoint, SourceRetentionSeconds: 86400, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {cluster}, "namespace": {"apps", "private"}}}}
			qualification := analytics.Qualification{DatasetID: test.Dataset, SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(binding), EvidenceDigest: graph.ScopeDigest("fixture-proof"), CollectorVersion: "labelled-fixture/v1", VerifiedUntil: now.Add(time.Minute), CoverageFrom: now.Add(-time.Hour), CoverageTo: now.Add(time.Minute), MaxWindowSeconds: 3600}
			adapter, err := analytics.NewVictoria(binding, http.DefaultClient, func(context.Context, evidence.Binding) error { return nil }, []analytics.Qualification{qualification})
			if err != nil {
				t.Fatal(err)
			}
			intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: test.Dataset, Operation: "aggregate", Metrics: []string{test.Metric}, Dimensions: []string{"namespace"}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 50, TimeRange: &analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now.Add(time.Second), Timezone: "Asia/Shanghai"}}
			plan, err := adapter.Compile(intent, analytics.Binding{Subject: "fixture-operator", Grant: grant})
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Execute(ctx, plan)
			// Real backends make ingested blocks searchable asynchronously. Retry
			// only a genuine empty observation, within the fixture's fixed deadline.
			for attempts := 0; err == nil && result.Status == "no_data" && attempts < 10; attempts++ {
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Second):
				}
				result, err = adapter.Execute(ctx, plan)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !result.Exact || len(result.Rows) != 1 || result.Rows[0].Dimension != "apps" || result.Rows[0].Value != test.Want || result.Unit != test.Unit || result.SourceManifest == nil {
				t.Fatalf("authorized aggregate %+v", result)
			}
			unqualified, err := analytics.NewVictoria(binding, http.DefaultClient, func(context.Context, evidence.Binding) error { return nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = unqualified.Compile(intent, analytics.Binding{Subject: "fixture-operator", Grant: grant}); err == nil {
				t.Fatal("legacy source proof qualified new dataset")
			}
		})
	}
	t.Run("gauge trend and missing point facts", func(t *testing.T) {
		trendTenant := uuid.NewString()
		var samples strings.Builder
		for i, value := range []int{100, 200, 0, 400} {
			at := now.Add(time.Duration(i-3) * time.Minute).UnixMilli()
			for _, sample := range []struct {
				tenant, namespace string
				value             int
			}{{trendTenant, "apps", value}, {trendTenant, "private", 10000}, {"other", "apps", 20000}} {
				fmt.Fprintf(&samples, "container_memory_working_set_bytes{tenant=%q,cluster=%q,namespace=%q,uid=\"trend-pod\",container=\"app\"} %d %d\n", sample.tenant, cluster, sample.namespace, sample.value, at)
			}
		}
		post(metrics+"/api/v1/import/prometheus", "text/plain", samples.String())
		source := evidence.Binding{SourceType: "victoriametrics", Tenant: trendTenant, SourceID: uuid.NewString(), Revision: 1, BackendLogicalID: "task-fixture", Endpoint: metrics, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": trendTenant}, Scopes: map[string][]string{"cluster": {cluster}, "namespace": {"apps", "private"}}}}
		qualification := analytics.Qualification{Operation: "trend", DatasetID: "metrics.pod-memory", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("trend-fixture-proof"), CollectorVersion: "labelled-fixture/v1", VerifiedUntil: now.Add(time.Minute), CoverageFrom: now.Add(-time.Hour), CoverageTo: now.Add(time.Minute), MaxWindowSeconds: 3600}
		comparisonQualification := qualification
		comparisonQualification.Operation = "compare"
		adapter, err := analytics.NewVictoria(source, http.DefaultClient, func(context.Context, evidence.Binding) error { return nil }, []analytics.Qualification{qualification, comparisonQualification})
		if err != nil {
			t.Fatal(err)
		}
		binding := analytics.Binding{Subject: "fixture-operator", Grant: graph.Scope{Tenant: trendTenant, Cluster: cluster, Namespaces: []string{"apps"}, AuthorizationRevision: "fixture-grant"}}
		intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "metrics.pod-memory", Operation: "trend", Metrics: []string{"memory_bytes"}, Dimensions: []string{"time"}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 50, StepSeconds: 60, TimeRange: &analytics.QueryTimeRange{From: now.Add(-3 * time.Minute), To: now, Timezone: "UTC"}}
		plan, err := adapter.Compile(intent, binding)
		if err != nil {
			t.Fatal(err)
		}
		result, err := adapter.Execute(ctx, plan)
		for attempts := 0; err == nil && result.Status == "no_data" && attempts < 10; attempts++ {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
			result, err = adapter.Execute(ctx, plan)
		}
		if err != nil || !result.Exact || len(result.Rows) != 4 || result.StepSeconds != 60 {
			t.Fatalf("real bounded trend: %+v %v", result, err)
		}
		stored := investigation.BuildStoredQueryResult(result)
		for i, want := range []float64{100, 200, 0, 400} {
			if result.Rows[i].Value != want || stored.Facts[i].Value != want || result.Rows[i].Dimension != now.Add(time.Duration(i-3)*time.Minute).Format(time.RFC3339) {
				t.Fatalf("trend fact %d: %+v", i, result)
			}
		}
		intent.TimeRange.From = now.Add(-4 * time.Minute)
		plan, err = adapter.Compile(intent, binding)
		if err != nil {
			t.Fatal(err)
		}
		result, err = adapter.Execute(ctx, plan)
		if err != nil || result.Exact || result.Status != "partial" || len(result.Rows) != 4 || result.Rows[0].Dimension != now.Add(-3*time.Minute).Format(time.RFC3339) {
			t.Fatalf("missing observation became zero: %+v %v", result, err)
		}
		intent.Operation = "compare"
		intent.StepSeconds = 0
		intent.Dimensions = []string{"comparison"}
		intent.TimeRange.From = now.Add(-time.Minute)
		intent.ComparisonTimeRange = &analytics.QueryTimeRange{From: now.Add(-4 * time.Minute), To: now.Add(-3 * time.Minute), Timezone: "UTC"}
		plan, err = adapter.Compile(intent, binding)
		if err != nil {
			t.Fatal(err)
		}
		result, err = adapter.Execute(ctx, plan)
		if err != nil || !result.Exact || len(result.Rows) != 3 || result.Rows[0].Value != 100 || result.Rows[1].Value != 400 || result.Rows[2].Dimension != "increase" || result.Rows[2].Value != 300 {
			t.Fatalf("real gauge comparison: %+v %v", result, err)
		}
		intent.TimeRange.From = now.Add(-2 * time.Minute)
		intent.TimeRange.To = now.Add(-time.Minute)
		plan, err = adapter.Compile(intent, binding)
		if err != nil {
			t.Fatal(err)
		}
		result, err = adapter.Execute(ctx, plan)
		if err != nil || !result.Exact || len(result.Rows) != 3 || result.Rows[1].Value != 0 || result.Rows[2].Dimension != "decrease" || result.Rows[2].Value != 100 {
			t.Fatalf("observed zero comparison: %+v %v", result, err)
		}
	})
}
