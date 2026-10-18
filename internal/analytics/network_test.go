package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/contract"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/deepflow"
	"ops-platform/internal/resource"
	"strings"
	"testing"
	"time"
)

type networkTestMapper map[string]deepflow.Endpoint

func (m networkTestMapper) ByCanonical(_ context.Context, id string) (deepflow.Endpoint, error) {
	return m[id], nil
}
func (m networkTestMapper) ByBackend(_ context.Context, id uint64) (deepflow.Endpoint, error) {
	for _, e := range m {
		if e.PodID == id {
			return e, nil
		}
	}
	return deepflow.Endpoint{}, evidence.ErrScopeUnverified
}

func TestNetworkComparisonDenominatorsAndQualification(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	baselineFrom := now.Add(-4 * time.Minute)
	id := func(uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	}
	target, peer := id("a"), id("b")
	empty := false
	baselineErrors := 10
	queries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		r.ParseForm()
		sql := r.Form.Get("sql")
		errors := 50
		responses := 1000
		if strings.Contains(sql, fmt.Sprint(baselineFrom.Unix())) {
			errors = baselineErrors
		}
		if empty {
			responses = 0
			errors = 0
		}
		fmt.Fprintf(w, `{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[[200,%d,0],[503,%d,0]]}}`, responses-errors, errors)
	}))
	defer server.Close()
	binding := evidence.Binding{SourceType: "deepflow", Tenant: "tenant-a", SourceID: "52c73f53-20d5-4e55-b33a-f7e97dccae74", Revision: 1, BackendLogicalID: "df", Endpoint: server.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	mapper := networkTestMapper{target: {CanonicalID: target, Namespace: "apps", PodID: 1, ClusterID: 2, NamespaceID: 3}, peer: {CanonicalID: peer, Namespace: "apps", PodID: 2, ClusterID: 2, NamespaceID: 3}}
	adapter, _ := deepflow.New(binding, server.Client(), mapper, func(context.Context, evidence.Binding) error { return nil }, true, "fixture_only")
	proof := Qualification{Operation: "compare", DatasetID: "network.http-5xx-rate", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(binding), EvidenceDigest: graph.ScopeDigest("independent-golden-fixture"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600, ObservationPoint: "s-p", CoverageComplete: true}
	n := NewNetwork(adapter, []Qualification{proof})
	intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: proof.DatasetID, Operation: "compare", Metrics: []string{"http_5xx_percent"}, Dimensions: []string{"comparison"}, Filters: []Filter{}, Sort: []Sort{}, Limit: 3, TimeRange: &QueryTimeRange{now.Add(-time.Minute), now, "UTC"}, ComparisonTimeRange: &QueryTimeRange{baselineFrom, baselineFrom.Add(time.Minute), "UTC"}, NetworkSelection: &NetworkSelection{target, peer, "outgoing"}}
	auth := Binding{Subject: "operator", Grant: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "grant"}, Selection: Selection{Namespaces: []string{"apps"}}}
	plan, err := n.Compile(intent, auth)
	if err != nil {
		t.Fatal(err)
	}
	result, facts, err := n.Execute(t.Context(), plan)
	if err != nil || len(result.Rows) != 3 || result.Rows[0].Value != 1 || result.Rows[1].Value != 5 || result.Rows[2].Value != 4 || len(facts) != 2 || len(result.Statistics) != 6 {
		t.Fatalf("comparison %+v %v", result, err)
	}
	combined := proof
	combined.DatasetID = "network.http-summary"
	combinedIntent := intent
	combinedIntent.DatasetID = combined.DatasetID
	combinedPlan, err := NewNetwork(adapter, []Qualification{combined}).Compile(combinedIntent, auth)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := NewNetwork(adapter, []Qualification{combined}).Execute(t.Context(), combinedPlan)
	if err != nil || len(snapshot.Statistics) != 8 {
		t.Fatal("combined HTTP statistics missing", err)
	}
	encoded, _ := json.Marshal(snapshot)
	if err := contract.Validate("https://ops.local/schemas/query-result/v1", encoded); err != nil {
		t.Fatal("combined units violate wire contract", err)
	}
	percentProof := combined
	percentProof.Operation = "compare_percent"
	percentNetwork := NewNetwork(adapter, []Qualification{percentProof})
	percentIntent := combinedIntent
	percentIntent.Operation = "compare_percent"
	percentPlan, err := percentNetwork.Compile(percentIntent, auth)
	if err != nil {
		t.Fatal(err)
	}
	before := queries
	percent, _, err := percentNetwork.Execute(t.Context(), percentPlan)
	if err != nil || !percent.Exact || percent.Unit != "percent" || len(percent.Rows) != 1 || percent.Rows[0].Value != 400 || queries-before != 2 {
		t.Fatal("relative change or charged subqueries invalid", percent, err)
	}
	baselineErrors = 0
	percent, _, err = percentNetwork.Execute(t.Context(), percentPlan)
	if err != nil || percent.Exact || len(percent.Rows) != 0 || len(percent.Statistics) != 8 {
		t.Fatal("zero baseline invented relative change or discarded population", percent, err)
	}
	baselineErrors = 10
	trendProof := combined
	trendProof.Operation = "trend"
	trendNetwork := NewNetwork(adapter, []Qualification{trendProof})
	trendIntent := combinedIntent
	trendIntent.Operation = "trend"
	trendIntent.ComparisonTimeRange = nil
	trendIntent.Dimensions = []string{"time"}
	trendIntent.StepSeconds = 60
	trendIntent.TimeRange = &QueryTimeRange{baselineFrom, baselineFrom.Add(3 * time.Minute), "UTC"}
	trendPlan, err := trendNetwork.Compile(trendIntent, auth)
	if err != nil {
		t.Fatal(err)
	}
	before = queries
	trend, _, err := trendNetwork.Execute(t.Context(), trendPlan)
	if err != nil || !trend.Exact || len(trend.Rows) != 3 || len(trend.Statistics) != 12 || queries-before != 3 {
		t.Fatal("trend population or bounded subqueries invalid", trend, err)
	}
	for i, expected := range []float64{1, 5, 5} {
		if trend.Rows[i].Value != expected || trend.Rows[i].Dimension != baselineFrom.Add(time.Duration(i)*time.Minute).Format(time.RFC3339) {
			t.Fatal("bucket value or original boundary changed", trend.Rows[i])
		}
	}
	empty = true
	result, _, err = n.Execute(t.Context(), plan)
	if err != nil || result.Exact || result.Status != "no_data" || len(result.Rows) != 0 {
		t.Fatal("zero denominator became healthy")
	}
	n.qualifications[0].VerifiedUntil = now.Add(-time.Second)
	if _, err = n.Compile(intent, auth); err != ErrSource {
		t.Fatal("expired proof accepted")
	}
}

