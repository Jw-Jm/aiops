package analytics

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"strings"
	"testing"
	"time"
)

func TestFixedExtendedTemplatesAndSamples(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tenant := uuid.NewString()
	for _, entry := range []struct{ dataset, metric, operation, source, want string }{{"metrics.pod-cpu", "cpu_cores", "trend", "victoriametrics", "sum(rate(container_cpu"}, {"metrics.pod-restarts", "restart_increase", "trend", "victoriametrics", "[60s]"}, {"metrics.pod-memory", "memory_bytes", "average", "victoriametrics", "avg_over_time"}, {"metrics.pod-phase", "phase_count", "average", "victoriametrics", "avg_over_time"}, {"logs.records", "log_count", "trend", "victorialogs", "count() as value"}, {"logs.records", "log_count", "samples", "victorialogs", "limit 3"}} {
		t.Run(entry.dataset+entry.operation, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				calls++
				if !strings.Contains(r.Form.Get("query"), entry.want) {
					t.Error(r.Form.Get("query"))
				}
				if entry.operation == "samples" {
					json.NewEncoder(w).Encode(map[string]string{"_time": now.Add(-time.Second).Format(time.RFC3339), "_msg": "password=hidden https://user:secret@example.test sessionToken=abc", "tenant": tenant, "cluster": "cluster", "namespace": "apps", "uid": "pod", "level": "info"})
					return
				}
				at, _ := time.Parse(time.RFC3339, r.Form.Get("time"))
				series := map[string]any{"metric": map[string]string{}, "value": []any{at.Unix(), "2"}}
				typ := "vector"
				if entry.operation == "trend" && entry.source != "victorialogs" {
					typ = "matrix"
					delete(series, "value")
					series["values"] = [][]any{{now.Add(-time.Minute).Unix(), "1"}, {now.Unix(), "2"}}
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": typ, "result": []any{series}}})
			}))
			defer server.Close()
			source := evidence.Binding{SourceType: entry.source, Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, Endpoint: server.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
			proof := Qualification{DatasetID: entry.dataset, Operation: entry.operation, SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("fixture"), CollectorVersion: "fixture", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
			adapter, _ := NewVictoria(source, server.Client(), func(context.Context, evidence.Binding) error { return nil }, []Qualification{proof})
			q := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: entry.dataset, Operation: entry.operation, Metrics: []string{entry.metric}, Dimensions: []string{}, Filters: []Filter{}, Sort: []Sort{}, Limit: 2, TimeRange: &QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}}
			if entry.operation == "trend" {
				q.Dimensions = []string{"time"}
				q.StepSeconds = 60
			}
			plan, err := adapter.Compile(q, Binding{Subject: "operator", Grant: graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}, AuthorizationRevision: "rev"}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Execute(t.Context(), plan)
			if err != nil {
				t.Fatal(err)
			}
			if entry.operation == "samples" {
				// Assert on the sanitized sample bodies only. Checking the whole
				// envelope made this test flaky: the random tenant UUID feeds the
				// digests, and a hex digest occasionally contains "abc".
				samples, _ := json.Marshal(result.Samples)
				if result.Exact || result.Status != "partial" || strings.Contains(string(samples), "hidden") || strings.Contains(string(samples), "user:secret") || strings.Contains(string(samples), "abc") {
					t.Fatalf("unsafe samples: %s", samples)
				}
			}
			if entry.source == "victorialogs" && entry.operation == "trend" && calls != 2 {
				t.Fatal("missing bucket")
			}
		})
	}
}

func TestComparisonPercentZeroBaseline(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, baseline := range []string{"100", "0"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.ParseForm()
			at, _ := time.Parse(time.RFC3339, r.Form.Get("time"))
			value := "400"
			if at.Before(now.Add(-time.Minute)) {
				value = baseline
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{at.Unix(), value}}}}})
		}))
		tenant := uuid.NewString()
		source := evidence.Binding{SourceType: "victoriametrics", Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, Endpoint: server.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
		proof := Qualification{DatasetID: "metrics.pod-memory", Operation: "compare_percent", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("fixture"), CollectorVersion: "fixture", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
		adapter, _ := NewVictoria(source, server.Client(), func(context.Context, evidence.Binding) error { return nil }, []Qualification{proof})
		q := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: proof.DatasetID, Operation: proof.Operation, Metrics: []string{"memory_bytes"}, Dimensions: []string{"comparison"}, Filters: []Filter{}, Sort: []Sort{}, Limit: 3, TimeRange: &QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}, ComparisonTimeRange: &QueryTimeRange{From: now.Add(-10 * time.Minute), To: now.Add(-9 * time.Minute), Timezone: "UTC"}}
		plan, err := adapter.Compile(q, Binding{Subject: "operator", Grant: graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}, AuthorizationRevision: "rev"}})
		if err != nil {
			t.Fatal(err)
		}
		out, err := adapter.Execute(t.Context(), plan)
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "0" {
			if out.Status != "no_data" || len(out.Rows) != 0 {
				t.Fatal(out)
			}
		} else if !out.Exact || out.Unit != "percent" || len(out.Rows) != 1 || out.Rows[0].Value != 300 {
			t.Fatal(out)
		}
	}
}

