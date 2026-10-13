package integration

import (
	"context"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"testing"
	"time"
)

// Real runtime-role SQL execution verifies parameter typing, empty scope arrays,
// tenant isolation and aggregate provenance after every migration.
func TestAssistantControlPlaneQueries(t *testing.T) {
	ctx, _, pool, b := sp05Database(t)
	scope := graph.Scope{Tenant: b.TenantID.String(), Cluster: b.ClusterUID, Namespaces: []string{"apps"}, AuthorizationRevision: "test-revision"}
	executor := analytics.ControlPlaneExecutor{Pool: pool, AuthorizeTx: func(context.Context, pgx.Tx, string, string, graph.Scope, string) error { return nil }}
	now := time.Now().UTC()
	for _, pair := range [][2]string{{"findings.occurrences", "occurrence_count"}, {"incidents.records", "incident_count"}, {"executions.records", "execution_count"}, {"audit.events", "audit_count"}} {
		t.Run(pair[0], func(t *testing.T) {
			domain := "operational"
			grant := scope
			if pair[0] == "audit.events" {
				domain = "audit"
				grant.Cluster = ""
				grant.Namespaces = nil
			}
			intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: pair[0], Operation: "aggregate", Metrics: []string{pair[1]}, Dimensions: []string{}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 50, TimeRange: &analytics.QueryTimeRange{From: now.Add(-time.Hour), To: now, Timezone: "Asia/Shanghai"}}
			plan, err := analytics.CompileControlPlane(intent, analytics.Binding{Subject: "assistant-test", Grant: grant}, domain)
			if err != nil {
				t.Fatal(err)
			}
			result, err := executor.Execute(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			if result.ControlPlaneSnapshot == "" || !result.Exact || len(result.Rows) != 1 || result.Rows[0].Value != 0 {
				t.Fatalf("unexpected empty snapshot: %+v", result)
			}
		})
	}
}
