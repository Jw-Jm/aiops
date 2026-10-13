package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"math"
	"net/http"
	"ops-platform/internal/analytics"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAssistantExtendedVictoriaSources(t *testing.T) {
	metrics, logs := os.Getenv("OPS_ASSISTANT_TEST_METRICS_URL"), os.Getenv("OPS_ASSISTANT_TEST_LOGS_URL")
	if metrics == "" || logs == "" {
		t.Skip("task-owned Victoria URLs required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	tenant := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Second)
	cluster := "extended-fixture"
	post := func(url, contentType, body string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 200 && r.StatusCode != 204 {
			t.Fatal(r.Status)
		}
	}
	var text strings.Builder
	for i := 0; i <= 15; i++ {
		at := now.Add(time.Duration(i-15) * time.Minute).UnixMilli()
		for _, row := range []struct {
			tenant, ns, uid string
			factor          int
		}{{tenant, "apps", "a", 1}, {tenant, "private", "p", 1000}, {"other", "apps", "o", 1000}} {
			fmt.Fprintf(&text, "container_cpu_usage_seconds_total{tenant=%q,cluster=%q,namespace=%q,uid=%q,container=\"app\"} %d %d\n", row.tenant, cluster, row.ns, row.uid, 30*i*row.factor, at)
			counter := i
			if i >= 10 {
				counter = i - 10
			}
			fmt.Fprintf(&text, "kube_pod_container_status_restarts_total{tenant=%q,cluster=%q,namespace=%q,uid=%q} %d %d\n", row.tenant, cluster, row.ns, row.uid, counter*row.factor, at)
			fmt.Fprintf(&text, "container_memory_working_set_bytes{tenant=%q,cluster=%q,namespace=%q,uid=%q,container=\"app\"} %d %d\n", row.tenant, cluster, row.ns, row.uid, 100*row.factor, at)
		}
	}
	for i, want := range []int{0, 1, 2} {
		for pod := 0; pod < 2; pod++ {
			value := 0
			if pod < want {
				value = 1
			}
			fmt.Fprintf(&text, "kube_pod_status_phase{tenant=%q,cluster=%q,namespace=\"apps\",uid=\"phase-%d\",phase=\"Running\"} %d %d\n", tenant, cluster, pod, value, now.Add(time.Duration(i-2)*time.Minute).UnixMilli())
		}
	}
	for pod := 0; pod < 2; pod++ {
		fmt.Fprintf(&text, "kube_pod_status_phase{tenant=%q,cluster=%q,namespace=\"apps\",uid=\"phase-%d\",phase=\"Running\"} 1 %d\n", tenant, cluster, pod, now.Add(-6*time.Minute).UnixMilli())
	}
	post(metrics+"/api/v1/import/prometheus", "text/plain", text.String())
	var lines strings.Builder
	for i := 0; i < 3; i++ {
		for n := 0; n <= i; n++ {
			raw, _ := json.Marshal(map[string]string{"_time": now.Add(time.Duration(i-2)*time.Minute - 10*time.Second).Format(time.RFC3339), "_msg": "password=hidden https://user:secret@example.test sessionToken=abc", "tenant": tenant, "cluster": cluster, "namespace": "apps", "uid": "a", "level": "error"})
			lines.Write(raw)
			lines.WriteByte('\n')
		}
	}
	post(logs+"/insert/jsonline?_stream_fields=tenant,cluster,namespace,uid", "application/stream+json", lines.String())
	grant := analytics.Binding{Subject: "fixture", Grant: graph.Scope{Tenant: tenant, Cluster: cluster, Namespaces: []string{"apps"}, AuthorizationRevision: "fixture"}}
	adapter := func(dataset, source, endpoint string, operations ...string) *analytics.Victoria {
		t.Helper()
		b := evidence.Binding{SourceType: source, Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, Endpoint: endpoint, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {cluster}, "namespace": {"apps", "private"}}}}
		proofs := []analytics.Qualification{}
		for _, op := range operations {
			proofs = append(proofs, analytics.Qualification{DatasetID: dataset, Operation: op, SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(b), EvidenceDigest: graph.ScopeDigest("fixture"), CollectorVersion: "labelled-fixture/v1", VerifiedUntil: now.Add(time.Minute), CoverageFrom: now.Add(-time.Hour), CoverageTo: now.Add(time.Minute), MaxWindowSeconds: 3600})
		}
		v, e := analytics.NewVictoria(b, http.DefaultClient, func(context.Context, evidence.Binding) error { return nil }, proofs)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	execute := func(v *analytics.Victoria, q analytics.QueryIntent) analytics.QueryResult {
		t.Helper()
		p, e := v.Compile(q, grant)
		if e != nil {
			t.Fatal(e)
		}
		var r analytics.QueryResult
		for attempt := 0; attempt < 12; attempt++ {
			r, e = v.Execute(ctx, p)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status != "no_data" {
				return r
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		t.Fatalf("unsearchable fixture: %+v", r)
		return r
	}
	query := func(dataset, metric, op string) analytics.QueryIntent {
		return analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: dataset, Operation: op, Metrics: []string{metric}, Dimensions: []string{}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 50, TimeRange: &analytics.QueryTimeRange{From: now.Add(-2 * time.Minute), To: now, Timezone: "UTC"}}
	}
	for _, row := range []struct{ dataset, metric string }{{"metrics.pod-cpu", "cpu_cores"}, {"metrics.pod-restarts", "restart_increase"}, {"metrics.pod-phase", "phase_count"}} {
		t.Run(row.dataset, func(t *testing.T) {
			v := adapter(row.dataset, "victoriametrics", metrics, "trend", "compare", "compare_percent")
			q := query(row.dataset, row.metric, "trend")
			q.Dimensions = []string{"time"}
			q.StepSeconds = 60
			r := execute(v, q)
			if !r.Exact || len(r.Rows) != 3 {
				t.Fatal(r)
			}
			for i, p := range r.Rows {
				switch row.dataset {
				case "metrics.pod-cpu":
					if math.Abs(p.Value-0.5) > 0.01 {
						t.Fatal("CPU scope/rate", p)
					}
				case "metrics.pod-restarts":
					if p.Value < 0 || p.Value > 2 {
						t.Fatal("restart reset or scope", p)
					}
				case "metrics.pod-phase":
					if p.Value != float64(i) {
						t.Fatal("phase observation", r)
					}
				}
			}
			q.Operation = "compare"
			q.StepSeconds = 0
			q.Dimensions = []string{"comparison"}
			q.ComparisonTimeRange = &analytics.QueryTimeRange{From: now.Add(-8 * time.Minute), To: now.Add(-6 * time.Minute), Timezone: "UTC"}
			r = execute(v, q)
			if !r.Exact || len(r.Rows) != 3 {
				t.Fatal("comparison", r)
			}
		})
	}
	v := adapter("metrics.pod-memory", "victoriametrics", metrics, "average", "compare_percent")
	r := execute(v, query("metrics.pod-memory", "memory_bytes", "average"))
	if !r.Exact || len(r.Rows) != 1 || r.Rows[0].Value != 100 {
		t.Fatal("window average", r)
	}
	logAdapter := adapter("logs.records", "victorialogs", logs, "trend", "compare", "compare_percent", "samples")
	q := query("logs.records", "log_count", "trend")
	q.Dimensions = []string{"time"}
	q.StepSeconds = 60
	r = execute(logAdapter, q)
	if !r.Exact || len(r.Rows) != 3 {
		t.Fatal(r)
	}
	for i, p := range r.Rows {
		if p.Value != float64(i+1) {
			t.Fatal("log bucket became cumulative", r)
		}
	}
	q = query("logs.records", "log_count", "compare_percent")
	q.TimeRange.From = now.Add(-time.Minute)
	q.Dimensions = []string{"comparison"}
	q.ComparisonTimeRange = &analytics.QueryTimeRange{From: now.Add(-3 * time.Minute), To: now.Add(-2 * time.Minute), Timezone: "UTC"}
	r = execute(logAdapter, q)
	if !r.Exact || r.Unit != "percent" || len(r.Rows) != 1 || r.Rows[0].Value != 200 {
		t.Fatal("log percent", r)
	}
	q = query("logs.records", "log_count", "samples")
	q.Limit = 2
	r = execute(logAdapter, q)
	raw, _ := json.Marshal(r)
	if r.Exact || !r.Truncated || len(r.Samples) != 2 || strings.Contains(string(raw), "hidden") || strings.Contains(string(raw), "user:secret") || strings.Contains(string(raw), "abc") {
		t.Fatalf("bounded sanitized samples %s", raw)
	}
}
