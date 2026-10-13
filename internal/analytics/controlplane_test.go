package analytics

import (
	"errors"
	"ops-platform/internal/graph"
	"testing"
	"time"
)

func TestControlPlaneCompilerClosedQueries(t *testing.T) {
	now := time.Now().UTC()
	intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "findings.occurrences", Operation: "aggregate", Metrics: []string{"occurrence_count"}, Dimensions: []string{"day"}, Filters: []Filter{}, Sort: []Sort{}, Limit: 50, TimeRange: &QueryTimeRange{From: now.Add(-time.Hour), To: now, Timezone: "Asia/Shanghai"}}
	binding := Binding{Subject: "operator", Grant: graph.Scope{Tenant: "tenant", Cluster: "cluster", Namespaces: []string{"apps"}, AuthorizationRevision: "revision"}}
	plan, err := CompileControlPlane(intent, binding, "operational")
	if err != nil || plan.queryID != "findings.occurrences/v1" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	original := plan.digest
	intent.TimeRange.From = now.Add(-10 * time.Hour)
	binding.Grant.Namespaces[0] = "private"
	if plan.intent.TimeRange.From.Equal(intent.TimeRange.From) || plan.scope.Namespaces[0] != "apps" || plan.digest != original {
		t.Fatal("compiled plan mutated through caller data")
	}
	intent.TimeRange.From = now.Add(-time.Hour)
	intent.Metrics = []string{"count(*) FROM audit.records"}
	if _, err = CompileControlPlane(intent, binding, "operational"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expression accepted: %v", err)
	}
	intent.Metrics = []string{"occurrence_count"}
	intent.Filters = []Filter{{Field: "namespace", Operator: "eq", Value: "outside"}}
	if _, err = CompileControlPlane(intent, binding, "operational"); !errors.Is(err, ErrDenied) {
		t.Fatalf("outside namespace accepted: %v", err)
	}
	intent.Filters = []Filter{}
	intent.DatasetID = "audit.events"
	intent.Metrics = []string{"audit_count"}
	if _, err = CompileControlPlane(intent, binding, "operational"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("admin role inherited from operational grant: %v", err)
	}
}