func TestCapabilitiesRequireCurrentQualificationAndScope(t *testing.T) {
	tenant := uuid.NewString()
	now := time.Now().UTC()
	source := evidence.Binding{SourceType: "victoriametrics", Tenant: tenant, SourceID: uuid.NewString(), Revision: 2, Endpoint: "http://127.0.0.1", ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
	proof := Qualification{DatasetID: "metrics.pod-memory", Operation: "trend", SourceRevision: 2, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("proof"), CollectorVersion: "fixture", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
	scope := graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}}
	for _, change := range []string{"valid", "expired", "revision", "binding", "digest", "namespace", "resource", "revoked"} {
		t.Run(change, func(t *testing.T) {
			q := proof
			grant := scope
			switch change {
			case "expired":
				q.VerifiedUntil = now.Add(-time.Second)
			case "revision":
				q.SourceRevision = 1
			case "binding":
				q.BindingDigest = graph.ScopeDigest("old")
			case "digest":
				q.EvidenceDigest = "declaration"
			case "namespace":
				grant.Namespaces = []string{"private"}
			case "resource":
				grant.Resources = []string{"forged"}
			}
			v, _ := NewVictoria(source, http.DefaultClient, func(context.Context, evidence.Binding) error {
				if change == "revoked" {
					return ErrDenied
				}
				return nil
			}, []Qualification{q})
			out, err := v.Capabilities(t.Context(), grant)
			if change == "valid" {
				if err != nil || len(out) != 1 || out[0].Status != "qualification_configured" {
					t.Fatal(out, err)
				}
			} else if len(out) != 0 {
				t.Fatal("unqualified capability advertised", out)
			}
			if change == "revoked" && err == nil {
				t.Fatal("revoked source advertised")
			}
		})
	}
}

func TestNodeReadyUIDSelectorDoesNotCountOtherNodes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tenant := uuid.NewString()
	source := evidence.Binding{SourceType: "victoriametrics", Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, Endpoint: "http://127.0.0.1:18428", ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"cluster"}}}}
	proof := Qualification{DatasetID: "metrics.node-ready", Operation: "aggregate", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("fixture"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Minute), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
	adapter, err := NewVictoria(source, http.DefaultClient, func(context.Context, evidence.Binding) error { return nil }, []Qualification{proof})
	if err != nil {
		t.Fatal(err)
	}
	target := "k8s+v1://" + tenant + "/cluster/core/Node/node-uid"
	binding := Binding{Subject: "operator", Grant: graph.Scope{Tenant: tenant, Cluster: "cluster", ClusterScoped: true, AuthorizationRevision: "fixture"}, Selection: Selection{IncludeClusterScoped: true, CanonicalIDs: []string{target}}}
	intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: proof.DatasetID, Operation: "aggregate", Metrics: []string{"ready_count"}, Dimensions: []string{}, Filters: []Filter{}, Sort: []Sort{}, Limit: 1, TimeRange: &QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}}
	p, err := adapter.Compile(intent, binding)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.query, `uid=~"node-uid"`) {
		t.Fatal(p.query)
	}
	narrowed := binding.Grant
	narrowed.Resources = []string{target}
	if caps, err := adapter.Capabilities(t.Context(), narrowed); err != nil || len(caps) != 1 {
		t.Fatal("Node UID qualification hidden", caps, err)
	}

	binding.Selection.CanonicalIDs = []string{strings.Replace(target, "/Node/", "/Pod/", 1)}
	if _, err := adapter.Compile(intent, binding); err == nil {
		t.Fatal("Pod substituted for Node UID")
	}
}
