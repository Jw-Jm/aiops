package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation/tools"
)

// The same real input must pass both contracts: the Graph compiler accepts the
// current resource aggregate, and the MCP reservation reserves exactly the one
// source request the query issues. The reservation is persisted before the source
// is called, so an under-reservation would let a query run without a budget.
func TestCurrentResourceIntentCompilesAndReservesOneQuery(t *testing.T) {
	intent := analytics.QueryIntent{
		SchemaVersion: "query-intent/v1", DatasetID: "resources.current", Operation: "aggregate",
		Metrics: []string{"resource_count"}, Dimensions: []string{"kind"},
		Filters: []analytics.Filter{{Field: "kind", Operator: "eq", Value: "Pod"}}, Sort: []analytics.Sort{}, Limit: 50,
	}
	grant := graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "grant-v1"}
	if _, err := analytics.Compile(intent, analytics.Binding{Subject: "operator-a", Grant: grant, OwnerEpoch: 1}); err != nil {
		t.Fatalf("the current resource aggregate must compile: %v", err)
	}
	args, err := json.Marshal(map[string]any{"intent": intent})
	if err != nil {
		t.Fatal(err)
	}
	reserve, err := toolReservation(tools.Definition{Name: "execute_analysis_query", RawQuery: true}, args)
	if err != nil {
		t.Fatalf("the current resource aggregate must reserve: %v", err)
	}
	if reserve.RawQueries != 1 {
		t.Fatalf("reserved raw queries = %d, want 1", reserve.RawQueries)
	}
	// A time-series expansion still reserves its real number of source requests.
	now := time.Now().UTC().Truncate(time.Second)
	series := intent
	series.DatasetID, series.Metrics = "logs.records", []string{"log_count"}
	series.Operation = "trend"
	series.StepSeconds = 60
	series.TimeRange = &analytics.QueryTimeRange{From: now.Add(-49 * time.Minute), To: now}
	series.Dimensions = []string{"time"}
	series.Filters = nil
	seriesArgs, err := json.Marshal(map[string]any{"intent": series})
	if err != nil {
		t.Fatal(err)
	}
	seriesReserve, err := toolReservation(tools.Definition{Name: "execute_analysis_query", RawQuery: true}, seriesArgs)
	if err != nil || seriesReserve.RawQueries != 50 {
		t.Fatalf("series reservation = %d (%v), want 50", seriesReserve.RawQueries, err)
	}
}
