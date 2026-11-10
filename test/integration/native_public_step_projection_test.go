package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
)

// The public step projection of a real native job.
//
// What is real in this scenario:
//   - the native operator account, its grant and its live session;
//   - the Worker job, the signed Worker context and the Worker lease;
//   - the frozen assistant input of the persisted turn, read through the real
//     Repository.GetAssistantInput of the Worker;
//   - the committed step row and the stored result bytes.
//
// What is a fixture and is not claimed to be more:
//   - the tool envelope around that assistant input is assembled by this test
//     in the shape of a tool-response/v3 answer. No MCP gateway call, no
//     Semantic API tool call and no model provider ran here: the Worker live
//     run with a real get_assistant_context invocation is an acceptance step
//     of the deployment, not of this test.
//
// The frozen assistant input really carries the internal admission material:
// the effective grant snapshot of the binding, the execution scope, the
// effective budget, the message history and its manifest digest.
//
// The public steps read of the same job must publish the public subset of that
// step: the identity, the state, the budget and the locatable facts. It must
// not publish the internal execution context. The stored row keeps its bytes:
// the projection only changes the answer of the read.
func TestNativePublicStepProjectionHidesAssistantContext(t *testing.T) {
	f := newNativeJobFixture(t)
	signer := investigation.ContextSigner{Transit: newNativeTestTransit(t), Key: "native-action-key"}
	workerCtx := context.Background()
	account := f.newAccount(t, `"apps"`)
	conversationID, turn := f.submitIncident(t, account, f.newIncident(t).IncidentID, "P4-05 public step projection")
	if turn.JobID == uuid.Nil {
		t.Fatal("the unified entry turn produced no job")
	}
	lease, err := f.jobs.Claim(workerCtx, f.tenantID, "native-dispatcher", 45*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	job, err := f.jobs.Get(workerCtx, f.tenantID, turn.JobID)
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
	_, toolLease, err := f.jobs.Authenticate(workerCtx, claims, job.ToolCatalogDigest)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	// The real assistant input of the persisted turn. It carries the effective
	// grant snapshot, the admission digests and the frozen model input: the very
	// material the public step must not publish.
	input, err := f.jobs.GetAssistantInput(workerCtx, toolLease)
	if err != nil {
		t.Fatalf("assistant input: %v", err)
	}
	var modelInput map[string]any
	if err = json.Unmarshal(input, &modelInput); err != nil {
		t.Fatalf("assistant input is not an object: %v", err)
	}
	if _, ok := modelInput["binding"]; !ok {
		t.Fatalf("the real assistant input carries no binding: %s", input)
	}
	// The fixture envelope wraps the real input as the data of a tool response.
	// It adds nothing: every field of the payload is the real assistant input.
	result, err := json.Marshal(map[string]any{
		"schemaVersion": "tool-response/v3",
		"tool":          "get_assistant_context",
		"state":         "succeeded",
		"partial":       false,
		"evidenceRefs":  []string{},
		"data":          modelInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{}`)
	allocation, err := f.jobs.AllocateTool(workerCtx, toolLease, "get_assistant_context", args)
	if err != nil {
		t.Fatalf("allocate the assistant context tool: %v", err)
	}
	// The reservation is the legal bound of the product for this tool: the MCP
	// gateway reserves one tool call, 1 MiB of result bytes and 100 evidence
	// items for the assistant context (internal/investigation/mcp/server.go,
	// toolReservation; locked by the unit test of that bound). The settlement
	// writes the budget back into the envelope and charges the normalized
	// output, so the reservation must cover the normalized output and not only
	// the bytes of the envelope this fixture hands over.
	reserve := investigation.Usage{ToolCalls: 1, ResultBytes: 1 << 20, EvidenceItems: 100}
	call := investigation.Call{Claims: &claims, StepID: allocation.StepID, ContextID: claims.ContextID, JTI: allocation.JTI,
		Name: "get_assistant_context", ArgsDigest: investigation.ArgumentsDigest(args), Reserve: reserve}
	if _, err = f.jobs.BeginCall(workerCtx, toolLease, call); err != nil {
		t.Fatalf("begin tool call: %v", err)
	}
	// The settlement charges the reservation the same way the gateway does: the
	// output envelope consumes bytes too, so the bound is charged conservatively.
	used := reserve
	used.EvidenceItems = 0
	used.ResultBytes = reserve.ResultBytes
	if _, err = f.jobs.CompleteToolStep(workerCtx, toolLease, allocation.StepID, result, used); err != nil {
		t.Fatalf("complete the assistant context step: %v", err)
	}
	// The stored row keeps the internal material: the projection must not
	// rewrite the persisted result.
	var stored string
	if err = f.db.QueryRowContext(f.ctx, `SELECT result::text FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3`,
		f.tenantID, turn.JobID, allocation.StepID).Scan(&stored); err != nil {
		t.Fatalf("read the stored step result: %v", err)
	}
	for _, want := range []string{"grantSnapshot", "executionScope", "effectiveBudget", "historyManifestDigest"} {
		if !strings.Contains(stored, want) {
			t.Fatalf("the stored step result misses the real %q: %s", want, stored)
		}
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	handlers := &httpapi.InvestigationHandlers{
		Repository: investigation.Repository{Pool: f.apiPool},
		CursorKey:  ed25519.PrivateKey(private),
	}
	owner := auth.WithRequestContext(account.ctx, auth.RequestContext{
		TenantID: f.tenantID, Subject: account.subject, Roles: []auth.Role{auth.Operator},
		RequestID: "p405-native-step-projection", TokenExpiresAt: time.Now().Add(time.Hour),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/investigations/"+turn.JobID.String()+"/steps", nil)
	response := httptest.NewRecorder()
	handlers.ServeHTTP(response, request.WithContext(owner))
	if response.Code != 200 {
		t.Fatalf("owner steps read = %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	// The public step keeps the identity, the state, the budget and the
	// locatable public subset of the assistant context.
	for _, want := range []string{
		`"toolName":"get_assistant_context"`, `"state":"succeeded"`, `"stepId":"` + allocation.StepID.String() + `"`,
		`"schemaVersion":"assistant-input/v1"`, `"consumed"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the public steps read misses %s: %s", want, body)
		}
	}
	// The public step does not publish the internal execution context.
	for _, leaked := range []string{
		"grantSnapshot", "grant_snapshot", "contextDigest", "context_digest",
		"inputDigest", "input_digest", "assistant_binding", "assistantBinding",
		"sourceCapabilities", "analysisCatalog", "effectiveBudget",
		"historyManifestDigest", "executionScope", "authorization_source", "authorizationSource",
		"identity_source", "identitySource",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("the public steps read publishes the internal %q: %s", leaked, body)
		}
	}
	// The public read creates no job and changes no row.
	var jobs int
	if err = f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`,
		f.tenantID, turn.JobID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("the public steps read changed the job rows: %d", jobs)
	}
	// The public read does not rewrite the stored bytes of the step.
	var after string
	if err = f.db.QueryRowContext(f.ctx, `SELECT result::text FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3`,
		f.tenantID, turn.JobID, allocation.StepID).Scan(&after); err != nil {
		t.Fatalf("read the stored step result again: %v", err)
	}
	if after != stored {
		t.Fatalf("the public steps read rewrote the stored result:\nbefore %s\nafter  %s", stored, after)
	}
	_ = conversationID
	f.quarantine(t, turn.JobID)
}
