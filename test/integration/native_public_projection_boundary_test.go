package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
)

// submitIncident creates a troubleshoot session bound to the incident and one
// queued business turn for the account. The turn produces a unified entry job
// (investigation-job/v3) that carries the incident and its revision. A session
// without an incident would produce a data query job, and such a job may never
// be attached to an incident.
func (f *nativeJobFixture) submitIncident(t *testing.T, account nativeJobAccount, incidentID uuid.UUID, title string) (uuid.UUID, assistant.Turn) {
	t.Helper()
	conversation, err := f.repo.Create(account.ctx, assistant.Conversation{
		TenantID: f.tenantID, OwnerSubject: account.subject, Title: title, Mode: "troubleshoot",
		AuthorizationDomain: "operational", ClusterUID: f.clusterUID,
		Binding: assistant.ConversationBinding{Timezone: "UTC", IncidentID: incidentID, Selection: analytics.Selection{Namespaces: []string{"apps"}}},
	})
	if err != nil {
		t.Fatalf("create the incident conversation: %v", err)
	}
	turn, err := f.repo.Submit(account.ctx, f.tenantID, conversation.ConversationID, account.subject, "turn-"+uuid.Must(uuid.NewV7()).String(), assistant.SubmitMessage{Message: "查看故障上下文与依赖关系", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatalf("submit the incident turn: %v", err)
	}
	return conversation.ConversationID, turn
}

// The public investigation contract of the unified entry. This is a real
// PostgreSQL run against the API runtime role with a real native session:
// the owner reads the job, the steps and the history of the incident, and no
// row of the answer may publish the internal assistant binding. A foreign
// subject of the same tenant and a subject of another tenant are refused and
// change nothing: no state, no ledger and no audit record.
//
// The cancellation boundary is proved with the request the API really accepts:
// a legal JSON body and exactly one stable idempotency key. A request without
// them is refused by the handler before the transition and proves nothing
// about the owner boundary, so it is asserted separately as its own case.
func TestNativePublicInvestigationProjectionBoundary(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	// The incident of the history. It exists before the unified entry job, so
	// the job carries the incident and its revision through the real session
	// path instead of an update of the stored row.
	seed := f.newIncident(t)
	// A second session of the same owner without an incident. The link of a v3
	// history row must point at the exact session that produced the job and
	// never at another session of the same owner.
	otherConversationID, _ := f.submit(t, account, "P4-05 another session")
	conversationID, turn := f.submitIncident(t, account, seed.IncidentID, "P4-05 public projection")
	if turn.JobID == uuid.Nil {
		t.Fatal("the unified entry turn produced no job")
	}
	if conversationID == otherConversationID {
		t.Fatal("the two sessions of the owner share one conversation")
	}
	// The job really carries the internal admission binding, so the leak
	// assertion below is meaningful.
	var binding string
	if err := f.db.QueryRowContext(f.ctx, `SELECT COALESCE(assistant_binding::text,'') FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`,
		f.tenantID, turn.JobID).Scan(&binding); err != nil {
		t.Fatalf("read the assistant binding of the job: %v", err)
	}
	var inputDigest, contextDigest string
	if err := f.db.QueryRowContext(f.ctx, `SELECT COALESCE(input_digest,''),COALESCE(context_digest,'') FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2`,
		f.tenantID, turn.TurnID).Scan(&inputDigest, &contextDigest); err != nil {
		t.Fatalf("read the frozen digests of the turn: %v", err)
	}
	if binding == "" || inputDigest == "" || contextDigest == "" {
		t.Fatalf("the fixture job carries no assistant binding: %q %q %q", binding, inputDigest, contextDigest)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate the history cursor key: %v", err)
	}
	key := ed25519.PrivateKey(private)
	_ = public
	handlers := &httpapi.InvestigationHandlers{
		Repository: investigation.Repository{Pool: f.apiPool},
		CursorKey:  key,
	}
	owner := func(ctx context.Context) context.Context {
		return auth.WithRequestContext(ctx, auth.RequestContext{
			TenantID:       f.tenantID,
			Subject:        account.subject,
			Roles:          []auth.Role{auth.Operator},
			RequestID:      "p405-native-projection",
			TokenExpiresAt: time.Now().Add(time.Hour),
		})
	}
	call := func(ctx context.Context, method, path, body string, headers map[string]string) (int, string) {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		request := httptest.NewRequest(method, path, reader)
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		handlers.ServeHTTP(response, request.WithContext(ctx))
		return response.Code, response.Body.String()
	}
	read := func(ctx context.Context, path string) (int, string) {
		return call(ctx, http.MethodGet, path, "", nil)
	}
	cancel := func(ctx context.Context, jobID uuid.UUID, key string) (int, string) {
		return call(ctx, http.MethodPost, "/api/v1/investigations/"+jobID.String()+":cancel", "{}", map[string]string{"Idempotency-Key": key})
	}
	leaked := []string{
		"assistant_binding", "assistantBinding", "grantSnapshot", "grant_snapshot",
		"inputDigest", "input_digest", "contextDigest", "context_digest",
		"authorization_source", "authorizationSource", "identity_source", "identitySource",
	}
	assertNoBinding := func(label, body string) {
		t.Helper()
		for _, key := range leaked {
			if strings.Contains(body, key) {
				t.Fatalf("%s published the internal binding %q: %s", label, key, body)
			}
		}
	}
	// 1. The owner reads the job of the unified entry.
	status, body := read(owner(account.ctx), "/api/v1/investigations/"+turn.JobID.String())
	if status != 200 {
		t.Fatalf("owner job read = %d %s", status, body)
	}
	assertNoBinding("the job read", body)
	for _, want := range []string{"\"conversationId\":\"" + conversationID.String() + "\"", "\"turnId\":\"" + turn.TurnID.String() + "\"", "\"schemaVersion\":\"investigation-job/v3\""} {
		if !strings.Contains(body, want) {
			t.Fatalf("the job read misses %s: %s", want, body)
		}
	}
	// 2. The owner reads the public steps of the same job.
	status, body = read(owner(account.ctx), "/api/v1/investigations/"+turn.JobID.String()+"/steps")
	if status != 200 {
		t.Fatalf("owner steps read = %d %s", status, body)
	}
	assertNoBinding("the steps read", body)
	// 3. The incident of the same subject carries a legacy job and the
	// unified entry job. The history is read through the same public
	// projection and the pagination is exact.
	// The effective scope is proved with the native session of the owner. A
	// context without the native session resolves the legacy bindings only and
	// can never describe the authority of a native operator.
	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(account.ctx, f.tenantID.String(), account.subject, f.clusterUID)
	if err != nil {
		t.Fatalf("effective scope: %v", err)
	}
	defs, err := tools.Catalog()
	if err != nil {
		t.Fatalf("tool catalog: %v", err)
	}
	repository := investigation.Repository{Pool: f.apiPool}
	legacy := make([]uuid.UUID, 0, 24)
	for i := 0; i < 24; i++ {
		// The legacy shaped job is admitted with the very same native session
		// and the very same runtime role as the unified entry job. A legacy
		// binding is never used to make the history readable. Every job of the
		// incident is a distinct admission, so the history really carries 25
		// rows instead of one deduplicated row.
		job, err := repository.CreateJob(account.ctx, investigation.CreateInvestigationRequest{
			TenantID: f.tenantID, IncidentID: seed.IncidentID, Subject: account.subject,
			TriggerRevision: seed.Revision, TriggerKind: "manual", PolicyVersion: f.policy,
			Scope: scope, ToolCatalogDigest: tools.Digest(defs), Budget: investigation.DefaultBudget(),
			Focus: fmt.Sprintf("历史调查 %02d", i),
		})
		if err != nil {
			t.Fatalf("create the legacy job %d: %v", i, err)
		}
		legacy = append(legacy, job.JobID)
	}
	expected := map[string]bool{}
	for _, id := range legacy {
		if expected[id.String()] {
			t.Fatalf("the legacy job %s was admitted twice", id)
		}
		expected[id.String()] = true
	}
	expected[turn.JobID.String()] = true
	historyPath := "/api/v1/incidents/" + seed.IncidentID.String() + "/investigations"
	status, body = read(owner(account.ctx), historyPath+"?limit=20")
	if status != 200 {
		t.Fatalf("owner history read = %d %s", status, body)
	}
	assertNoBinding("the history read", body)
	first, next := decodeHistory(t, body)
	if len(first) != 20 {
		t.Fatalf("the first history page has %d rows, want 20", len(first))
	}
	if next == "" {
		t.Fatal("the first history page carries no next cursor")
	}
	seen := map[string]bool{}
	for _, row := range first {
		seen[row] = true
	}
	status, body = read(owner(account.ctx), historyPath+"?limit=20&cursor="+next)
	if status != 200 {
		t.Fatalf("owner history second page = %d %s", status, body)
	}
	assertNoBinding("the second history page", body)
	second, _ := decodeHistory(t, body)
	if len(second) != 5 {
		t.Fatalf("the second history page has %d rows, want 5", len(second))
	}
	for _, row := range second {
		if seen[row] {
			t.Fatalf("the job %s is on both history pages", row)
		}
		seen[row] = true
	}
	if len(seen) != len(expected) {
		t.Fatalf("the history shows %d jobs, want %d", len(seen), len(expected))
	}
	for id := range expected {
		if !seen[id] {
			t.Fatalf("the history misses the job %s", id)
		}
	}
	// The v3 row of the history carries the exact link of the session and the
	// turn that produced it. The link is never the link of another session of
	// the same owner.
	rows := decodeHistoryRows(t, body)
	linked := 0
	for _, row := range rows {
		if row.ConversationID == "" && row.TurnID == "" {
			continue
		}
		linked++
		if row.JobID != turn.JobID.String() {
			t.Fatalf("the v3 row links the job %s", row.JobID)
		}
		if row.ConversationID != conversationID.String() {
			t.Fatalf("the v3 row links the conversation %s, want %s", row.ConversationID, conversationID)
		}
		if row.TurnID != turn.TurnID.String() {
			t.Fatalf("the v3 row links the turn %s, want %s", row.TurnID, turn.TurnID)
		}
		if row.ConversationID == otherConversationID.String() {
			t.Fatal("the v3 row links another session of the same owner")
		}
	}
	if linked != 1 {
		t.Fatalf("the history carries %d linked v3 rows, want 1", linked)
	}
	// 4. A foreign subject of the same tenant and a subject of another
	// tenant are refused, and the refusal changes nothing.
	var state, consumed string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state,budget_consumed::text FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, turn.JobID).Scan(&state, &consumed); err != nil {
		t.Fatalf("read the job state: %v", err)
	}
	var audits int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1`, f.tenantID).Scan(&audits); err != nil {
		t.Fatalf("read the audit count: %v", err)
	}
	foreign := f.newAccount(t, `"apps"`)
	foreignCtx := auth.WithRequestContext(foreign.ctx, auth.RequestContext{
		TenantID: f.tenantID, Subject: foreign.subject, Roles: []auth.Role{auth.Operator},
		RequestID: "p405-native-foreign", TokenExpiresAt: time.Now().Add(time.Hour),
	})
	status, body = read(foreignCtx, "/api/v1/investigations/"+turn.JobID.String())
	if status == 200 {
		t.Fatalf("a foreign operator read the job of another subject: %s", body)
	}
	if strings.Contains(body, turn.JobID.String()) {
		t.Fatalf("the refused read published the job: %s", body)
	}
	crossCtx := auth.WithRequestContext(account.ctx, auth.RequestContext{
		TenantID: uuid.Must(uuid.NewV7()), Subject: account.subject, Roles: []auth.Role{auth.Operator},
		RequestID: "p405-native-cross", TokenExpiresAt: time.Now().Add(time.Hour),
	})
	status, body = read(crossCtx, "/api/v1/investigations/"+turn.JobID.String())
	if status == 200 {
		t.Fatalf("a subject of another tenant read the job: %s", body)
	}
	if strings.Contains(body, turn.JobID.String()) {
		t.Fatalf("the refused cross tenant read published the job: %s", body)
	}
	// The refusal changed no state, no ledger and no audit record.
	var stateAfter, consumedAfter string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state,budget_consumed::text FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, turn.JobID).Scan(&stateAfter, &consumedAfter); err != nil {
		t.Fatalf("read the job state after the refusals: %v", err)
	}
	if stateAfter != state || consumedAfter != consumed {
		t.Fatalf("a refused read changed the job: %s/%s -> %s/%s", state, consumed, stateAfter, consumedAfter)
	}
	var auditsAfter int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1`, f.tenantID).Scan(&auditsAfter); err != nil {
		t.Fatalf("read the audit count after the refusals: %v", err)
	}
	if auditsAfter != audits {
		t.Fatalf("a refused read wrote %d audit records", auditsAfter-audits)
	}
	// 5. The cancellation owner boundary. Every request below carries a legal
	// JSON body and exactly one stable idempotency key, so each of them really
	// reaches the transition instead of being refused by the handler.
	//
	// 5a. A request without the legal body or without the idempotency key is
	// refused before the transition. This case proves nothing about the owner
	// boundary and is asserted as its own case.
	status, body = call(owner(account.ctx), http.MethodPost, "/api/v1/investigations/"+turn.JobID.String()+":cancel", "", nil)
	if status != 400 {
		t.Fatalf("a cancel without a body = %d %s, want 400", status, body)
	}
	status, body = call(owner(account.ctx), http.MethodPost, "/api/v1/investigations/"+turn.JobID.String()+":cancel", "{}", nil)
	if status != 400 {
		t.Fatalf("a cancel without an idempotency key = %d %s, want 400", status, body)
	}
	// 5b. A foreign subject of the same tenant is refused with exactly 403 and
	// changes nothing.
	foreignKey := "p405-foreign-cancel"
	status, body = cancel(foreignCtx, turn.JobID, foreignKey)
	if status != 403 {
		t.Fatalf("a foreign operator stopped the job of another subject: %d %s", status, body)
	}
	if strings.Contains(body, turn.JobID.String()) {
		t.Fatalf("the refused stop published the job: %s", body)
	}
	// 5c. A job that does not exist is refused with exactly 404.
	status, body = cancel(owner(account.ctx), uuid.Must(uuid.NewV7()), "p405-missing-cancel")
	if status != 404 {
		t.Fatalf("a cancel of an unknown job = %d %s, want 404", status, body)
	}
	// 5d. The refused stop changed no state, no ledger and no audit record.
	var stateAfterStop, reservedAfterStop string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state,budget_reserved::text FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, turn.JobID).Scan(&stateAfterStop, &reservedAfterStop); err != nil {
		t.Fatalf("read the job state after the refused stop: %v", err)
	}
	if stateAfterStop != state {
		t.Fatalf("a refused stop changed the job state: %s -> %s", state, stateAfterStop)
	}
	var auditsAfterStop int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1`, f.tenantID).Scan(&auditsAfterStop); err != nil {
		t.Fatalf("read the audit count after the refused stop: %v", err)
	}
	if auditsAfterStop != audits {
		t.Fatalf("a refused stop wrote %d audit records", auditsAfterStop-audits)
	}
	var foreignKeys int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM investigation.request_keys WHERE tenant_id=$1 AND subject=$2 AND idempotency_key=$3`, f.tenantID, foreign.subject, foreignKey).Scan(&foreignKeys); err != nil {
		t.Fatalf("read the idempotency ledger after the refused stop: %v", err)
	}
	if foreignKeys != 0 {
		t.Fatalf("a refused stop wrote %d idempotency ledger rows", foreignKeys)
	}
	// 5e. The owner stops the job of the own session. The state, the ledger
	// and the retained facts follow the cancellation convention.
	ownerKey := "p405-owner-cancel"
	status, body = cancel(owner(account.ctx), turn.JobID, ownerKey)
	if status != 200 {
		t.Fatalf("the owner could not stop the own job: %d %s", status, body)
	}
	if !strings.Contains(body, "\"state\":\"cancelled\"") {
		t.Fatalf("the accepted stop does not report the cancelled state: %s", body)
	}
	var cancelledState, cancelledCode, cancelledResult string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state,error_code,COALESCE(result::text,'') FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, turn.JobID).Scan(&cancelledState, &cancelledCode, &cancelledResult); err != nil {
		t.Fatalf("read the cancelled job: %v", err)
	}
	if cancelledState != "cancelled" || cancelledCode != "CANCELLED" {
		t.Fatalf("the cancelled job is %s/%s, want cancelled/CANCELLED", cancelledState, cancelledCode)
	}
	var answer struct {
		SchemaVersion string   `json:"schemaVersion"`
		Partial       bool     `json:"partial"`
		Warnings      []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(cancelledResult), &answer); err != nil {
		t.Fatalf("decode the retained answer of the cancelled job: %v (%s)", err, cancelledResult)
	}
	if answer.SchemaVersion != "assistant-answer/v1" || !answer.Partial {
		t.Fatalf("the cancelled job keeps no partial answer: %s", cancelledResult)
	}
	if len(answer.Warnings) != 1 || answer.Warnings[0] != "CANCELLED" {
		t.Fatalf("the retained answer of the cancelled job carries no cancellation warning: %s", cancelledResult)
	}
	var cancelEvents int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM investigation.events WHERE tenant_id=$1 AND job_id=$2 AND event_type='cancelled'`, f.tenantID, turn.JobID).Scan(&cancelEvents); err != nil {
		t.Fatalf("read the event ledger of the cancelled job: %v", err)
	}
	if cancelEvents != 1 {
		t.Fatalf("the cancelled job wrote %d cancellation events, want 1", cancelEvents)
	}
	var ownerKeys int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM investigation.request_keys WHERE tenant_id=$1 AND subject=$2 AND idempotency_key=$3 AND job_id=$4`, f.tenantID, account.subject, ownerKey, turn.JobID).Scan(&ownerKeys); err != nil {
		t.Fatalf("read the idempotency ledger of the accepted stop: %v", err)
	}
	if ownerKeys != 1 {
		t.Fatalf("the accepted stop wrote %d idempotency ledger rows, want 1", ownerKeys)
	}
	// The same key never cancels a second job.
	status, body = cancel(owner(account.ctx), legacy[0], ownerKey)
	if status != 409 {
		t.Fatalf("a reused idempotency key on another job = %d %s, want 409", status, body)
	}
	// 6. A withdrawn, a disabled and a re-versioned authority may read nothing
	// and stop nothing, and the refusal writes no state, no ledger and no
	// audit record.
	revokeIncident := f.newIncidentFamily(t, "pod", "CrashLoopBackOff", "api-1")
	cases := []struct {
		name  string
		apply func(t *testing.T, account nativeJobAccount)
	}{
		{"a withdrawn grant", func(t *testing.T, account nativeJobAccount) {
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='fixture withdrawal' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
				t.Fatalf("revoke the grant: %v", err)
			}
		}},
		{"a disabled account", func(t *testing.T, account nativeJobAccount) {
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET status='disabled' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
				t.Fatalf("disable the account: %v", err)
			}
		}},
		{"a stale authorization version", func(t *testing.T, account nativeJobAccount) {
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
				t.Fatalf("re-version the account: %v", err)
			}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			revoked := f.newAccount(t, `"apps"`)
			revokedScope, e := (graph.Authorization{Pool: f.apiPool}).Effective(revoked.ctx, f.tenantID.String(), revoked.subject, f.clusterUID)
			if e != nil {
				t.Fatalf("effective scope of the revoked account: %v", e)
			}
			job, e := repository.CreateJob(revoked.ctx, investigation.CreateInvestigationRequest{
				TenantID: f.tenantID, IncidentID: revokeIncident.IncidentID, Subject: revoked.subject,
				TriggerRevision: revokeIncident.Revision, TriggerKind: "manual", PolicyVersion: f.policy,
				Scope: revokedScope, ToolCatalogDigest: tools.Digest(defs), Budget: investigation.DefaultBudget(),
			})
			if e != nil {
				t.Fatalf("create the job of the revoked account: %v", e)
			}
			before := jobLedger(t, f, job.JobID)
			testCase.apply(t, revoked)
			revokedCtx := auth.WithRequestContext(revoked.ctx, auth.RequestContext{
				TenantID: f.tenantID, Subject: revoked.subject, Roles: []auth.Role{auth.Operator},
				RequestID: "p405-native-revoked", TokenExpiresAt: time.Now().Add(time.Hour),
			})
			status, body = read(revokedCtx, "/api/v1/investigations/"+job.JobID.String())
			if status != 403 {
				t.Fatalf("%s read the job: %d %s, want 403", testCase.name, status, body)
			}
			status, body = read(revokedCtx, "/api/v1/investigations/"+job.JobID.String()+"/steps")
			if status != 403 {
				t.Fatalf("%s read the steps: %d %s, want 403", testCase.name, status, body)
			}
			status, body = read(revokedCtx, "/api/v1/incidents/"+revokeIncident.IncidentID.String()+"/investigations")
			if status != 403 {
				t.Fatalf("%s read the history: %d %s, want 403", testCase.name, status, body)
			}
			status, body = cancel(revokedCtx, job.JobID, "p405-revoked-"+job.JobID.String())
			if status != 403 {
				t.Fatalf("%s stopped the job: %d %s, want 403", testCase.name, status, body)
			}
			after := jobLedger(t, f, job.JobID)
			if before != after {
				t.Fatalf("%s changed the job ledger: %s -> %s", testCase.name, before, after)
			}
		})
	}
}

