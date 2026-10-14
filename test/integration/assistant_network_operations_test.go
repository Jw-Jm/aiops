package integration

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/deepflow"
	"ops-platform/internal/resource"
)

// This verifies the real source adapter and analytics compiler; the existing
// fixed-turn acceptance separately verifies signed RPC, archival and readback.
func assistantActualNetworkOperations(t *testing.T, ctx context.Context, adapter *deepflow.Adapter, proofs []analytics.Qualification, scope graph.Scope, target, namespace string, raw []byte) {
	t.Helper()
	var windows struct {
		Golden []struct {
			From int64 `json:"fromUnix"`
			To   int64 `json:"toUnix"`
		} `json:"golden"`
	}
	if json.Unmarshal(raw, &windows) != nil || len(windows.Golden) != 3 {
		t.Fatal("three independent windows required")
	}
	var pods struct {
		Items []struct{ Metadata struct{ UID string } }
	}
	if json.Unmarshal(sp04Kubectl(t, ctx, nil, "-n", namespace, "get", "pods", "-l", "app=fault-chain-server,ops.platform.io/test-release=fault-chain-20261014", "-o", "json"), &pods) != nil || len(pods.Items) != 1 {
		t.Fatal("owned peer required")
	}
	peer := resource.CanonicalID{Domain: "k8s", Tenant: scope.Tenant, Scope: scope.Cluster, APIGroup: "core", Kind: "Pod", StableID: pods.Items[0].Metadata.UID}.String()
	window := func(i int) *analytics.QueryTimeRange {
		return &analytics.QueryTimeRange{From: time.Unix(windows.Golden[i].From, 0).UTC(), To: time.Unix(windows.Golden[i].To, 0).UTC(), Timezone: "Asia/Shanghai"}
	}
	now := time.Now().UTC()
	if err := adapter.QualifyL7(ctx, evidence.Query{ResourceCanonicalID: peer, Namespace: namespace, Template: "GetL7Context", From: now.Add(-10 * time.Second), To: now, Limit: 100, Scope: scope}); err != nil {
		t.Fatal("real L7 scope probe", err)
	}
	network := analytics.NewNetwork(adapter, proofs)
	execute := func(dataset, op string, trend bool) analytics.QueryResult {
		intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: dataset, Operation: op, Metrics: []string{"http_5xx_percent"}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 3, TimeRange: window(1), ComparisonTimeRange: window(0), Dimensions: []string{"comparison"}, NetworkSelection: &analytics.NetworkSelection{TargetCanonicalID: target, PeerCanonicalID: peer, Direction: "outgoing"}}
		if dataset == "network.tcp-summary" {
			intent.Metrics = []string{"connection_failure_observation_count"}
		}
		expectedCost := int64(2)
		if trend {
			intent.ComparisonTimeRange = nil
			intent.Dimensions = []string{"time"}
			intent.StepSeconds = 60
			intent.TimeRange = &analytics.QueryTimeRange{From: window(0).From, To: window(2).To, Timezone: "Asia/Shanghai"}
			expectedCost = 3
		}
		if analytics.NetworkRawQueryCost(intent) != expectedCost {
			t.Fatal("incorrect raw subquery admission cost")
		}
		plan, err := network.Compile(intent, analytics.Binding{Subject: "source-isolation-probe", Grant: scope, Selection: analytics.Selection{Namespaces: []string{namespace}}})
		if err != nil {
			t.Fatal("real operation compile", dataset, op, err)
		}
		result, evidence, err := network.Execute(ctx, plan)
		if err != nil {
			t.Fatal("real operation execute", dataset, op, err)
		}
		if int64(len(evidence)) != expectedCost || len(result.DerivationEvidenceRefs) != len(evidence) {
			t.Fatal("subquery evidence count differs from admission cost")
		}
		encoded, _ := json.Marshal(result)
		if err := contract.Validate("https://ops.local/schemas/query-result/v1", encoded); err != nil {
			t.Fatal("real operation wire contract", err)
		}
		if result.NetworkContext == nil || result.NetworkContext.TimeReliable {
			t.Fatal("source clock incorrectly qualified")
		}
		return result
	}
	trend := execute("network.http-summary", "trend", true)
	if !trend.Exact || trend.Status != "complete" || len(trend.Rows) != 3 {
		t.Fatal("real trend incomplete")
	}
	for i, want := range []float64{1, 5, 0} {
		if trend.Rows[i].Value != want || trend.Rows[i].Dimension != window(i).From.Format(time.RFC3339) {
			t.Fatal("real trend differs from independent bucket oracle", i)
		}
	}
	percent := execute("network.http-summary", "compare_percent", false)
	if !percent.Exact || percent.Status != "complete" || percent.Unit != "percent" || len(percent.Rows) != 1 || percent.Rows[0].Dimension != "increase" || percent.Rows[0].Value != 400 {
		t.Fatal("real 1 to 5 percent comparison is not 400 percent increase")
	}
	zero := execute("network.tcp-summary", "compare_percent", false)
	if zero.Exact || zero.Status != "no_data" || len(zero.Rows) != 0 || !slices.Contains(zero.Warnings, "percentage_requires_nonzero_baseline") {
		t.Fatal("real zero baseline invented percentage")
	}
	report, _ := json.MarshalIndent(map[string]any{"schemaVersion": "fault-chain-network-operations/v1", "passed": true, "adapterMode": "live", "scope": "real source adapter and compiler; not signed RPC or archived readback", "sourceProofDigest": graph.ScopeDigest(json.RawMessage(raw)), "trend": trend, "relativeComparison": percent, "zeroBaseline": zero}, "", "  ")
	if err := os.WriteFile(os.Getenv("OPS_FAULT_CHAIN_OPERATIONS_REPORT"), report, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("real source trend 1/5/0; relative increase 400%; zero-baseline percentage omitted; subquery evidence and contracts verified")
}
