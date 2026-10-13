package analytics_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
)

const tenant = "018f0000-0000-7000-8000-000000000001"

type authority struct {
	scope       graph.Scope
	calls       int
	revokeAfter int
}

func (a *authority) Effective(_ context.Context, tenant, subject, cluster string) (graph.Scope, error) {
	a.calls++
	if tenant != a.scope.Tenant || subject != "operator-a" || cluster != a.scope.Cluster {
		return graph.Scope{}, errors.New("denied")
	}
	if a.revokeAfter > 0 && a.calls > a.revokeAfter {
		return graph.Scope{}, errors.New("revoked")
	}
	return analytics.CloneScope(a.scope), nil
}

func fixture(t *testing.T, fresh bool, count int) (*graph.Graph, analytics.Binding, *authority) {
	t.Helper()
	now := time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC)
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := graph.NewWithClock(tenant, "cluster-a", "worker-a", []kubernetes.GVR{gvr}, func() time.Time { return now })
	g.SetOwner(1, now.Add(time.Minute))
	objects := []unstructured.Unstructured{}
	for i := 0; i < count; i++ {
		phase := "Running"
		if i%2 == 0 {
			phase = "Failed"
		}
		objects = append(objects, unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": fmt.Sprintf("pod-%03d", i), "uid": fmt.Sprintf("uid-%03d", i), "namespace": "apps"},
			"status": map[string]any{"phase": phase},
		}})
	}
	objects = append(objects, unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "private", "uid": "private-uid", "namespace": "private"},
		"status": map[string]any{"phase": "Failed"},
	}})
	state := kubernetes.GVRState{LastListCompletedAt: now, WatchConnected: fresh, WatchContinuous: fresh, LastConnectivityProbeAt: now}
	if err := g.Replace(t.Context(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: state}); err != nil {
		t.Fatal(err)
	}
	scope := graph.Scope{Tenant: tenant, Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "grant-v1"}
	return g, analytics.Binding{Subject: "operator-a", Grant: scope, OwnerEpoch: 1}, &authority{scope: scope}
}
func intent() analytics.QueryIntent {
	return analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "resources.current", Operation: "aggregate",
		Metrics: []string{"resource_count"}, Dimensions: []string{}, Filters: []analytics.Filter{{Field: "kind", Operator: "eq", Value: "Pod"}}, Sort: []analytics.Sort{}, Limit: 50}
}
func validateResult(t *testing.T, result analytics.QueryResult) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate("https://ops.local/schemas/query-result/v1", raw); err != nil {
		t.Fatal(err)
	}
}