// jobLedger is the whole observable ledger of a job: the state, the budget
// ledger, the event ledger, the idempotency ledger and the audit count.
func jobLedger(t *testing.T, f *nativeJobFixture, jobID uuid.UUID) string {
	t.Helper()
	var state, consumed, reserved, result string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state,budget_consumed::text,budget_reserved::text,COALESCE(result::text,'') FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, jobID).Scan(&state, &consumed, &reserved, &result); err != nil {
		t.Fatalf("read the job ledger: %v", err)
	}
	var events, keys, audits int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM investigation.events WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, jobID).Scan(&events); err != nil {
		t.Fatalf("read the event ledger: %v", err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM investigation.request_keys WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, jobID).Scan(&keys); err != nil {
		t.Fatalf("read the idempotency ledger: %v", err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND entity_id=$2`, f.tenantID, jobID).Scan(&audits); err != nil {
		t.Fatalf("read the audit ledger: %v", err)
	}
	return strings.Join([]string{state, consumed, reserved, result, fmt.Sprint(events), fmt.Sprint(keys), fmt.Sprint(audits)}, "|")
}

// decodeHistory returns the job ids of a history page and the next cursor.
func decodeHistory(t *testing.T, body string) ([]string, string) {
	t.Helper()
	rows := decodeHistoryRows(t, body)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.JobID)
	}
	return ids, decodeHistoryCursor(t, body)
}

// historyRow is one row of the public history projection.
type historyRow struct {
	JobID          string `json:"jobId"`
	ConversationID string `json:"conversationId"`
	TurnID         string `json:"turnId"`
	SchemaVersion  string `json:"schemaVersion"`
}

func decodeHistoryRows(t *testing.T, body string) []historyRow {
	t.Helper()
	var envelope struct {
		Data []historyRow `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode the history response: %v (%s)", err, body)
	}
	return envelope.Data
}

func decodeHistoryCursor(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Meta struct {
			NextCursor string `json:"nextCursor"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode the history response: %v (%s)", err, body)
	}
	return envelope.Meta.NextCursor
}
