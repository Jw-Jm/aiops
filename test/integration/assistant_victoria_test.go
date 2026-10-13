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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
}
