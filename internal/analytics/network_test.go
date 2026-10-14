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
