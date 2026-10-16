package analytics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
)

type costRoundTrip func(*http.Request) (*http.Response, error)

func (f costRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func costQueryIntent(dataset, metric, operation string, step int, from time.Time, to time.Time) QueryIntent {
	return QueryIntent{
		SchemaVersion: "query-intent/v1", DatasetID: dataset, Operation: operation,
		Metrics: []string{metric}, Dimensions: []string{"time"}, Filters: []Filter{}, Sort: []Sort{},
		Limit: 50, StepSeconds: step, TimeRange: &QueryTimeRange{From: from, To: to, Timezone: "UTC"},
	}
}

// TestSourceRequestCostMatchesExecutedRequests is the regression test for the
// budget defect: the number reserved before a call must equal the number of
// backend requests the call actually issues.
func TestSourceRequestCostMatchesExecutedRequests(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, entry := range []struct {
		name                string
		dataset, metric, op string
		source              string
		step                int
		window              time.Duration
		comparison          bool
		wantRequests        int64
		executeAndCount     bool
	}{
		{name: "logs.records/trend", dataset: "logs.records", metric: "log_count", op: "trend", source: "victorialogs", step: 60, window: 49 * time.Minute, wantRequests: 50, executeAndCount: true},
		{name: "metrics.pod-memory/compare", dataset: "metrics.pod-memory", metric: "memory_bytes", op: "compare", source: "victoriametrics", comparison: true, wantRequests: 2, executeAndCount: true},
		{name: "metrics.pod-cpu/trend", dataset: "metrics.pod-cpu", metric: "cpu_cores", op: "trend", source: "victoriametrics", step: 60, window: 49 * time.Minute, wantRequests: 1},
		{name: "logs.records/aggregate", dataset: "logs.records", metric: "log_count", op: "aggregate", source: "victorialogs", window: 10 * time.Minute, wantRequests: 1},
		{name: "network.tcp-summary/trend", dataset: "network.tcp-summary", metric: "retransmissions", op: "trend", source: "deepflow", step: 60, window: 49 * time.Minute, wantRequests: 49},
		{name: "network.tcp-summary/compare", dataset: "network.tcp-summary", metric: "retransmissions", op: "compare", source: "deepflow", comparison: true, wantRequests: 2},
		{name: "resources.current/aggregate", dataset: "resources.current", metric: "resource_count", op: "aggregate", source: "graph", window: 10 * time.Minute, wantRequests: 1},
		{name: "incidents.records/aggregate", dataset: "incidents.records", metric: "incident_count", op: "aggregate", source: "postgresql", window: 10 * time.Minute, wantRequests: 1},
	} {
		t.Run(entry.name, func(t *testing.T) {
			intent := costQueryIntent(entry.dataset, entry.metric, entry.op, entry.step, now.Add(-entry.window), now)
			if entry.comparison {
				intent.Dimensions, intent.StepSeconds = []string{"comparison"}, 0
				intent.TimeRange.From = now.Add(-time.Minute)
				intent.ComparisonTimeRange = &QueryTimeRange{From: now.Add(-10 * time.Minute), To: now.Add(-9 * time.Minute), Timezone: "UTC"}
			}
			cost, err := SourceRequestCost(intent)
			if err != nil {
				t.Fatalf("SourceRequestCost: %v", err)
			}
			if cost != entry.wantRequests {
				t.Fatalf("reserved raw queries = %d, want %d", cost, entry.wantRequests)
			}
			if !entry.executeAndCount || entry.source == "deepflow" || entry.source == "graph" || entry.source == "postgresql" {
				return
			}
			calls := 0
			client := &http.Client{Transport: costRoundTrip(func(r *http.Request) (*http.Response, error) {
				if err := r.ParseForm(); err != nil {
					return nil, err
				}
				at, err := time.Parse(time.RFC3339Nano, r.Form.Get("time"))
				if err != nil {
					return nil, err
				}
				calls++
				body := fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[%d,"2"]}]}}`, at.Unix())
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			tenant := uuid.NewString()
			source := evidence.Binding{SourceType: entry.source, Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, Endpoint: "http://cost.invalid", ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
			proof := Qualification{DatasetID: entry.dataset, Operation: entry.op, SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("cost-fixture"), CollectorVersion: "cost/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-2 * time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
			adapter, err := NewVictoria(source, client, func(context.Context, evidence.Binding) error { return nil }, []Qualification{proof})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := adapter.Compile(intent, Binding{Subject: "cost-operator", Grant: graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}, AuthorizationRevision: "cost-grant"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = adapter.Execute(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			if int64(calls) != cost {
				t.Fatalf("executed %d source requests but reserved %d", calls, cost)
			}
		})
	}
}

func TestSourceRequestCostRejectsUnboundedIntents(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, entry := range []struct {
		name   string
		intent QueryIntent
		want   error
	}{
		{
			name:   "unknown dataset",
			intent: costQueryIntent("logs.unknown", "log_count", "aggregate", 0, now.Add(-time.Hour), now),
			want:   ErrUnsupported,
		},
		{
			name:   "operation outside the catalog",
			intent: costQueryIntent("resources.current", "resource_count", "trend", 60, now.Add(-time.Hour), now),
			want:   ErrUnsupported,
		},
		{
			name:   "compare without a comparison window",
			intent: costQueryIntent("metrics.pod-memory", "memory_bytes", "compare", 0, now.Add(-time.Hour), now),
			want:   ErrInvalid,
		},
		{
			name:   "trend below the minimum step",
			intent: costQueryIntent("logs.records", "log_count", "trend", 30, now.Add(-time.Hour), now),
			want:   ErrInvalid,
		},
		{
			name:   "trend on a non-divisible window",
			intent: costQueryIntent("logs.records", "log_count", "trend", 60, now.Add(-time.Hour-time.Second), now),
			want:   ErrInvalid,
		},
		{
			name:   "missing time range",
			intent: QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "logs.records", Operation: "aggregate"},
			want:   ErrInvalid,
		},
	} {
		t.Run(entry.name, func(t *testing.T) {
			if _, err := SourceRequestCost(entry.intent); !errors.Is(err, entry.want) {
				t.Fatalf("got %v, want %v", err, entry.want)
			}
		})
	}
}
