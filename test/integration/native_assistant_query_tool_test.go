package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"ops-platform/internal/analytics"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
)

// A production control-plane tool must re-prove persisted native Job authority,
// without requiring a browser session or falling back to legacy role bindings.
func TestNativeAssistantAuditQueryToolWithoutBrowserSession(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccountWithRole(t, "platform_admin", `{}`)
	_, turn := f.submitAudit(t, account, "原生审计真实工具")
	workerCtx := context.Background()
	lease, err := f.jobs.Claim(workerCtx, f.tenantID, "native-query-tool", 45*time.Second)
	if err != nil || lease.JobID != turn.JobID {
		t.Fatalf("claim: %v", err)
	}
	job, err := f.jobs.Get(workerCtx, f.tenantID, turn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "audit.events", Operation: "aggregate", Metrics: []string{"audit_count"}, Dimensions: []string{}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 50, TimeRange: &analytics.QueryTimeRange{From: now.Add(-time.Hour), To: now, Timezone: "Asia/Shanghai"}}
	args, err := json.Marshal(map[string]any{"intent": intent})
	if err != nil {
		t.Fatal(err)
	}
	adapter := httpapi.AssistantTools{Legacy: httpapi.InvestigationTools{SP04: &httpapi.SP04Handlers{Pool: f.apiPool}}}
	result, err := adapter.Invoke(workerCtx, job, "execute_analysis_query", args)
	if err != nil || result.State != "succeeded" {
		t.Fatalf("native workload audit query: state=%s err=%v", result.State, err)
	}
	raw, _ := json.Marshal(result.Data)
	var data struct {
		Stored investigation.StoredQueryResult `json:"storedQueryResult"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Stored.Result.ControlPlaneSnapshot == "" || !data.Stored.Result.Exact || len(data.Stored.Result.Rows) != 1 || data.Stored.Result.Rows[0].Value < 1 {
		t.Fatal("real audit aggregation/snapshot missing")
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='query regression',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Invoke(workerCtx, job, "execute_analysis_query", args); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked tool query: %v", err)
	}
}
