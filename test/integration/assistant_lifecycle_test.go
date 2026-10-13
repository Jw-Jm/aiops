package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/assistant"
	"ops-platform/internal/investigation"
	"strings"
	"testing"
	"time"
)

func TestAssistantConversationLifecycle(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	policyVersion := sp06Policy(t, ctx, db, pool, b.TenantID)
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, pool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	jobs := investigation.Repository{Pool: pool, AssistantEnabled: true}
	repo := assistant.Repository{Pool: apiPool, Jobs: jobs, Admit: func(context.Context, assistant.Conversation, string) (assistant.Admission, error) {
		return assistant.Admission{PolicyVersion: policyVersion, ModelProfileVersion: "test-profile/v1", Budget: investigation.DefaultBudget()}, nil
	}}
	createInput := assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "sp06-admin", Title: "Audit query", Mode: "data_query", AuthorizationDomain: "audit", Binding: assistant.ConversationBinding{Timezone: "Asia/Shanghai"}}
	creationKey := assistant.CreationKey{Key: "create-audit-query", RequestDigest: "sha256:" + strings.Repeat("2", 64)}
	c, err := repo.Create(ctx, createInput, creationKey)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := repo.Create(ctx, createInput, creationKey)
	if err != nil || repeated.ConversationID != c.ConversationID {
		t.Fatal("conversation creation retry changed identity", err)
	}
	creationKey.RequestDigest = "sha256:" + strings.Repeat("3", 64)
	if _, err = repo.Create(ctx, createInput, creationKey); !errors.Is(err, investigation.ErrConflict) {
		t.Fatal("creation key reused with a changed request", err)
	}
	if _, err = repo.Get(ctx, b.TenantID, c.ConversationID, "other-admin"); err == nil {
		t.Fatal("owner isolation failed")
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO investigation.assistant_worker_capabilities(tenant_id,worker_id,model_profile_version,catalog_version,expires_at) VALUES($1,'test-worker','test-profile/v1','assistant-tools/v1',clock_timestamp()+interval '1 minute')`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	q := assistant.SubmitMessage{Message: "统计本人最近一小时的操作", ExpectedConversationRevision: 1}
	turn, err := repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "test-key", q)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "test-key", q)
	if err != nil || retry.TurnID != turn.TurnID || retry.JobID != turn.JobID {
		t.Fatalf("idempotent retry: %+v %v", retry, err)
	}
	q.ExpectedConversationRevision = 2
	if _, err = repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "another-key", q); !errors.Is(err, assistant.ErrBusy) {
		t.Fatalf("active turn accepted: %v", err)
	}
	old := investigation.Repository{Pool: pool}
	if _, err = old.Claim(ctx, b.TenantID, "old-worker", 30*time.Second); !errors.Is(err, investigation.ErrNoJob) {
		t.Fatalf("old worker claimed v3: %v", err)
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "test-worker", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if lease.JobID != turn.JobID {
		t.Fatal("wrong turn claimed")
	}
	inputRaw, err := jobs.GetAssistantInput(ctx, lease)
	var input assistant.TurnInput
	if err != nil || json.Unmarshal(inputRaw, &input) != nil || input.RequestedAt.IsZero() || input.RequestedAt.Before(turn.CreatedAt.Add(-time.Second)) || input.RequestedAt.After(turn.CreatedAt.Add(time.Second)) || input.Binding.Timezone != "Asia/Shanghai" {
		t.Fatalf("missing frozen platform time anchor: %v", err)
	}
	current, err := repo.Turn(ctx, b.TenantID, turn.TurnID, "sp06-admin")
	if err != nil || current.State != "running" {
		t.Fatalf("job trigger did not update turn: %+v %v", current, err)
	}
	if err = repo.Cancel(ctx, b.TenantID, turn.TurnID, "sp06-admin", "cancel-key"); err != nil {
		t.Fatal(err)
	}
	current, err = repo.Turn(ctx, b.TenantID, turn.TurnID, "sp06-admin")
	if err != nil || current.State != "cancelled" {
		t.Fatalf("cancelled turn: %+v %v", current, err)
	}
	lateAnswer, err := json.Marshal(investigation.AssistantAnswer{SchemaVersion: "assistant-answer/v1", AnswerKind: "unsupported", Summary: "No committed observations", FactRefs: []investigation.FactRef{}, QueryResultRefs: []uuid.UUID{}, EvidenceRefs: []string{}, EntityRefs: []string{}, MissingEvidence: []string{}, ScopeSummary: "audit", AsOf: time.Now().UTC(), DegradedSources: []string{}, Warnings: []string{}, Hypotheses: []investigation.Hypothesis{}, NextChecks: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = jobs.Complete(ctx, lease, lateAnswer); !errors.Is(err, investigation.ErrLease) {
		t.Fatalf("cancelled worker published a late answer: %v", err)
	}
	partial, err := repo.Submit(ctx, b.TenantID, c.ConversationID, "sp06-admin", "partial-key", q)
	if err != nil {
		t.Fatal(err)
	}
	lease, err = jobs.Claim(ctx, b.TenantID, "test-worker", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_assistant_context", ArgsDigest: investigation.ArgumentsDigest([]byte(`{}`)), Arguments: json.RawMessage(`{}`), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536}}
	if _, err = jobs.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.worker_queue SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND job_id=$2`, b.TenantID, partial.JobID); err != nil {
		t.Fatal(err)
	}
	oldLease := lease
	lease, err = jobs.Claim(ctx, b.TenantID, "replacement-worker", 30*time.Second)
	if err != nil || lease.JobID != partial.JobID || lease.Generation <= oldLease.Generation {
		t.Fatalf("v3 worker takeover: %+v %v", lease, err)
	}
	if err = jobs.CompleteStep(ctx, oldLease, call.StepID, []byte(`{}`), investigation.Usage{ToolCalls: 1}); !errors.Is(err, investigation.ErrLease) {
		t.Fatalf("old worker committed after takeover: %v", err)
	}
	recovered, err := jobs.Get(ctx, b.TenantID, partial.JobID)
	if err != nil || recovered.Reserved != (investigation.Usage{}) || recovered.Consumed.ToolCalls != 1 || recovered.Consumed.ResultBytes < call.Reserve.ResultBytes {
		t.Fatalf("unknown outcome was not conservatively charged: %+v %v", recovered.Consumed, err)
	}
	if err = jobs.ExhaustBudget(ctx, lease); err != nil {
		t.Fatal(err)
	}
	current, err = repo.Turn(ctx, b.TenantID, partial.TurnID, "sp06-admin")
	if err != nil || current.State != "partial" || len(current.Answer) == 0 {
		t.Fatalf("budget partial answer: %+v %v", current, err)
	}
	page, err := repo.MessagePage(ctx, b.TenantID, c.ConversationID, "sp06-admin", 0, 2)
	if err != nil || len(page) != 2 {
		t.Fatalf("latest message page: %+v %v", page, err)
	}
	earlier, err := repo.MessagePage(ctx, b.TenantID, c.ConversationID, "sp06-admin", page[0].Sequence, 2)
	if err != nil || len(earlier) == 0 || earlier[len(earlier)-1].Sequence >= page[0].Sequence {
		t.Fatalf("message pagination overlapped: %+v %v", earlier, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_messages SET content='{}'::jsonb WHERE tenant_id=$1 AND turn_id=$2 AND role='assistant'`, b.TenantID, partial.TurnID); err == nil {
		t.Fatal("immutable assistant answer was rewritten")
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_messages SET retain_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND turn_id=$2 AND role='assistant'`, b.TenantID, partial.TurnID); err != nil {
		t.Fatal(err)
	}
	current, err = repo.Turn(ctx, b.TenantID, partial.TurnID, "sp06-admin")
	if err != nil || len(current.Answer) != 0 || current.ErrorCode != "RESULT_EXPIRED" {
		t.Fatalf("expired answer disclosed: %+v %v", current, err)
	}
	conversation, err := repo.Get(ctx, b.TenantID, c.ConversationID, "sp06-admin")
	if err != nil || conversation.ActiveTurn == nil || len(conversation.ActiveTurn.Answer) != 0 {
		t.Fatalf("expired conversation answer disclosed: %+v %v", conversation, err)
	}
	_, events, err := repo.Events(ctx, b.TenantID, c.ConversationID, "sp06-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	expiredEvent := false
	for _, event := range events {
		if event.TurnID == partial.TurnID && event.Type == "answer.expired" {
			expiredEvent = true
			if strings.Contains(string(event.Payload), "summary") {
				t.Fatal("expired event retained answer content")
			}
		}
	}
	if !expiredEvent {
		t.Fatal("expired terminal event was not replaced")
	}
	if _, err = jobs.ReadSteps(ctx, b.TenantID, partial.JobID, "sp06-admin"); !errors.Is(err, investigation.ErrResultExpired) {
		t.Fatalf("expired tool ledger readable: %v", err)
	}
	if _, _, err = jobs.Events(ctx, b.TenantID, partial.JobID, "sp06-admin", 0); !errors.Is(err, investigation.ErrResultExpired) {
		t.Fatalf("expired Job event answer readable: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_messages SET legal_hold=true WHERE tenant_id=$1 AND turn_id=$2 AND role='assistant'`, b.TenantID, partial.TurnID); err != nil {
		t.Fatal(err)
	}
	current, err = repo.Turn(ctx, b.TenantID, partial.TurnID, "sp06-admin")
	if err != nil || len(current.Answer) == 0 {
		t.Fatalf("legal hold lost retained answer: %+v %v", current, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='disabled',revision=revision+1 WHERE tenant_id=$1 AND subject='sp06-admin'`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Messages(ctx, b.TenantID, c.ConversationID, "sp06-admin"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked history readable: %v", err)
	}
	var incidentID *uuid.UUID
	if err = db.QueryRowContext(ctx, `SELECT incident_id FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, b.TenantID, turn.JobID).Scan(&incidentID); err != nil || incidentID != nil {
		t.Fatalf("invented Incident: %v %v", incidentID, err)
	}
}
