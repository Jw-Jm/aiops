package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
)

// PG authority and native task transport are real. The graph summary is a
// protocol fixture; deployed live queries supply the source accuracy evidence.
func TestNativeResourceAnalyticsWithoutBrowserSession(t *testing.T) {
	f := newNativeJobFixture(t)
	a := f.newAccount(t, `"apps"`)
	_, turn := f.submit(t, a, "Native resource aggregation")
	ctx := context.Background()
	lease, err := f.jobs.Claim(ctx, f.tenantID, "resource-query-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.jobs.Get(ctx, f.tenantID, turn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q graph.Query
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		raw, _ := json.Marshal(q)
		claims, err := graph.VerifyContext(public, r.Header.Get("X-Graph-Context"), "platform-graph-query", raw)
		if err != nil {
			t.Error(err)
			w.WriteHeader(403)
			return
		}
		current, err := (graph.Authorization{Pool: f.workerPool}).EffectiveClaim(r.Context(), claims)
		if err != nil || !claims.AllowsCurrent(current) {
			t.Errorf("task reproof: %v", err)
			w.WriteHeader(403)
			return
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": "resource-graph/v2", "graphRevision": map[string]int64{"ownerEpoch": 1, "graphGeneration": 1}, "ownerInstance": "resource-fixture", "collectedAt": time.Now().UTC(), "freshness": "fresh", "partial": false,
			"warnings": []string{}, "degradedSources": []string{}, "nodes": []any{}, "edges": []any{}, "rankedEvidence": []any{}, "conflicts": []any{},
			"budgets": map[string]any{"maxDepth": 1, "storageMaxDepth": 1, "maxNodes": 1, "maxEdges": 1, "nodeCount": 0, "edgeCount": 0, "truncated": false},
			"summary": map[string]any{"total": 0, "byKind": map[string]int{"Pod": 0}, "byHealth": map[string]int{"normal": 0}},
		})
	}))
	defer worker.Close()
	if _, err = f.db.ExecContext(f.ctx, `INSERT INTO platform.graph_ownership(tenant_id,cluster_uid,owner_epoch,owner_instance,owner_endpoint,lease_uid,route_expires_at) VALUES($1,$2,1,'resource-fixture',$3,$4,clock_timestamp()+interval '5 minutes')`, f.tenantID, f.clusterUID, worker.URL, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	h := &httpapi.SP04Handlers{Pool: f.apiPool, SigningKey: key, Client: worker.Client(), EndpointAllowed: func(endpoint string) bool { return endpoint == worker.URL }}
	api := httpapi.AssistantTools{Legacy: httpapi.InvestigationTools{SP04: h}}
	intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "resources.current", Operation: "aggregate", Metrics: []string{"resource_count"}, Dimensions: []string{"health"}, Filters: []analytics.Filter{{Field: "kind", Operator: "eq", Value: "Pod"}}, Sort: []analytics.Sort{}, Limit: 50}
	args, _ := json.Marshal(map[string]any{"intent": intent})
	result, err := api.Invoke(ctx, job, "execute_analysis_query", args)
	if err != nil || result.State != "succeeded" || calls.Load() != 1 {
		t.Fatalf("native resource query state=%s calls=%d error=%v", result.State, calls.Load(), err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='resource query regression' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, a.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = api.Invoke(ctx, job, "execute_analysis_query", args); !errors.Is(err, investigation.ErrDenied) && !errors.Is(err, analytics.ErrDenied) {
		t.Fatalf("revoked query error=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("revoked authority reached graph transport")
	}
	f.quarantine(t, lease.JobID)
}
