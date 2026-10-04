package integration

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/investigation"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSP06RecoveryNonceTransactionAndValidator(t *testing.T) {
	ctx, db, repo, req := sp06JobFixture(t)
	req.IdempotencyKey = "recovery-key"
	req.RequestDigest = "sha256:original"
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.RequestDigest = "sha256:changed"
	if _, err = repo.CreateJob(ctx, req); !errors.Is(err, investigation.ErrConflict) {
		t.Fatalf("idempotency key rebound: %v", err)
	}
	lease, err := repo.Claim(ctx, j.TenantID, "recovery-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_incident_context", ArgsDigest: investigation.ArgumentsDigest([]byte(`{}`)), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 1024}}
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for range 10 {
		wg.Go(func() { _, e := repo.BeginCall(ctx, lease, call); results <- e })
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, investigation.ErrReplay) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("concurrent nonce admitted %d", success)
	}
	// A database failure after the remote result must roll back Step, budget and audit together.
	_, err = db.ExecContext(ctx, `CREATE FUNCTION investigation.sp06_test_abort() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'SP06 owned commit fault';END $$; CREATE TRIGGER sp06_test_abort BEFORE UPDATE ON investigation.steps FOR EACH ROW WHEN(NEW.state='succeeded') EXECUTE FUNCTION investigation.sp06_test_abort()`)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteStep(ctx, lease, call.StepID, []byte(`{"evidenceRefs":[]}`), investigation.Usage{ToolCalls: 1, ResultBytes: 19}); err == nil {
		t.Fatal("commit fault was ignored")
	}
	var state string
	if db.QueryRowContext(ctx, `SELECT state FROM investigation.steps WHERE step_id=$1`, call.StepID).Scan(&state) != nil || state != "running" {
		t.Fatal("partial Step commit")
	}
	_, err = db.ExecContext(ctx, `DROP TRIGGER sp06_test_abort ON investigation.steps;DROP FUNCTION investigation.sp06_test_abort()`)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteStep(ctx, lease, call.StepID, []byte(`{"evidenceRefs":[]}`), investigation.Usage{ToolCalls: 1, ResultBytes: 19}); err != nil {
		t.Fatal(err)
	}
	// Read recovery returns the original successful Step and never reserves/calls it again.
	allocation, err := repo.AllocateTool(ctx, lease, call.Name, []byte(`{}`))
	if err != nil || !allocation.Committed || allocation.StepID != call.StepID {
		t.Fatalf("tool recovery: %+v %v", allocation, err)
	}
	if _, err = repo.RecoverStep(ctx, lease, call.StepID, allocation.ArgsDigest); err != nil {
		t.Fatal(err)
	}
	reserve := investigation.Usage{ModelRequests: 1, InputTokens: 16384, OutputTokens: 1024, ResultBytes: 65536}
	args := json.RawMessage(`{"requestDigest":"sha256:` + strings.Repeat("a", 64) + `"}`)
	model, err := repo.AllocateModel(ctx, lease, reserve, args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AllocateModel(ctx, lease, reserve, args); !errors.Is(err, investigation.ErrReplay) {
		t.Fatalf("duplicate in-flight model allocation: %v", err)
	}
	consumed := investigation.Usage{ModelRequests: 1, InputTokens: 100, OutputTokens: 20, ResultBytes: 1024}
	response := json.RawMessage(`{"provider":"openai-compatible","usageKnown":true,"response":{"choices":[{"message":{"content":"stored model response"}}]}}`)
	if err = repo.SettleModel(ctx, lease, model.StepID, response, &consumed, ""); err != nil {
		t.Fatal(err)
	}
	// Takeover invalidates every old callback but retains committed model/tool responses.
	old := lease
	_, err = db.ExecContext(ctx, `UPDATE investigation.worker_queue SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, j.JobID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err = repo.Claim(ctx, j.TenantID, "takeover-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := repo.AllocateModel(ctx, lease, reserve, args)
	if err != nil || !recovered.Committed || recovered.StepID != model.StepID || !strings.Contains(string(recovered.Result), "stored model response") {
		t.Fatalf("model recovery: %+v %v", recovered, err)
	}
	if err = repo.CompleteStep(ctx, old, call.StepID, []byte(`{"evidenceRefs":[]}`), investigation.Usage{ToolCalls: 1, ResultBytes: 19}); !errors.Is(err, investigation.ErrLease) {
		t.Fatalf("old callback %v", err)
	}
	// Sequence admission is ordered and replay guarded independently of jti.
	seq := int64(2)
	call.StepID = uuid.New()
	call.ContextID = uuid.New()
	call.JTI = ""
	call.Seq = &seq
	if _, err = repo.BeginCall(ctx, lease, call); !errors.Is(err, investigation.ErrReplay) {
		t.Fatalf("out of order %v", err)
	}
	seq = 1
	if _, err = repo.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BeginCall(ctx, lease, call); !errors.Is(err, investigation.ErrReplay) {
		t.Fatalf("sequence replay %v", err)
	}
	if err = repo.FailStep(ctx, lease, call.StepID, "SOURCE_TIMEOUT"); err != nil {
		t.Fatal(err)
	}
	forged := []byte(`{"schemaVersion":"investigation-result/v1","status":"probable","summary":"forged","evidenceRefs":["` + uuid.NewString() + `"],"candidateUpdates":[],"actionPlans":[],"partial":false,"degradedSources":[]}`)
	if err = repo.Complete(ctx, lease, forged); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("forged Evidence accepted: %v", err)
	}
	confirmed := []byte(`{"schemaVersion":"investigation-result/v1","status":"confirmed","summary":"model ranking","evidenceRefs":[],"candidateUpdates":[],"actionPlans":[],"partial":false,"degradedSources":[]}`)
	if err = repo.Complete(ctx, lease, confirmed); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("model confirmation accepted: %v", err)
	}
	valid := []byte(`{"schemaVersion":"investigation-result/v1","status":"probable","summary":"no evidence","evidenceRefs":[],"candidateUpdates":[],"actionPlans":[],"partial":false,"degradedSources":[]}`)
	if err = repo.Complete(ctx, lease, valid); err != nil {
		t.Fatal(err)
	}
	terminal, err := repo.Get(ctx, j.TenantID, j.JobID)
	if err != nil || terminal.State != "partial" || !strings.Contains(string(terminal.Result), `"unresolved"`) {
		t.Fatal("unsupported conclusion was not degraded")
	}
	if _, err = repo.BeginCall(ctx, lease, call); !errors.Is(err, investigation.ErrLease) {
		t.Fatal("ended Job admitted call")
	}
	_, events, err := repo.Events(ctx, j.TenantID, j.JobID, j.Subject, 0)
	if err != nil || len(events) < 8 {
		t.Fatalf("durable events: %d %v", len(events), err)
	}
	_, resumed, err := repo.Events(ctx, j.TenantID, j.JobID, j.Subject, events[3].EventID)
	if err != nil || len(resumed) != len(events)-4 || resumed[0].EventID != events[3].EventID+1 {
		t.Fatal("durable event resume")
	}
}

func TestSP06CancelExpireAndCurrentRevocation(t *testing.T) {
	ctx, db, repo, req := sp06JobFixture(t)
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repo.Claim(ctx, j.TenantID, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_findings", ArgsDigest: "sha256:args", Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 1024}}
	if _, err = repo.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE investigation.jobs SET expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, j.JobID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { _ = repo.Stop(ctx, j.TenantID, j.JobID, j.Subject, false) })
	wg.Go(func() { _ = repo.ExpirePass(ctx, j.TenantID) })
	wg.Wait()
	ended, err := repo.Get(ctx, j.TenantID, j.JobID)
	if err != nil || (ended.State != "cancelled" && ended.State != "expired") || ended.Reserved != (investigation.Usage{}) || ended.Consumed.ToolCalls != 1 {
		t.Fatalf("cancel/expire race: %+v %v", ended, err)
	}
	if err = repo.CompleteStep(ctx, lease, call.StepID, []byte(`{}`), investigation.Usage{}); !errors.Is(err, investigation.ErrLease) {
		t.Fatalf("post-terminal commit: %v", err)
	}
	req.TriggerKind = "new_evidence"
	j, err = repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2`, j.TenantID, j.Subject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Claim(ctx, j.TenantID, "revoked-worker", time.Minute); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked claim: %v", err)
	}
	var failedState, failedCode string
	err = db.QueryRowContext(ctx, `SELECT state,error_code FROM investigation.jobs WHERE job_id=$1`, j.JobID).Scan(&failedState, &failedCode)
	if err != nil || failedState != "failed" || failedCode != "AUTHORIZATION_REVOKED" {
		t.Fatal("revocation did not durably terminate")
	}
	if _, _, err = repo.Events(ctx, j.TenantID, j.JobID, j.Subject, 0); !errors.Is(err, investigation.ErrDenied) {
		t.Fatal("revoked SSE read")
	}
}