// An empty backend response must not become an exact numeric observation. The
// DeepFlow summary query cannot distinguish "no traffic in the window" from
// "the collector missed it", so a zero-row answer keeps the explicit zero
// population but stops being exact and stops producing a value row.
func TestNetworkEmptyPopulationIsNotAnExactObservation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	id := func(uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	}
	target, peer := id("a"), id("b")
	var payload string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		fmt.Fprint(w, payload)
	}))
	defer server.Close()
	binding := evidence.Binding{SourceType: "deepflow", Tenant: "tenant-a", SourceID: "52c73f53-20d5-4e55-b33a-f7e97dccae74", Revision: 1, BackendLogicalID: "df", Endpoint: server.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	mapper := networkTestMapper{target: {CanonicalID: target, Namespace: "apps", PodID: 1, ClusterID: 2, NamespaceID: 3}, peer: {CanonicalID: peer, Namespace: "apps", PodID: 2, ClusterID: 2, NamespaceID: 3}}
	adapter, _ := deepflow.New(binding, server.Client(), mapper, func(context.Context, evidence.Binding) error { return nil }, true, "fixture_only")
	auth := Binding{Subject: "operator", Grant: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "grant"}, Selection: Selection{Namespaces: []string{"apps"}}}
	window := &QueryTimeRange{now.Add(-time.Minute), now, "UTC"}
	for _, test := range []struct{ dataset, metric string }{{"network.http-5xx", "http_5xx_count"}, {"network.http-responses", "response_count"}, {"network.tcp-retransmissions", "retransmission_count"}, {"network.connection-failure-observations", "connection_failure_observation_count"}} {
		proof := Qualification{Operation: "aggregate", DatasetID: test.dataset, SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(binding), EvidenceDigest: graph.ScopeDigest("independent-golden-fixture"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600, ObservationPoint: "s-p", CoverageComplete: true}
		n := NewNetwork(adapter, []Qualification{proof})
		intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: test.dataset, Operation: "aggregate", Metrics: []string{test.metric}, Dimensions: []string{}, Filters: []Filter{}, Sort: []Sort{}, Limit: 1, TimeRange: window, NetworkSelection: &NetworkSelection{target, peer, "outgoing"}}
		plan, err := n.Compile(intent, auth)
		if err != nil {
			t.Fatal(test.dataset, err)
		}
		// The query succeeds with no completed-observation rows at all.
		payload = `{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[]}}`
		if test.dataset == "network.tcp-retransmissions" || test.dataset == "network.connection-failure-observations" {
			payload = `{"OPT_STATUS":"SUCCESS","result":{"columns":["status","observations","retrans_tx","retrans_rx"],"values":[]}}`
		}
		empty, _, err := n.Execute(t.Context(), plan)
		if err != nil || empty.Exact || empty.Status != "no_data" || len(empty.Rows) != 0 {
			t.Fatalf("%s empty population became an exact observation: %+v %v", test.dataset, empty, err)
		}
		found := false
		for _, warning := range empty.Warnings {
			found = found || warning == "current_empty_observation_population"
		}
		if !found || empty.Freshness != "fresh" {
			t.Fatalf("%s missing empty population warning: %+v", test.dataset, empty.Warnings)
		}
		// The population stays explicit whether or not any session completed.
		expected := 3
		if test.dataset == "network.tcp-retransmissions" || test.dataset == "network.connection-failure-observations" {
			expected = 1
		}
		if len(empty.Statistics) != expected || empty.Statistics[0].Key != "current.observations" || empty.Statistics[0].Value != 0 {
			t.Fatalf("%s empty population statistics: %+v", test.dataset, empty.Statistics)
		}
		if expected == 3 && (empty.Statistics[1].Key != "current.responses" || empty.Statistics[1].Value != 0 || empty.Statistics[2].Key != "current.5xx") {
			t.Fatalf("%s empty population lost its HTTP population: %+v", test.dataset, empty.Statistics)
		}
		encoded, _ := json.Marshal(empty)
		if err := contract.Validate("https://ops.local/schemas/query-result/v1", encoded); err != nil {
			t.Fatalf("%s empty population violates the wire contract: %v", test.dataset, err)
		}
	}
	// The complementary case stays exact: a recorded population with zero 5xx.
	proof := Qualification{Operation: "aggregate", DatasetID: "network.http-5xx", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(binding), EvidenceDigest: graph.ScopeDigest("independent-golden-fixture"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600, ObservationPoint: "s-p", CoverageComplete: true}
	intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "network.http-5xx", Operation: "aggregate", Metrics: []string{"http_5xx_count"}, Dimensions: []string{}, Filters: []Filter{}, Sort: []Sort{}, Limit: 1, TimeRange: window, NetworkSelection: &NetworkSelection{target, peer, "outgoing"}}
	plan, err := NewNetwork(adapter, []Qualification{proof}).Compile(intent, auth)
	if err != nil {
		t.Fatal(err)
	}
	payload = `{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[[200,950,0]]}}`
	observed, _, err := NewNetwork(adapter, []Qualification{proof}).Execute(t.Context(), plan)
	if err != nil || !observed.Exact || observed.Status != "complete" || len(observed.Rows) != 1 || observed.Rows[0].Value != 0 {
		t.Fatalf("recorded population without 5xx lost its exact zero: %+v %v", observed, err)
	}
}

