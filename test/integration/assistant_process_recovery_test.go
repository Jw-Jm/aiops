package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/app"
	"ops-platform/internal/assistant"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/resource"
)

type assistantCrashConfig struct {
	DatabaseURL string
	Tenant      uuid.UUID
	ReadyFile   string
}
type assistantCrashState struct {
	Lease      investigation.Lease
	StepID     uuid.UUID
	ArgsDigest string
}

// Child runs the real v3 repository with the actual least-privilege worker login.
// The tool body is an explicitly labelled protocol fixture; no model semantics
// or source-data correctness is inferred from the kill/recovery test.
func TestAssistantCrashWorkerChild(t *testing.T) {
	path := os.Getenv("OPS_ASSISTANT_CRASH_CHILD_CONFIG")
	if path == "" {
		t.Skip("subprocess helper only")
	}
	var cfg assistantCrashConfig
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("child configuration")
	}
	ctx := context.Background()
	pool, err := app.OpenRuntimePool(ctx, cfg.DatabaseURL, "worker_runtime_role")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := investigation.Repository{Pool: pool, AssistantEnabled: true}
	lease, err := repo.Claim(ctx, cfg.Tenant, "killable-v3-worker", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{}`)
	step := uuid.New()
	call := investigation.Call{StepID: step, ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_assistant_context", Arguments: args, ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536}}
	if _, err = repo.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"schemaVersion":"tool-response/v3","tool":"get_assistant_context","state":"succeeded","data":{"protocolFixture":true},"evidenceRefs":[],"degradedSources":[],"partial":false}`)
	if _, err = repo.CompleteToolStep(ctx, lease, step, result, investigation.Usage{ToolCalls: 1}); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(assistantCrashState{lease, step, call.ArgsDigest})
	if os.WriteFile(cfg.ReadyFile, raw, 0600) != nil {
		t.Fatal("child ready file")
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestAssistantActualProcessKillAndTakeover(t *testing.T) {
	testAssistantActualProcessKillAndTakeover(t, false, "complete")
}

func TestAssistantEvidenceOnlyProcessKillAndTakeover(t *testing.T) {
	for _, terminal := range []string{"complete", "cancel", "budget"} {
		t.Run(terminal, func(t *testing.T) { testAssistantActualProcessKillAndTakeover(t, true, terminal) })
	}
}

func testAssistantActualProcessKillAndTakeover(t *testing.T, evidenceOnly bool, terminal string) {
	if os.Getenv("OPS_ASSISTANT_RUNTIME_ACCEPTANCE") != "1" {
		t.Skip("owned remote process/DB runtime required")
	}
	ctx, db, pool, b, repo, jobs := assistantExtendedFixture(t)
	conversation := assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "Actual kill/recovery", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC"}}
	message := assistant.SubmitMessage{Message: "列出当前可用能力", ExpectedConversationRevision: 1}
	if evidenceOnly {
		repo.FaultChainEnabled = true
		now := time.Now().UTC().Truncate(time.Second)
		target := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: uuid.NewString()}.String()
		// Identity and tool data are explicit fixtures; PostgreSQL roles,
		// process death, lease takeover and evidence-only admission are real.
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','apps','protocol-fixture-pod','{}',clock_timestamp())`, b.TenantID, target, b.ClusterID); err != nil {
			t.Fatal(err)
		}
		grant, err := (graph.Authorization{Pool: pool}).Effective(ctx, b.TenantID.String(), conversation.OwnerSubject, b.ClusterUID)
		if err != nil {
			t.Fatal(err)
		}
		scope, err := analytics.RestrictScope(grant, analytics.Selection{Namespaces: []string{"apps"}})
		if err != nil {
			t.Fatal(err)
		}
		scope.Resources = []string{target}
		rc := assistant.ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: uuid.New(), TenantID: b.TenantID.String(), OwnerSubject: conversation.OwnerSubject, ClusterUID: b.ClusterUID, Resources: []assistant.Candidate{{CanonicalID: target, Kind: "Pod", Name: "protocol-fixture-pod", Namespace: "apps", ObservedAt: now}}, Symptom: "协议夹具：验证无模型调查进程恢复", TimeRange: assistant.TimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}, ResolvedAt: now, ObservationRevision: graph.Revision{OwnerEpoch: 1, GraphGeneration: 1}, GrantSnapshotDigest: graph.ScopeDigest(grant), ExecutionScope: scope, ExecutionScopeDigest: graph.ScopeDigest(scope)}
		rc.ContextDigest = graph.ScopeDigest(rc)
		conversation.Mode = "troubleshoot"
		conversation.Binding.ResourceContext = &rc
		conversation.Binding.Selection = analytics.Selection{Namespaces: []string{"apps"}}
		message.FaultChainRequest = &investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: target, Fault: analytics.QueryTimeRange{From: rc.TimeRange.From, To: rc.TimeRange.To, Timezone: "UTC"}, MaxEdges: 4}
	}
	c, err := repo.Create(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "process-kill", message)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	configFile := filepath.Join(dir, "child.json")
	ready := filepath.Join(dir, "ready.json")
	raw, _ := json.Marshal(assistantCrashConfig{pool.Config().ConnString(), b.TenantID, ready})
	if err = os.WriteFile(configFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, executable, "-test.run", "^TestAssistantCrashWorkerChild$", "-test.timeout", "2m")
	child.Env = append(os.Environ(), "OPS_ASSISTANT_CRASH_CHILD_CONFIG="+configFile)
	// Capture stderr only in a private file; never put DSN/config in test output.
	logFile, err := os.OpenFile(filepath.Join(dir, "child.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	child.Stdout = logFile
	child.Stderr = logFile
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	var state assistantCrashState
	for n := 0; n < 100; n++ {
		raw, err = os.ReadFile(ready)
		if err == nil && json.Unmarshal(raw, &state) == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if state.StepID == uuid.Nil {
		t.Fatal("child never committed the protocol Step; private diagnostics retained during test")
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err == nil {
		t.Fatal("expected SIGKILL process exit")
	}
	time.Sleep(2100 * time.Millisecond)
	replacement, err := jobs.Claim(ctx, b.TenantID, "replacement-v3-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.JobID != turn.JobID || replacement.Generation <= state.Lease.Generation {
		t.Fatal("wrong takeover generation")
	}
	if evidenceOnly {
		job, err := jobs.Get(ctx, b.TenantID, turn.JobID)
		if err != nil || job.Assistant == nil || job.Assistant.ExecutionMode != "evidence_only" || job.Budget.ModelRequests != 0 {
			t.Fatal("takeover changed execution mode or model budget", err)
		}
		input, err := jobs.GetAssistantInput(ctx, replacement)
		var frozen assistant.TurnInput
		if err != nil || json.Unmarshal(input, &frozen) != nil || frozen.FaultChainRequest == nil || graph.ScopeDigest(*frozen.FaultChainRequest) != graph.ScopeDigest(*message.FaultChainRequest) {
			t.Fatal("takeover changed frozen fault-chain request", err)
		}
	}
	allocation, err := jobs.AllocateTool(ctx, replacement, "get_assistant_context", json.RawMessage(`{}`))
	if err != nil || !allocation.Committed || allocation.StepID != state.StepID {
		t.Fatal("committed tool was not reused", err)
	}
	if _, err = jobs.RecoverStep(ctx, replacement, state.StepID, state.ArgsDigest); err != nil {
		t.Fatal(err)
	}
	if err = jobs.Complete(ctx, state.Lease, assistantEmptyAnswer()); !errors.Is(err, investigation.ErrLease) {
		t.Fatal("killed generation accepted a late answer", err)
	}
	switch terminal {
	case "cancel":
		if err = repo.Cancel(ctx, b.TenantID, turn.TurnID, c.OwnerSubject, "cancel-recovered-evidence-only"); err != nil {
			t.Fatal(err)
		}
		if err = jobs.Complete(ctx, replacement, assistantEmptyAnswer()); !errors.Is(err, investigation.ErrLease) {
			t.Fatal("cancelled replacement published an answer", err)
		}
		current, err := repo.Turn(ctx, b.TenantID, turn.TurnID, c.OwnerSubject)
		if err != nil || current.State != "cancelled" {
			t.Fatal("cancelled turn lost state", err)
		}
	case "budget":
		if err = jobs.ExhaustBudget(ctx, replacement); err != nil {
			t.Fatal(err)
		}
		current, err := repo.Turn(ctx, b.TenantID, turn.TurnID, c.OwnerSubject)
		var answer investigation.AssistantAnswer
		if err != nil || current.State != "partial" || json.Unmarshal(current.Answer, &answer) != nil || len(answer.QueryResultRefs) != 0 || len(answer.FactRefs) != 0 || len(answer.MissingEvidence) == 0 {
			t.Fatal("budget exhaustion invented uncommitted statistics", err)
		}
	default:
		if err = jobs.Complete(ctx, replacement, assistantEmptyAnswer()); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='get_assistant_context' AND state='succeeded'`, b.TenantID, turn.JobID).Scan(&count); err != nil || count != 1 {
		t.Fatal("tool executed more than once", count, err)
	}
	if evidenceOnly {
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='model'`, b.TenantID, turn.JobID).Scan(&count); err != nil || count != 0 {
			t.Fatal("evidence-only recovery admitted model step", err)
		}
	}
	t.Log("actual SIGKILL + lease expiry + v3 takeover; committed Step reused exactly once; old generation fenced")
}