func TestCountsCompleteAuthorizedGraphInsteadOfReturnedPage(t *testing.T) {
	g, binding, auth := fixture(t, true, 250)
	plan, err := analytics.Compile(intent(), binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (analytics.Executor{Authorization: auth, Graph: g}).Execute(t.Context(), plan)
	if err != nil || !result.Exact || result.Status != "complete" || len(result.Rows) != 1 || result.Rows[0].Value != 250 {
		t.Fatalf("expected 250 visible resources, not one page or 251 including private: %+v, %v", result, err)
	}
	validateResult(t, result)
}

func TestHealthGroupingTopAndScopeRestriction(t *testing.T) {
	g, binding, auth := fixture(t, true, 20)
	in := intent()
	in.Dimensions = []string{"health"}
	in.Limit = 1
	plan, err := analytics.Compile(in, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (analytics.Executor{Authorization: auth, Graph: g}).Execute(t.Context(), plan)
	if err != nil || len(result.Rows) != 1 || result.Rows[0] != (analytics.Row{Dimension: "abnormal", Value: 10}) || !result.Exact || !result.Truncated {
		t.Fatalf("complete aggregation and stable ties before Top: %+v %v", result, err)
	}
	validateResult(t, result)
	id := (resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "uid-019"}).String()
	binding.Selection.CanonicalIDs = []string{id}
	plan, err = analytics.Compile(intent(), binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err = (analytics.Executor{Authorization: auth, Graph: g}).Execute(t.Context(), plan)
	if err != nil || result.Rows[0].Value != 1 {
		t.Fatalf("resource restriction ignored: %+v %v", result, err)
	}
}

func TestEmptyFreshIsZeroButEmptyStaleIsNoData(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		g, binding, auth := fixture(t, fresh, 0)
		plan, err := analytics.Compile(intent(), binding)
		if err != nil {
			t.Fatal(err)
		}
		result, err := (analytics.Executor{Authorization: auth, Graph: g}).Execute(t.Context(), plan)
		if err != nil {
			t.Fatal(err)
		}
		if fresh {
			if !result.Exact || len(result.Rows) != 1 || result.Rows[0].Value != 0 {
				t.Fatalf("fresh zero: %+v", result)
			}
		} else if result.Status != "no_data" || result.Exact || len(result.Rows) != 0 {
			t.Fatalf("stale became zero: %+v", result)
		}
		validateResult(t, result)
	}
}

func TestRecheckRevocationBeforeReturningNumbers(t *testing.T) {
	g, binding, auth := fixture(t, true, 2)
	plan, err := analytics.Compile(intent(), binding)
	if err != nil {
		t.Fatal(err)
	}
	auth.revokeAfter = 1
	if result, err := (analytics.Executor{Authorization: auth, Graph: g}).Execute(t.Context(), plan); !errors.Is(err, analytics.ErrDenied) || len(result.Rows) != 0 {
		t.Fatalf("revoked authority disclosed: %+v %v", result, err)
	}
}

func TestCompilerRejectsUncontrolledSemantics(t *testing.T) {
	_, binding, _ := fixture(t, true, 0)
	cases := []struct {
		name   string
		change func(*analytics.QueryIntent)
		want   error
	}{
		{"other dataset", func(q *analytics.QueryIntent) { q.DatasetID = "audit.events" }, analytics.ErrUnsupported},
		{"deferred VM", func(q *analytics.QueryIntent) { q.Filters[0].Value = "VirtualMachine" }, analytics.ErrUnsupported},
		{"namespace operator cannot count nodes", func(q *analytics.QueryIntent) { q.Filters[0].Value = "Node" }, analytics.ErrDenied},
		{"general kinds", func(q *analytics.QueryIntent) { q.Filters = []analytics.Filter{} }, analytics.ErrUnsupported},
		{"invented metric", func(q *analytics.QueryIntent) { q.Metrics = []string{"cpu_percent"} }, analytics.ErrUnsupported},
		{"namespace grouping", func(q *analytics.QueryIntent) { q.Dimensions = []string{"namespace"} }, analytics.ErrUnsupported},
		{"foreign namespace", func(q *analytics.QueryIntent) {
			q.Filters = append(q.Filters, analytics.Filter{Field: "namespace", Operator: "eq", Value: "private"})
		}, analytics.ErrDenied},
		{"regex", func(q *analytics.QueryIntent) { q.Filters[0].Operator = "regex" }, analytics.ErrUnsupported},
		{"SQL field", func(q *analytics.QueryIntent) { q.Filters[0].Field = "SELECT *" }, analytics.ErrUnsupported},
		{"duplicate conflicting filter", func(q *analytics.QueryIntent) {
			q.Filters = append(q.Filters, analytics.Filter{Field: "kind", Operator: "eq", Value: "Node"})
		}, analytics.ErrInvalid},
		{"bad sorting", func(q *analytics.QueryIntent) { q.Sort = []analytics.Sort{{Field: "uid", Direction: "desc"}} }, analytics.ErrUnsupported},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			q := intent()
			test.change(&q)
			if _, err := analytics.Compile(q, binding); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestDecodeIntentRejectsPrivilegeFieldsAndHistory(t *testing.T) {
	raw, _ := json.Marshal(intent())
	for _, field := range []string{"tenantId", "scope", "sql", "promql", "endpoint", "system", "budget", "timeRange"} {
		t.Run(field, func(t *testing.T) {
			var payload map[string]any
			json.Unmarshal(raw, &payload)
			payload[field] = "model controlled"
			modified, _ := json.Marshal(payload)
			if _, err := analytics.DecodeIntent(modified); !errors.Is(err, analytics.ErrInvalid) {
				t.Fatalf("accepted %s: %v", field, err)
			}
		})
	}
}

func TestGrantAndSelectionRemainSeparate(t *testing.T) {
	_, binding, _ := fixture(t, true, 0)
	binding.Grant.Namespaces = []string{"apps", "other"}
	binding.Selection.Namespaces = []string{"apps"}
	scope, err := analytics.RestrictScope(binding.Grant, binding.Selection)
	if err != nil || len(scope.Namespaces) != 1 || len(binding.Grant.Namespaces) != 2 {
		t.Fatalf("scope or grant modified: %+v %+v %v", scope, binding.Grant, err)
	}
	scope.Namespaces[0] = "mutated"
	if binding.Selection.Namespaces[0] != "apps" || binding.Grant.Namespaces[0] != "apps" {
		t.Fatal("scope shares mutable caller slices")
	}
	binding.Selection.IncludeClusterScoped = true
	if _, err := analytics.RestrictScope(binding.Grant, binding.Selection); !errors.Is(err, analytics.ErrDenied) {
		t.Fatalf("cluster scope escalation: %v", err)
	}
	binding.Selection = analytics.Selection{CanonicalIDs: []string{(resource.CanonicalID{Domain: "k8s", Tenant: "foreign", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "uid"}).String()}}
	if _, err := analytics.RestrictScope(binding.Grant, binding.Selection); !errors.Is(err, analytics.ErrDenied) {
		t.Fatalf("foreign tenant: %v", err)
	}
}

type badGraph struct{ result graph.Result }

func (g badGraph) Query(context.Context, graph.Query) (graph.Result, error) { return g.result, nil }

func TestMalformedSourceSummaryCannotBecomeFacts(t *testing.T) {
	g, binding, auth := fixture(t, true, 1)
	plan, err := analytics.Compile(intent(), binding)
	if err != nil {
		t.Fatal(err)
	}
	query := graph.Query{QueryKind: "list", Kind: "Pod", Scope: binding.Grant, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 1, MaxEdges: 1}
	result, err := g.Query(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	result.Summary.ByKind["VirtualMachine"] = 1
	if _, err := (analytics.Executor{Authorization: auth, Graph: badGraph{result}}).Execute(t.Context(), plan); !errors.Is(err, analytics.ErrSource) {
		t.Fatalf("source contract mismatch accepted: %v", err)
	}
}

func TestUnknownPartialReasonRemainsIncomplete(t *testing.T) {
	out := graph.Result{Summary: &graph.ResourceSummary{}, Freshness: "fresh", Partial: true, Warnings: []string{"scope_limited"}}
	if !analytics.CompleteResourceList(out) {
		t.Fatal("authorized scope mistaken for missing data")
	}
	out.Warnings = append(out.Warnings, "future_partial_reason")
	if analytics.CompleteResourceList(out) {
		t.Fatal("unknown partial reason ignored")
	}
	out.Warnings = nil
	if analytics.CompleteResourceList(out) {
		t.Fatal("unexplained partial ignored")
	}
	out.Partial = false
	out.DegradedSources = []string{"kubernetes"}
	if analytics.CompleteResourceList(out) {
		t.Fatal("degraded source ignored")
	}
}

func TestCatalogCannotBeModifiedThroughReturnedValue(t *testing.T) {
	catalog, err := analytics.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Datasets[0].Filters[0] = "sql"
	next, err := analytics.Catalog()
	if err != nil || next.Datasets[0].Filters[0] != "kind" {
		t.Fatalf("caller mutated released catalog: %+v %v", next, err)
	}
}