// The empty population is a per-window property. A comparison with one empty
// side must not fabricate a delta from a zero baseline, and a trend must not
// fill missing buckets with zeros.
func TestNetworkEmptyWindowKeepsComparisonAndTrendHonest(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	id := func(uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	}
	target, peer := id("a"), id("b")
	const emptyHTTP = `{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[]}}`
	var responses []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		payload := emptyHTTP
		if len(responses) > 0 {
			payload = responses[0]
			responses = responses[1:]
		}
		fmt.Fprint(w, payload)
	}))
	defer server.Close()
	binding := evidence.Binding{SourceType: "deepflow", Tenant: "tenant-a", SourceID: "52c73f53-20d5-4e55-b33a-f7e97dccae74", Revision: 1, BackendLogicalID: "df", Endpoint: server.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	mapper := networkTestMapper{target: {CanonicalID: target, Namespace: "apps", PodID: 1, ClusterID: 2, NamespaceID: 3}, peer: {CanonicalID: peer, Namespace: "apps", PodID: 2, ClusterID: 2, NamespaceID: 3}}
	adapter, _ := deepflow.New(binding, server.Client(), mapper, func(context.Context, evidence.Binding) error { return nil }, true, "fixture_only")
	auth := Binding{Subject: "operator", Grant: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "grant"}, Selection: Selection{Namespaces: []string{"apps"}}}
	baselineWindow := &QueryTimeRange{now.Add(-3 * time.Minute), now.Add(-2 * time.Minute), "UTC"}
	window := &QueryTimeRange{now.Add(-time.Minute), now, "UTC"}
	proof := Qualification{Operation: "compare", DatasetID: "network.http-summary", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(binding), EvidenceDigest: graph.ScopeDigest("independent-golden-fixture"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600, ObservationPoint: "s-p", CoverageComplete: true}
	intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "network.http-summary", Operation: "compare", Metrics: []string{"http_5xx_percent"}, Dimensions: []string{"comparison"}, Filters: []Filter{}, Sort: []Sort{}, Limit: 3, TimeRange: window, ComparisonTimeRange: baselineWindow, NetworkSelection: &NetworkSelection{target, peer, "outgoing"}}
	plan, err := NewNetwork(adapter, []Qualification{proof}).Compile(intent, auth)
	if err != nil {
		t.Fatal(err)
	}
	// Baseline is empty, current has a recorded population.
	responses = []string{emptyHTTP, `{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[[200,950,0],[503,50,0]]}}`}
	emptyBaseline, _, err := NewNetwork(adapter, []Qualification{proof}).Execute(t.Context(), plan)
	if err != nil || emptyBaseline.Exact || emptyBaseline.Status != "partial" || len(emptyBaseline.Rows) != 1 || emptyBaseline.Rows[0].Dimension != "current" || emptyBaseline.Rows[0].Value != 5 {
		t.Fatalf("empty baseline produced a fabricated comparison: %+v %v", emptyBaseline, err)
	}
	if len(emptyBaseline.Statistics) != 7 || emptyBaseline.Statistics[0].Key != "baseline.observations" || emptyBaseline.Statistics[0].Value != 0 {
		t.Fatalf("empty baseline lost its explicit zero population: %+v", emptyBaseline.Statistics)
	}
	encoded, _ := json.Marshal(emptyBaseline)
	if err := contract.Validate("https://ops.local/schemas/query-result/v1", encoded); err != nil {
		t.Fatalf("partial comparison violates the wire contract: %v", err)
	}
	trendProof := proof
	trendProof.Operation = "trend"
	trendProof.DatasetID = "network.http-5xx"
	trendIntent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "network.http-5xx", Operation: "trend", Metrics: []string{"http_5xx_count"}, Dimensions: []string{"time"}, Filters: []Filter{}, Sort: []Sort{}, Limit: 3, TimeRange: &QueryTimeRange{now.Add(-3 * time.Minute), now, "UTC"}, StepSeconds: 60, NetworkSelection: &NetworkSelection{target, peer, "outgoing"}}
	trendPlan, err := NewNetwork(adapter, []Qualification{trendProof}).Compile(trendIntent, auth)
	if err != nil {
		t.Fatal(err)
	}
	// Three buckets: the middle one has no completed sessions at all.
	responses = []string{
		`{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[[200,900,0],[503,100,0]]}}`,
		`{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[]}}`,
		`{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[[200,1000,0]]}}`,
	}
	trend, _, err := NewNetwork(adapter, []Qualification{trendProof}).Execute(t.Context(), trendPlan)
	if err != nil || trend.Exact || trend.Status != "partial" || len(trend.Rows) != 2 {
		t.Fatalf("empty trend bucket was filled or accepted as exact: %+v %v", trend, err)
	}
	for index, bucket := range []time.Time{now.Add(-3 * time.Minute), now.Add(-time.Minute)} {
		if trend.Rows[index].Dimension != bucket.Format(time.RFC3339) {
			t.Fatalf("empty trend bucket was filled instead of skipped: %+v", trend.Rows)
		}
	}
	found := false
	for _, warning := range trend.Warnings {
		found = found || strings.HasSuffix(warning, "_empty_observation_population") && warning != "current_empty_observation_population"
	}
	if !found {
		t.Fatalf("empty trend bucket missing its bucket-scoped gap warning: %+v", trend.Warnings)
	}
	encoded, _ = json.Marshal(trend)
	if err := contract.Validate("https://ops.local/schemas/query-result/v1", encoded); err != nil {
		t.Fatalf("partial trend violates the wire contract: %v", err)
	}
}
