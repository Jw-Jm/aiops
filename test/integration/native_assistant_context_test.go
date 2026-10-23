package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
)

// nativeTestTransit is an ES256 signer for the integration boundary. It replaces
// only the OpenBao Transit call; the signing format, the DB context register and
// the verification path are the production code. A real Transit key rotation test
// lives in the guarded OpenBao suite; this file must not be read as Bao evidence.
type nativeTestTransit struct{ key *ecdsa.PrivateKey }

func newNativeTestTransit(t *testing.T) nativeTestTransit {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return nativeTestTransit{key: key}
}

func (s nativeTestTransit) SigningKeys(context.Context, string) (map[int]*ecdsa.PublicKey, int, error) {
	return map[int]*ecdsa.PublicKey{1: &s.key.PublicKey}, 1, nil
}

func (s nativeTestTransit) SignJWS(_ context.Context, _ string, _ int, input []byte) ([]byte, error) {
	digest := sha256.Sum256(input)
	r, ss, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, 64)
	r.FillBytes(out[:32])
	ss.FillBytes(out[32:])
	return out, nil
}

const nativeAssistantWorkload = "spiffe://ops.local/ns/native-job/sa/ops-investigator"

func nativeAssistantAnswer(t *testing.T, summary string) []byte {
	t.Helper()
	raw, err := json.Marshal(investigation.AssistantAnswer{
		SchemaVersion: "assistant-answer/v1", AnswerKind: "unsupported", Summary: summary,
		FactRefs: []investigation.FactRef{}, QueryResultRefs: []uuid.UUID{}, EvidenceRefs: []string{},
		EntityRefs: []string{}, MissingEvidence: []string{"fixture_no_live_source"}, ScopeSummary: "operational",
		AsOf: time.Now().UTC(), DegradedSources: []string{}, Warnings: []string{}, Hypotheses: []investigation.Hypothesis{}, NextChecks: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// pickFixtureTool selects one enabled assistant tool whose empty argument object
// validates, so the test exercises the real allocation/settlement path without
// inventing tool arguments.
func pickFixtureTool(t *testing.T, defs []tools.Definition) tools.Definition {
	t.Helper()
	for _, d := range defs {
		if d.Disabled == "" && d.Validate(json.RawMessage(`{}`)) == nil {
			return d
		}
	}
	names := []string{}
	for _, d := range defs {
		names = append(names, d.Name+"("+d.Disabled+")")
	}
	t.Fatalf("no fixture tool accepts an empty argument object: %v", names)
	return tools.Definition{}
}

// A native business turn must be dispatchable by a Worker that has no browser
// session: the signed Context carries the persisted native authority, the scope
// and the frozen model/policy versions, and a tool result is persisted before the
// answer is published back to the owner.
func TestNativeAssistantSignedContextAndToolPersistence(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	conversationID, turn := f.submit(t, account, "原生签名上下文")

	signer := investigation.ContextSigner{Transit: newNativeTestTransit(t), Key: "native-action-key"}
	workerCtx := context.Background()
	lease, err := f.jobs.Claim(workerCtx, f.tenantID, "native-dispatcher", 45*time.Second)
	if err != nil {
		t.Fatalf("worker claim without a session: %v", err)
	}
	if lease.JobID != turn.JobID {
		t.Fatalf("claimed job = %s want %s", lease.JobID, turn.JobID)
	}
	job, err := f.jobs.Get(workerCtx, f.tenantID, turn.JobID)
	if err != nil {
		t.Fatalf("worker read job: %v", err)
	}
	defs, err := tools.ForJob(job)
	if err != nil {
		t.Fatal(err)
	}
	names := tools.Names(defs)
	jobToken, err := signer.IssueRegistered(workerCtx, f.jobs, job, lease, "platform-job-api", nativeAssistantWorkload, names)
	if err != nil {
		t.Fatalf("issue registered job context: %v", err)
	}
	claims, err := signer.VerifyContext(workerCtx, jobToken, "platform-job-api", nativeAssistantWorkload)
	if err != nil {
		t.Fatalf("verify job context: %v", err)
	}
	// The Context must still describe the native account and the frozen admission
	// facts, not a subject prefix or a legacy binding.
	if claims.Subject != account.subject || claims.Assistant == nil ||
		claims.Assistant.AuthorizationSource != "native" || claims.Assistant.AuthorizationUserID != account.userID ||
		claims.Assistant.AuthorizationVersion != 1 {
		t.Fatalf("context authority = subject=%q assistant=%+v", claims.Subject, claims.Assistant)
	}
	if claims.Assistant.ModelProfileVersion != "native-job-profile/v1" || claims.Assistant.CatalogVersion != "assistant-tools/v2" || claims.PolicyVersion != f.policy {
		t.Fatalf("frozen admission not carried: %+v policy=%q", claims.Assistant, claims.PolicyVersion)
	}
	if claims.Scope.Cluster != f.clusterUID || !slices.Contains(claims.Scope.Namespaces, "apps") {
		t.Fatalf("context scope = %+v", claims.Scope)
	}
	if claims.LeaseGeneration != lease.Generation {
		t.Fatalf("context generation = %d want %d", claims.LeaseGeneration, lease.Generation)
	}
	if _, err = signer.VerifyContext(workerCtx, jobToken, "platform-mcp-gateway", nativeAssistantWorkload); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("wrong audience accepted: %v", err)
	}
	if _, err = signer.VerifyContext(workerCtx, jobToken, "platform-job-api", "spiffe://ops.local/ns/other/sa/ops-investigator"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("wrong workload accepted: %v", err)
	}
	// The MCP context is a second, independently signed audience for the same job.
	if _, err = signer.IssueRegistered(workerCtx, f.jobs, job, lease, "platform-mcp-gateway", nativeAssistantWorkload, names); err != nil {
		t.Fatalf("issue registered mcp context: %v", err)
	}

	// Authenticate re-checks the registered context, the lease and the persisted
	// native authority inside one transaction.
	authorizedJob, toolLease, err := f.jobs.Authenticate(workerCtx, claims, job.ToolCatalogDigest)
	if err != nil {
		t.Fatalf("authenticate native context: %v", err)
	}
	if authorizedJob.JobID != turn.JobID {
		t.Fatalf("authenticated job = %s", authorizedJob.JobID)
	}
	def, err := tools.ForJob(authorizedJob)
	if err != nil {
		t.Fatal(err)
	}
	tool := pickFixtureTool(t, def)
	args := json.RawMessage(`{}`)
	allocation, err := f.jobs.AllocateTool(workerCtx, toolLease, tool.Name, args)
	if err != nil {
		t.Fatalf("allocate tool %s: %v", tool.Name, err)
	}
	call := investigation.Call{Claims: &claims, StepID: allocation.StepID, ContextID: claims.ContextID, JTI: allocation.JTI, Name: tool.Name, ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 1024}}
	if _, err = f.jobs.BeginCall(workerCtx, toolLease, call); err != nil {
		t.Fatalf("begin tool call: %v", err)
	}
	if _, err = f.jobs.BeginCall(workerCtx, toolLease, call); !errors.Is(err, investigation.ErrReplay) {
		t.Fatalf("issued context replay = %v want ErrReplay", err)
	}
	result, err := json.Marshal(map[string]any{"tool": tool.Name, "data": map[string]any{"fixtureSource": "integration", "items": []any{}}, "partial": false, "evidenceRefs": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	committed, err := f.jobs.CompleteToolStep(workerCtx, toolLease, allocation.StepID, result, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(result))})
	if err != nil {
		t.Fatalf("complete tool step: %v", err)
	}
	if len(committed) == 0 {
		t.Fatal("tool result was not persisted")
	}
	steps, err := f.jobs.Steps(workerCtx, toolLease)
	if err != nil || len(steps) != 1 || steps[0].State != "succeeded" || steps[0].Name != tool.Name {
		t.Fatalf("worker step view = %+v (%v)", steps, err)
	}

	// The answer is published through the lease and becomes readable by the owner;
	// the tool step stays attached to the turn.
	if err = f.jobs.Complete(workerCtx, toolLease, nativeAssistantAnswer(t, "原生签名上下文工具结果")); err != nil {
		t.Fatalf("complete native turn: %v", err)
	}
	completed, err := f.repo.Turn(account.ctx, f.tenantID, turn.TurnID, account.subject)
	if err != nil {
		t.Fatalf("owner turn readback: %v", err)
	}
	if completed.State != "completed" || completed.Answer == nil {
		t.Fatalf("owner turn = %+v", completed)
	}
	var answer investigation.AssistantAnswer
	if json.Unmarshal(completed.Answer, &answer) != nil || answer.Summary != "原生签名上下文工具结果" {
		t.Fatalf("persisted answer = %s", completed.Answer)
	}
	conversation, events, err := f.repo.Events(account.ctx, f.tenantID, conversationID, account.subject, 0)
	if err != nil {
		t.Fatalf("owner event readback: %v", err)
	}
	if conversation.ConversationID != conversationID || len(events) == 0 {
		t.Fatalf("owner events = %d conversations=%+v", len(events), conversation)
	}

	// Takeover after a Worker restart: a second queued native turn is claimed, its
	// Context is issued, the lease expires, and the restarted Worker must take the
	// same job over while the old Context is refused.
	_, pending := f.submit(t, account, "原生重启接管")
	pendingLease, err := f.jobs.Claim(workerCtx, f.tenantID, "native-dispatcher", 45*time.Second)
	if err != nil {
		t.Fatalf("claim second turn: %v", err)
	}
	pendingJob, err := f.jobs.Get(workerCtx, f.tenantID, pending.JobID)
	if err != nil {
		t.Fatal(err)
	}
	pendingDefs, err := tools.ForJob(pendingJob)
	if err != nil {
		t.Fatal(err)
	}
	pendingToken, err := signer.IssueRegistered(workerCtx, f.jobs, pendingJob, pendingLease, "platform-job-api", nativeAssistantWorkload, tools.Names(pendingDefs))
	if err != nil {
		t.Fatalf("issue takeover context: %v", err)
	}
	pendingClaims, err := signer.VerifyContext(workerCtx, pendingToken, "platform-job-api", nativeAssistantWorkload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE investigation.worker_queue SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, pending.JobID); err != nil {
		t.Fatal(err)
	}
	next, err := f.jobs.Claim(workerCtx, f.tenantID, "native-dispatcher-restarted", 45*time.Second)
	if err != nil {
		t.Fatalf("claim after restart: %v", err)
	}
	if next.JobID != pending.JobID || next.Generation <= pendingLease.Generation {
		t.Fatalf("takeover lease = %+v (first %+v)", next, pendingLease)
	}
	if _, _, err = f.jobs.Authenticate(workerCtx, pendingClaims, pendingJob.ToolCatalogDigest); !errors.Is(err, investigation.ErrLease) {
		t.Fatalf("old context after takeover = %v want ErrLease", err)
	}
	if _, err = f.jobs.GetAssistantInput(workerCtx, next); err != nil {
		t.Fatalf("restarted worker input: %v", err)
	}
	f.quarantine(t, pending.JobID)
}

// Revocation and cancellation must stop a native turn instead of letting a
// queued Worker inherit authority it no longer has.
func TestNativeAssistantContextRefusesRevocationAndCancel(t *testing.T) {
	f := newNativeJobFixture(t)
	signer := investigation.ContextSigner{Transit: newNativeTestTransit(t), Key: "native-action-key"}
	workerCtx := context.Background()

	revoked := f.newAccount(t, `"apps"`)
	_, revokedTurn := f.submit(t, revoked, "撤权上下文")
	lease, err := f.jobs.Claim(workerCtx, f.tenantID, "native-dispatcher", 45*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	job, err := f.jobs.Get(workerCtx, f.tenantID, revokedTurn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := tools.ForJob(job)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.IssueRegistered(workerCtx, f.jobs, job, lease, "platform-job-api", nativeAssistantWorkload, tools.Names(defs))
	if err != nil {
		t.Fatalf("issue context: %v", err)
	}
	claims, err := signer.VerifyContext(workerCtx, token, "platform-job-api", nativeAssistantWorkload)
	if err != nil {
		t.Fatalf("verify context: %v", err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration revoke',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, revoked.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, revoked.userID); err != nil {
		t.Fatal(err)
	}
	// An already-signed Context is refused after revocation, and the job fails
	// instead of being dispatched.
	if _, _, err = f.jobs.Authenticate(workerCtx, claims, job.ToolCatalogDigest); err == nil {
		t.Fatal("revoked native context was accepted")
	}

	// Cancellation is a persisted terminal state for the queued native turn.
	cancelled := f.newAccount(t, `"apps"`)
	_, cancelledTurn := f.submit(t, cancelled, "取消原生会话")
	if err = f.repo.Cancel(cancelled.ctx, f.tenantID, cancelledTurn.TurnID, cancelled.subject, "native-cancel"); err != nil {
		t.Fatalf("cancel native turn: %v", err)
	}
	readback, err := f.repo.Turn(cancelled.ctx, f.tenantID, cancelledTurn.TurnID, cancelled.subject)
	if err != nil {
		t.Fatalf("cancelled turn readback: %v", err)
	}
	if readback.State != "cancelled" {
		t.Fatalf("cancelled turn state = %q", readback.State)
	}
	if _, err = f.jobs.Claim(workerCtx, f.tenantID, "native-dispatcher", 45*time.Second); !errors.Is(err, investigation.ErrNoJob) {
		t.Fatalf("claim after cancel = %v want ErrNoJob", err)
	}
}
