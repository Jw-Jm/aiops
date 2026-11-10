package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
)

// The tool executes real PostgreSQL analytics, settles its real output under a
// native Worker lease, then the API runtime role drills that saved result.
func TestNativeAssistantDrillPreservesCommittedScope(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps","other"`)
	seed := f.newIncident(t)
	f.newIncidentFamily(t, "node", "NotReady", "different-target")
	conv, err := f.repo.Create(account.ctx, assistant.Conversation{TenantID: f.tenantID, OwnerSubject: account.subject, Title: "Scoped drill", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: f.clusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}, CanonicalIDs: []string{seed.Canonical}}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := f.repo.Submit(account.ctx, f.tenantID, conv.ConversationID, account.subject, "drill-turn", assistant.SubmitMessage{Message: "统计本资源告警", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	worker := context.Background()
	lease, err := f.jobs.Claim(worker, f.tenantID, "drill-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.jobs.Get(worker, f.tenantID, turn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	signer := investigation.ContextSigner{Transit: newNativeTestTransit(t), Key: "native-action-key"}
	defs, err := tools.ForJob(job)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.IssueRegistered(worker, f.jobs, job, lease, "platform-job-api", nativeAssistantWorkload, tools.Names(defs))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.VerifyContext(worker, token, "platform-job-api", nativeAssistantWorkload)
	if err != nil {
		t.Fatal(err)
	}
	_, lease, err = f.jobs.Authenticate(worker, claims, job.ToolCatalogDigest)
	if err != nil {
		t.Fatal(err)
	}
	adapter := httpapi.AssistantTools{Legacy: httpapi.InvestigationTools{SP04: &httpapi.SP04Handlers{Pool: f.apiPool}}}
	ids := map[string]uuid.UUID{}
	for _, label := range []string{"findings.occurrences", "incidents.records", "legacy"} {
		dataset := label
		if label == "legacy" {
			dataset = "findings.occurrences"
		}
		metric := "occurrence_count"
		if dataset == "incidents.records" {
			metric = "incident_count"
		}
		now := time.Now().UTC()
		intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: dataset, Operation: "aggregate", Metrics: []string{metric}, Dimensions: []string{"namespace"}, Filters: []analytics.Filter{{Field: "namespace", Operator: "eq", Value: "apps"}}, Sort: []analytics.Sort{}, Limit: 50, TimeRange: &analytics.QueryTimeRange{From: now.Add(-time.Hour), To: now, Timezone: "UTC"}}
		if label == "legacy" {
			intent.Dimensions = []string{"state"}
		}
		args, _ := json.Marshal(map[string]any{"intent": intent})
		allocation, e := f.jobs.AllocateTool(worker, lease, "execute_analysis_query", args)
		if e != nil {
			t.Fatal(e)
		}
		call := investigation.Call{Claims: &claims, StepID: allocation.StepID, ContextID: claims.ContextID, JTI: allocation.JTI, Name: "execute_analysis_query", Arguments: args, ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 1 << 20}}
		if _, e = f.jobs.BeginCall(worker, lease, call); e != nil {
			t.Fatal(e)
		}
		result, e := adapter.Invoke(worker, job, "execute_analysis_query", args)
		if e != nil {
			t.Fatal(e)
		}
		// Model the pre-descriptor wire format before settlement. Committed
		// steps are immutable, so history is never rewritten for the test.
		if label == "legacy" {
			delete(result.Data.(map[string]any), "filters")
		}
		raw, _ := json.Marshal(result)
		if _, e = f.jobs.CompleteToolStep(worker, lease, allocation.StepID, raw, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(raw))}); e != nil {
			t.Fatal(e)
		}
		var data struct {
			Stored investigation.StoredQueryResult `json:"storedQueryResult"`
		}
		encoded, _ := json.Marshal(result.Data)
		if e = json.Unmarshal(encoded, &data); e != nil {
			t.Fatal(e)
		}
		ids[label] = data.Stored.QueryResultID
		_, scope, e := f.repo.QueryResultWithScope(account.ctx, f.tenantID, data.Stored.QueryResultID, account.subject)
		if e != nil || scope.FiltersKnown != (label != "legacy") || (label != "legacy" && len(scope.Filters) != 1) || len(scope.CanonicalIDs) != 1 || scope.CanonicalIDs[0] != seed.Canonical {
			t.Fatalf("scope=%+v err=%v", scope, e)
		}
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	handler := &httpapi.SP05Handlers{Enabled: true, Pool: f.apiPool, GraphAPI: &httpapi.SP04Handlers{SigningKey: key}}
	actor := func(a nativeJobAccount) context.Context {
		return auth.WithRequestContext(a.ctx, auth.RequestContext{TenantID: f.tenantID, Subject: a.subject, Roles: []auth.Role{auth.Operator}, RequestID: "native-drill", TokenExpiresAt: time.Now().Add(time.Hour)})
	}
	read := func(ctx context.Context, path string) (int, map[string]json.RawMessage) {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r.WithContext(ctx))
		var body map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	for dataset, id := range ids {
		if dataset == "legacy" {
			continue
		}
		path := "/api/v1/findings"
		want := 2
		if dataset == "incidents.records" {
			path = "/api/v1/incidents"
			want = 1
		}
		address := path + "?clusterUid=" + f.clusterUID + "&assistantQueryResultId=" + id.String() + "&assistantGroup=apps&limit=1"
		count := 0
		for page := 0; page < 4; page++ {
			status, body := read(actor(account), address)
			if status != 200 {
				t.Fatalf("%s status=%d body=%s", dataset, status, body)
			}
			var rows []struct {
				Canonical string `json:"resourceCanonicalId"`
			}
			_ = json.Unmarshal(body["data"], &rows)
			for _, row := range rows {
				if row.Canonical != seed.Canonical {
					t.Fatalf("scope broadened: %s", row.Canonical)
				}
				count++
			}
			var meta struct {
				Next string `json:"nextCursor"`
			}
			_ = json.Unmarshal(body["meta"], &meta)
			if meta.Next == "" {
				break
			}
			address = path + "?clusterUid=" + f.clusterUID + "&assistantQueryResultId=" + id.String() + "&assistantGroup=apps&limit=1&cursor=" + meta.Next
		}
		if count != want {
			t.Fatalf("%s rows=%d want%d", dataset, count, want)
		}
		other := f.newAccount(t, `"apps"`)
		if status, _ := read(actor(other), address); status != 403 {
			t.Fatalf("foreign read=%d", status)
		}
		if status, _ := read(actor(account), path+"?clusterUid="+f.clusterUID+"&assistantQueryResultId="+id.String()+"&assistantGroup=other"); status != 403 {
			t.Fatalf("forged group=%d", status)
		}
	}
	legacyID := ids["legacy"]
	if _, oldScope, e := f.repo.QueryResultWithScope(account.ctx, f.tenantID, legacyID, account.subject); e != nil || oldScope.FiltersKnown {
		t.Fatalf("legacy descriptor scope=%+v error=%v", oldScope, e)
	}
	if status, _ := read(actor(account), "/api/v1/findings?clusterUid="+f.clusterUID+"&assistantQueryResultId="+legacyID.String()); status != 403 {
		t.Fatalf("unknown original filters were broadened: %d", status)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='drill regression' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if status, _ := read(actor(account), "/api/v1/incidents?clusterUid="+f.clusterUID+"&assistantQueryResultId="+ids["incidents.records"].String()); status != 403 {
		t.Fatalf("revoked read=%d", status)
	}
	f.quarantine(t, turn.JobID)
}
