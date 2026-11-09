package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
)

// TestAssistantFocusAndBudgetTighteningInPostgreSQL exercises the real
// assistant repository against a real PostgreSQL instance: the focus and the
// requested narrowing are frozen into the turn input and the job, the applied
// budget is the one the Worker is bound to, the same key with the same payload
// does not create a second job, and the same key with a changed payload is a
// conflict.
func TestAssistantFocusAndBudgetTighteningInPostgreSQL(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	policyVersion := sp06Policy(t, ctx, db, pool, b.TenantID)
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, pool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	admitted := investigation.DefaultBudget()
	admitted.ToolCalls = 6
	admitted.ModelRequests = 3
	admitted.DurationSeconds = 300
	jobs := investigation.Repository{Pool: pool, AssistantEnabled: true}
	repo := assistant.Repository{Pool: apiPool, Jobs: jobs, Admit: func(context.Context, assistant.Conversation, string) (assistant.Admission, error) {
		return assistant.Admission{PolicyVersion: policyVersion, ModelProfileVersion: "test-profile/v1", Budget: admitted}, nil
	}}
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "sp06-admin", Title: "Focus budget", Mode: "data_query", AuthorizationDomain: "audit", Binding: assistant.ConversationBinding{Timezone: "Asia/Shanghai"}}, assistant.CreationKey{Key: "create-focus-budget", RequestDigest: "sha256:" + repeatHex("4")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO investigation.assistant_worker_capabilities(tenant_id,worker_id,model_profile_version,catalog_version,expires_at) VALUES($1,'test-worker','test-profile/v1','assistant-tools/v2',clock_timestamp()+interval '10 minute')`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	tightening := investigation.BudgetTightening{ToolCalls: 2, ModelRequests: 1, DurationSeconds: 120}
	q := assistant.SubmitMessage{Message: "统计最近一小时的操作", ExpectedConversationRevision: 1, Focus: "只看失败操作", BudgetTightening: &tightening}
	turn, err := repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "focus-budget-key", q)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Budget == nil {
		t.Fatal("the applied budget was not echoed")
	}
	if turn.Budget.ToolCalls != 2 || turn.Budget.ModelRequests != 1 || turn.Budget.DurationSeconds != 120 {
		t.Fatalf("the applied budget is not the tightened one: %+v", turn.Budget)
	}
	if turn.Focus != "只看失败操作" {
		t.Fatalf("the focus was not echoed: %q", turn.Focus)
	}
	// The job the Worker claims carries the tightened budget and the focus.
	job, err := jobs.Get(ctx, b.TenantID, turn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Budget.ToolCalls != 2 || job.Budget.ModelRequests != 1 || job.Budget.DurationSeconds != 120 {
		t.Fatalf("the job budget is not the tightened one: %+v", job.Budget)
	}
	if job.Focus != "只看失败操作" {
		t.Fatalf("the job focus was not frozen: %q", job.Focus)
	}
	if job.AdmissionDigest == "" {
		t.Fatal("the admission digest was not frozen")
	}
	// The frozen input carries the focus and the effective budget.
	lease, err := jobs.Claim(ctx, b.TenantID, "test-worker", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jobs.GetAssistantInput(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	var input assistant.TurnInput
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	if input.Focus != "只看失败操作" || input.EffectiveBudget == nil || input.EffectiveBudget.ToolCalls != 2 {
		t.Fatalf("the frozen input does not carry the focus and the effective budget: %+v", input)
	}
	// The same key with the same payload replays the same turn and job.
	replay, err := repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "focus-budget-key", q)
	if err != nil {
		t.Fatal(err)
	}
	if replay.TurnID != turn.TurnID || replay.JobID != turn.JobID {
		t.Fatalf("the replay created another job: %+v", replay)
	}
	// The same key with a changed payload is a conflict.
	changed := q
	changed.Focus = "只看成功操作"
	if _, err = repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "focus-budget-key", changed); !errors.Is(err, investigation.ErrConflict) {
		t.Fatalf("a changed payload reused the idempotency key: %v", err)
	}
	// The Worker is bound by the tightened tool budget: the third tool call
	// is refused and the job keeps a partial outcome instead of a root cause.
	if _, err = jobs.AllocateTool(ctx, lease, "get_assistant_context", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.AllocateTool(ctx, lease, "execute_analysis_query", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.AllocateTool(ctx, lease, "query_logs", json.RawMessage(`{}`)); !errors.Is(err, investigation.ErrBudget) {
		t.Fatalf("the worker exceeded the tightened tool budget: %v", err)
	}
	// The refused allocation exhausts the budget on its own: the job keeps a
	// partial outcome and never a confirmed root cause.
	current, err := repo.Turn(ctx, b.TenantID, turn.TurnID, "sp06-admin")
	if err != nil {
		t.Fatal(err)
	}
	if current.State != "partial" {
		t.Fatalf("the exhausted job is not partial: %+v", current)
	}
	// The reloaded turn still reports the applied budget and the focus.
	if current.Budget == nil || current.Budget.ToolCalls != 2 || current.Focus != "只看失败操作" {
		t.Fatalf("the reloaded turn lost the applied budget or the focus: %+v", current)
	}
	// A client that asks for more than the admission is clamped, never widened.
	wide := q
	wide.BudgetTightening = &investigation.BudgetTightening{ToolCalls: 20}
	wide.ExpectedConversationRevision = 2
	clamped, err := repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "focus-budget-wide", wide)
	if err != nil {
		t.Fatal(err)
	}
	if clamped.Budget == nil || clamped.Budget.ToolCalls != admitted.ToolCalls {
		t.Fatalf("a client widened the admission: %+v", clamped.Budget)
	}
	// A request above the platform maximum is refused, not silently defaulted.
	over := q
	over.BudgetTightening = &investigation.BudgetTightening{ToolCalls: admitted.ToolCalls + 100000}
	over.ExpectedConversationRevision = 3
	if _, err = repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "focus-budget-over", over); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("a request above the platform maximum was accepted: %v", err)
	}
	// An invalid request is refused instead of silently defaulted.
	invalid := q
	invalid.BudgetTightening = &investigation.BudgetTightening{ToolCalls: -1}
	invalid.ExpectedConversationRevision = 3
	if _, err = repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "focus-budget-invalid", invalid); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("an invalid tightening was accepted: %v", err)
	}
}

// TestAssistantHTTPRejectsInvalidBudgetTypesInPostgreSQL exercises the real
// HTTP decode of the message and of the fault chain investigation route
// against a real PostgreSQL instance: an illegal budget type is refused at
// the request boundary and no job, idempotency ledger, audit record or
// conversation revision is written.
func TestAssistantHTTPRejectsInvalidBudgetTypesInPostgreSQL(t *testing.T) {
	ctx, db, _, b, repo, _ := assistantExtendedFixture(t)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "HTTP 边界", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := &httpapi.AssistantHandlers{Repository: repo, CursorKey: key}
	counts := func() (int, int, int, int, int64) {
		var jobCount, keyCount, turnCount, auditCount int
		var revision int64
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.jobs WHERE tenant_id=$1`, b.TenantID).Scan(&jobCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.assistant_request_keys WHERE tenant_id=$1`, b.TenantID).Scan(&keyCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.assistant_turns WHERE tenant_id=$1`, b.TenantID).Scan(&turnCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1`, b.TenantID).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT revision FROM investigation.assistant_conversations WHERE tenant_id=$1 AND conversation_id=$2`, b.TenantID, c.ConversationID).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return jobCount, keyCount, turnCount, auditCount, revision
	}
	beforeJobs, beforeKeys, beforeTurns, beforeAudit, beforeRevision := counts()
	bodies := map[string]string{
		"null dimension":  `{"message":"统计过去一小时","expectedConversationRevision":1,"budgetTightening":{"toolCalls":null}}`,
		"null object":     `{"message":"统计过去一小时","expectedConversationRevision":1,"budgetTightening":null}`,
		"fraction":        `{"message":"统计过去一小时","expectedConversationRevision":1,"budgetTightening":{"toolCalls":1.5}}`,
		"string":          `{"message":"统计过去一小时","expectedConversationRevision":1,"budgetTightening":{"toolCalls":"2"}}`,
		"unknown request": `{"message":"统计过去一小时","expectedConversationRevision":1,"unknown":1}`,
	}
	for _, route := range []string{"messages", "fault-chain-investigations"} {
		for name, body := range bodies {
			r := httptest.NewRequest("POST", "/api/v1/assistant/conversations/"+c.ConversationID.String()+"/"+route, strings.NewReader(body)).WithContext(auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: b.TenantID, Subject: "assistant-owner", Roles: []auth.Role{auth.Operator}}))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "http-"+route+"-"+strings.ReplaceAll(name, " ", "-"))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatalf("%s on %s = %d %s", name, route, w.Code, w.Body.String())
			}
		}
	}
	afterJobs, afterKeys, afterTurns, afterAudit, afterRevision := counts()
	if afterJobs != beforeJobs || afterKeys != beforeKeys || afterTurns != beforeTurns {
		t.Fatalf("a refused request wrote rows: jobs %d->%d keys %d->%d turns %d->%d", beforeJobs, afterJobs, beforeKeys, afterKeys, beforeTurns, afterTurns)
	}
	if afterAudit != beforeAudit {
		t.Fatalf("a refused request wrote %d audit records", afterAudit-beforeAudit)
	}
	if afterRevision != beforeRevision {
		t.Fatalf("a refused request moved the conversation revision: %d->%d", beforeRevision, afterRevision)
	}
	// A valid request on the same route is accepted afterwards: the refusals
	// left no state behind.
	r := httptest.NewRequest("POST", "/api/v1/assistant/conversations/"+c.ConversationID.String()+"/messages", strings.NewReader(`{"message":"统计过去一小时","expectedConversationRevision":1,"budgetTightening":{"toolCalls":2}}`)).WithContext(auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: b.TenantID, Subject: "assistant-owner", Roles: []auth.Role{auth.Operator}}))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "http-valid-after-refusal")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("the valid request after the refusals = %d %s", w.Code, w.Body.String())
	}
}

