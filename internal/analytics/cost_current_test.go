package analytics

import (
	"errors"
	"testing"
	"time"
)

// The current resource aggregate is an instant query over the live graph. It has
// no time window, the compiler rejects a fictional one, and the budget contract
// must therefore accept the same intent without a window instead of reserving a
// different number of source requests than the query really issues.
func TestSourceRequestCostAcceptsCurrentResourceAggregateWithoutWindow(t *testing.T) {
	intent := QueryIntent{
		SchemaVersion: "query-intent/v1", DatasetID: "resources.current", Operation: "aggregate",
		Metrics: []string{"resource_count"}, Dimensions: []string{"kind"}, Filters: []Filter{}, Sort: []Sort{}, Limit: 50,
	}
	cost, err := SourceRequestCost(intent)
	if err != nil || cost != 1 {
		t.Fatalf("current resource aggregate cost = %d (%v), want 1", cost, err)
	}
	// A time-series source keeps its strict window contract.
	series := intent
	series.DatasetID, series.Metrics = "logs.records", []string{"log_count"}
	if _, err := SourceRequestCost(series); !errors.Is(err, ErrInvalid) {
		t.Fatalf("time-series source without a window = %v", err)
	}
	// A half-open or inverted window is still invalid for a time-series source.
	now := time.Now().UTC().Truncate(time.Second)
	series.TimeRange = &QueryTimeRange{From: now, To: now}
	if _, err := SourceRequestCost(series); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty time-series window = %v", err)
	}
	// The dataset and the operation are resolved before the window, so an unknown
	// dataset is still unsupported and not a window problem.
	unknown := intent
	unknown.DatasetID = "resources.unknown"
	if _, err := SourceRequestCost(unknown); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown dataset = %v", err)
	}
	unsupported := intent
	unsupported.Operation = "trend"
	if _, err := SourceRequestCost(unsupported); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported operation = %v", err)
	}
}