func TestSP06UnknownModelUsageCannotUndercharge(t *testing.T) {
	ctx, db, repo, req := sp06JobFixture(t)
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	l, err := repo.Claim(ctx, j.TenantID, "unknown-usage", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reserve := investigation.Usage{ModelRequests: 1, InputTokens: 16384, OutputTokens: 1024, ResultBytes: 65536}
	m, err := repo.AllocateModel(ctx, l, reserve, json.RawMessage(`{"requestDigest":"sha256:`+strings.Repeat("b", 64)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	response := json.RawMessage(`{"provider":"openai-compatible","usageKnown":false,"response":{"choices":[{"message":{"content":"usage absent"}}]}}`)
	zero := investigation.Usage{}
	if err = repo.SettleModel(ctx, l, m.StepID, response, &zero, ""); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, j.TenantID, j.JobID)
	if err != nil || got.Consumed != reserve {
		t.Fatalf("unknown usage undercharged: %+v %v", got.Consumed, err)
	}
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_findings", ArgsDigest: "sha256:unknown", Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 1024}}
	if _, err = repo.BeginCall(ctx, l, call); err != nil {
		t.Fatal(err)
	}
	if err = repo.Stop(ctx, j.TenantID, j.JobID, j.Subject, false); err != nil {
		t.Fatal(err)
	}
	var state, code string
	if err = db.QueryRowContext(ctx, `SELECT state,error_code FROM investigation.steps WHERE step_id=$1`, call.StepID).Scan(&state, &code); err != nil || state != "failed" || code != "UNKNOWN_OUTCOME" {
		t.Fatalf("terminal job left in-flight Step: %s %s %v", state, code, err)
	}
}