func repeatHex(digit string) string {
	out := ""
	for i := 0; i < 64; i++ {
		out += digit
	}
	return out
}

// TestAssistantInvalidFocusAndBudgetWriteNothingInPostgreSQL exercises the
// real HTTP decode against the real repository and a real PostgreSQL instance:
// an illegal focus or an illegal budget type is refused before any job,
// idempotency ledger, audit record or conversation revision is written.
func TestAssistantInvalidFocusAndBudgetWriteNothingInPostgreSQL(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	policyVersion := sp06Policy(t, ctx, db, pool, b.TenantID)
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, pool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	admitted := investigation.DefaultBudget()
	admitted.ToolCalls = 6
	admitted.ModelRequests = 3
	admitted.DurationSeconds = 300
	jobs := investigation.Repository{Pool: pool, AssistantEnabled: true}
	repo := assistant.Repository{Pool: apiPool, Jobs: jobs, Admit: func(context.Context, assistant.Conversation, string) (assistant.Admission, error) {
		return assistant.Admission{PolicyVersion: policyVersion, ModelProfileVersion: "test-profile/v1", Budget: admitted}, nil
	}}
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "sp06-admin", Title: "Invalid input", Mode: "data_query", AuthorizationDomain: "audit", Binding: assistant.ConversationBinding{Timezone: "Asia/Shanghai"}}, assistant.CreationKey{Key: "create-invalid-input", RequestDigest: "sha256:" + repeatHex("5")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO investigation.assistant_worker_capabilities(tenant_id,worker_id,model_profile_version,catalog_version,expires_at) VALUES($1,'test-worker','test-profile/v1','assistant-tools/v2',clock_timestamp()+interval '10 minute')`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	counts := func() (int, int, int, int, int) {
		var jobCount, keyCount, turnCount, auditCount int
		var revision int64
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.jobs WHERE tenant_id=$1`, b.TenantID).Scan(&jobCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.assistant_request_keys WHERE tenant_id=$1`, b.TenantID).Scan(&keyCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.assistant_turns WHERE tenant_id=$1`, b.TenantID).Scan(&turnCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1`, b.TenantID).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT revision FROM investigation.assistant_conversations WHERE tenant_id=$1 AND conversation_id=$2`, b.TenantID, c.ConversationID).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return jobCount, keyCount, turnCount, auditCount, int(revision)
	}
	beforeJobs, beforeKeys, beforeTurns, beforeAudit, beforeRevision := counts()

	// An oversized redacted focus is refused: the redaction grows the focus
	// past the persisted limit, so it must never reach the job row.
	oversizedFocus := strings.Repeat("查", 492) + "token=x"
	if _, err = repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "focus-oversized", assistant.SubmitMessage{Message: "统计最近一小时的操作", ExpectedConversationRevision: 1, Focus: oversizedFocus}); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("an oversized redacted focus was accepted: %v", err)
	}
	// An illegal budget type is refused by the decode boundary before the
	// repository sees it.
	for name, raw := range map[string]string{
		"null dimension":   `{"message":"统计最近一小时的操作","expectedConversationRevision":1,"budgetTightening":{"toolCalls":null}}`,
		"null object":      `{"message":"统计最近一小时的操作","expectedConversationRevision":1,"budgetTightening":null}`,
		"fraction":         `{"message":"统计最近一小时的操作","expectedConversationRevision":1,"budgetTightening":{"toolCalls":1.5}}`,
		"string":           `{"message":"统计最近一小时的操作","expectedConversationRevision":1,"budgetTightening":{"toolCalls":"2"}}`,
		"unknown dimens.":  `{"message":"统计最近一小时的操作","expectedConversationRevision":1,"budgetTightening":{"toolCalls":2,"unknown":1}}`,
		"unknown request":  `{"message":"统计最近一小时的操作","expectedConversationRevision":1,"unknown":1}`,
	} {
		var q assistant.SubmitMessage
		if err = json.Unmarshal([]byte(raw), &q); !errors.Is(err, investigation.ErrInvalid) {
			t.Fatalf("%s was accepted: %v", name, err)
		}
	}
	afterJobs, afterKeys, afterTurns, afterAudit, afterRevision := counts()
	if afterJobs != beforeJobs || afterKeys != beforeKeys || afterTurns != beforeTurns {
		t.Fatalf("a refused request wrote rows: jobs %d->%d keys %d->%d turns %d->%d", beforeJobs, afterJobs, beforeKeys, afterKeys, beforeTurns, afterTurns)
	}
	if afterAudit != beforeAudit {
		t.Fatalf("a refused request wrote %d audit records", afterAudit-beforeAudit)
	}
	if afterRevision != beforeRevision {
		t.Fatalf("a refused request moved the conversation revision: %d->%d", beforeRevision, afterRevision)
	}
	// The conversation still accepts a valid request afterwards: the refusal
	// left no state behind.
	tightening := investigation.BudgetTightening{ToolCalls: 2}
	turn, err := repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "focus-valid-after-refusal", assistant.SubmitMessage{Message: "统计最近一小时的操作", ExpectedConversationRevision: 1, BudgetTightening: &tightening})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Budget == nil || turn.Budget.ToolCalls != 2 {
		t.Fatalf("the valid request after a refusal was not applied: %+v", turn.Budget)
	}
}
