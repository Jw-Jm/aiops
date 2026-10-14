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
	"ops-platform/internal/app"
	"ops-platform/internal/assistant"
	"ops-platform/internal/investigation"
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
	if os.Getenv("OPS_ASSISTANT_RUNTIME_ACCEPTANCE") != "1" {
		t.Skip("owned remote process/DB runtime required")
	}
	ctx, db, pool, b, repo, jobs := assistantExtendedFixture(t)
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "Actual kill/recovery", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC"}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "process-kill", assistant.SubmitMessage{Message: "列出当前可用能力", ExpectedConversationRevision: 1})
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
	if err = jobs.Complete(ctx, replacement, assistantEmptyAnswer()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='get_assistant_context' AND state='succeeded'`, b.TenantID, turn.JobID).Scan(&count); err != nil || count != 1 {
		t.Fatal("tool executed more than once", count, err)
	}
	t.Log("actual SIGKILL + lease expiry + v3 takeover; committed Step reused exactly once; old generation fenced")
}
