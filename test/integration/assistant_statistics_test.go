package integration

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestAssistantDerivedStatistics(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	now := time.Now().UTC().Truncate(time.Second)
	scope := graph.Scope{Tenant: b.TenantID.String(), Cluster: b.ClusterUID, Namespaces: []string{"apps"}, AuthorizationRevision: "fixture"}
	executor := analytics.ControlPlaneExecutor{Pool: pool, AuthorizeTx: func(context.Context, pgx.Tx, string, string, graph.Scope, string) error { return nil }}
	query := func(dataset, metric string) analytics.QueryResult {
		t.Helper()
		p, err := analytics.CompileControlPlane(analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: dataset, Operation: "aggregate", Metrics: []string{metric}, Dimensions: []string{}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 50, TimeRange: &analytics.QueryTimeRange{From: now.Add(-time.Hour), To: now, Timezone: "UTC"}}, analytics.Binding{Subject: "assistant-test", Grant: scope}, "operational")
		if err != nil {
			t.Fatal(err)
		}
		out, err := executor.Execute(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(out)
		if err = contract.Validate("https://ops.local/schemas/query-result/v1", raw); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, pair := range [][2]string{{"incidents.recovery-duration", "mean_recovery_ms"}, {"executions.success-rate", "success_percent"}} {
		out := query(pair[0], pair[1])
		if out.Exact || out.Status != "no_data" || len(out.Rows) != 0 {
			t.Fatalf("empty statistic became zero: %+v", out)
		}
	}
	for _, row := range []struct {
		ns       string
		duration time.Duration
	}{{"apps", time.Minute}, {"apps", 3 * time.Minute}, {"private", time.Hour}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO incident.records(tenant_id,incident_id,state,cluster_uid,namespace,started_at,resolved_at) VALUES($1,$2,'resolved',$3,$4,$5,$6)`, b.TenantID, uuid.New(), b.ClusterUID, row.ns, now.Add(-10*time.Minute).Add(-row.duration), now.Add(-10*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	out := query("incidents.recovery-duration", "mean_recovery_ms")
	if !out.Exact || len(out.Rows) != 1 || out.Rows[0].Value != 120000 {
		t.Fatalf("authorized mean: %+v", out)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO incident.records(tenant_id,incident_id,state,cluster_uid,namespace,resolved_at) VALUES($1,$2,'resolved',$3,'apps',$4)`, b.TenantID, uuid.New(), b.ClusterUID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	out = query("incidents.recovery-duration", "mean_recovery_ms")
	if out.Exact || out.Status != "partial" || out.Rows[0].Value != 120000 {
		t.Fatalf("unreliable start coerced: %+v", out)
	}
	// Execution rows are fixture observations only: no command is dispatched.
	profile := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.execution_profile_versions(tenant_id,profile_version_id,logical_name,version_number,content,digest,signature,signer_key_id,status,published_by) VALUES($1,$2,'statistics-fixture',1,'{}',$3,'x','fixture','published','fixture')`, b.TenantID, profile, graph.ScopeDigest("profile")); err != nil {
		t.Fatal(err)
	}
	for index, row := range []struct{ subject, ns, state string }{{"assistant-test", "apps", "succeeded"}, {"assistant-test", "apps", "succeeded"}, {"assistant-test", "apps", "succeeded"}, {"assistant-test", "apps", "failed"}, {"assistant-test", "apps", "cancelled"}, {"assistant-test", "apps", "execution_unknown"}, {"assistant-test", "private", "failed"}, {"other-owner", "apps", "failed"}} {
		target := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: uuid.NewString()}.String()
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod',$4,'fixture','{}',clock_timestamp())`, b.TenantID, target, b.ClusterID, row.ns); err != nil {
			t.Fatal(err)
		}
		ack := uuid.New()
		digest := graph.ScopeDigest(index)
		if _, err := db.ExecContext(ctx, `INSERT INTO action.risk_acknowledgements(tenant_id,acknowledgement_id,subject,target_canonical_id,shell,command_digest,risk_level,confirmed_at,expires_at) VALUES($1,$2,$3,$4,'bash',$5,'low',$6,$7)`, b.TenantID, ack, row.subject, target, digest, now.Add(-time.Hour), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO action.executions(tenant_id,execution_id,subject,target_canonical_id,shell,command_digest,risk_acknowledgement_id,execution_profile_version_id,idempotency_key_digest,state,created_at) VALUES($1,$2,$3,$4,'bash',$5,$6,$7,$5,$8,$9)`, b.TenantID, uuid.New(), row.subject, target, digest, ack, profile, row.state, now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	out = query("executions.success-rate", "success_percent")
	if !out.Exact || len(out.Rows) != 1 || out.Rows[0].Value != 75 || out.Unit != "percent" {
		t.Fatalf("owner/scope denominator: %+v", out)
	}

}
