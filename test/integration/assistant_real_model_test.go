package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/app"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/investigation"
	"ops-platform/internal/modelprofile"
)

// The identity/tenant setup is isolated Fixture data. Model, PostgreSQL,
// OpenBao signing, mTLS, official MCP and HolmesGPT are actual runtime paths.
// Passing this smoke test does not qualify the model's diagnostic quality.
func TestAssistantRealModelMCP(t *testing.T) {
	if os.Getenv("OPS_ASSISTANT_REAL_MODEL") != "1" {
		t.Skip("actual model smoke test requires explicit isolated runtime")
	}
	image := os.Getenv("OPS_ASSISTANT_TEST_INVESTIGATOR_IMAGE")
	python := os.Getenv("OPS_ASSISTANT_TEST_PYTHON_SOURCE")
	if image == "" || python == "" {
		t.Fatal("owned investigator image and updated source required")
	}
	ctx, db, workerPool, b := sp05Database(t)
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sp06Policy(t, ctx, db, workerPool, b.TenantID, priv)
	trust := configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{"integration-key": pub}}
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, workerPool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	ca, err := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), CACertBundle: ca, Token: os.Getenv("SP03_TEST_OPENBAO_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	if err = bao.ConfigureTransit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = bao.ConfigureInvocationSigning(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := sp06Certificates(t, dir)
	tokenFile := filepath.Join(dir, "bao.token")
	if os.WriteFile(tokenFile, []byte(os.Getenv("SP03_TEST_OPENBAO_TOKEN")), 0600) != nil {
		t.Fatal("token file")
	}
	t.Setenv("OPENBAO_ADDR", os.Getenv("SP03_TEST_OPENBAO_URL"))
	t.Setenv("OPENBAO_CA_FILE", os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	t.Setenv("OPENBAO_INVOCATION_TOKEN_FILE", tokenFile)
	apiAddress, investigatorAddress := sp06Port(t), sp06Port(t)
	profileBytes, err := os.ReadFile("../fixtures/assistant/model-profile.fixture.json")
	var material modelprofile.Profile
	if err != nil || json.Unmarshal(profileBytes, &material) != nil {
		t.Fatal("profile fixture missing", err)
	}
	profile := material.Reference()
	config := app.SP06Config{Namespace: "sp06-test", ListenAddress: apiAddress, InvestigatorEndpoint: "https://" + investigatorAddress, CertificateFile: files["ops-api.pem"], PrivateKeyFile: files["ops-api.key"], CAFile: files["ca.pem"], CRLFile: files["crl.pem"], PolicyName: "sp06-read-only", Tenants: []uuid.UUID{b.TenantID}, Budget: investigation.DefaultBudget(), Assistant: &app.AssistantRuntimeConfig{Enabled: true, ModelProfileVersion: profile, Profile: &material}}
	runtimeFile := filepath.Join(dir, "runtime.json")
	write := func(path string, value any) {
		raw, e := json.Marshal(value)
		if e != nil || os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("runtime configuration")
		}
	}
	write(runtimeFile, config)
	t.Setenv("SP06_RUNTIME_FILE", runtimeFile)
	_, cursorKey, _ := ed25519.GenerateKey(rand.Reader)
	sp04 := &httpapi.SP04Handlers{Pool: apiPool, SigningKey: cursorKey, SP05: &httpapi.SP05Handlers{Pool: apiPool, Enabled: true}}
	sp04.SP05.GraphAPI = sp04
	stopAPI, err := app.StartSP06API(ctx, apiPool, sp04, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer stopAPI()
	investigatorFile := filepath.Join(dir, "investigator.json")
	keyFile := filepath.Join(dir, "model.key")
	if os.WriteFile(keyFile, []byte("local-ollama-no-auth"), 0600) != nil {
		t.Fatal("model key file")
	}
	write(investigatorFile, map[string]any{"listenAddress": investigatorAddress, "jobAPI": "https://" + apiAddress, "mcpEndpoint": "https://" + apiAddress + "/mcp", "apiServerName": "ops-api.sp06-test.svc.cluster.local", "caFile": files["ca.pem"], "crlFile": files["crl.pem"], "certificateFile": files["ops-investigator.pem"], "privateKeyFile": files["ops-investigator.key"], "workerIdentity": "spiffe://ops.local/ns/sp06-test/sa/ops-worker", "modelAPIKeyFile": keyFile, "assistantModelProfileVersion": profile, "assistantModelProfile": material, "model": map[string]any{"base_url": "http://127.0.0.1:11434/v1", "api_key_ref": "secret://model/api-key", "model": "qwen2.5:1.5b", "timeout": 120, "token_budget": 2048}})
	name := "ops-assistant-smoke-" + uuid.NewString()
	process := exec.CommandContext(ctx, "docker", "run", "--rm", "--name", name, "--label", "ops.task=assistant-20261012", "--network", "host", "--user", "0", "-v", dir+":"+dir+":ro", "-v", python+":/assistant-src:ro", "-e", "PYTHONPATH=/assistant-src", "-e", "SP06_INVESTIGATOR_FILE="+investigatorFile, "--entrypoint", "python", image, "-m", "investigator.worker")
	var output bytes.Buffer
	process.Stdout = &output
	process.Stderr = &output
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = exec.Command("docker", "stop", "--time", "2", name).Run()
		_ = process.Wait()
		t.Log("investigator diagnostics", output.String())
	}()
	ready := false
	for n := 0; n < 200; n++ {
		conn, e := net.DialTimeout("tcp", investigatorAddress, 100*time.Millisecond)
		if e == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("investigator startup unavailable")
	}
	config.CertificateFile = files["ops-worker.pem"]
	config.PrivateKeyFile = files["ops-worker.key"]
	write(runtimeFile, config)
	stopWorker, err := app.StartSP06Worker(ctx, workerPool)
	if err != nil {
		t.Fatal(err)
	}
	defer stopWorker()
	ready = false
	for n := 0; n < 100; n++ {
		var exists bool
		err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.assistant_worker_capabilities WHERE tenant_id=$1 AND model_profile_version=$2 AND expires_at>clock_timestamp())`, b.TenantID, profile).Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if exists {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("assistant worker unavailable")
	}
	actor := auth.RequestContext{TenantID: b.TenantID, Subject: "sp06-admin", Roles: []auth.Role{auth.PlatformAdmin}, RequestID: uuid.NewString()}
	requestCtx := auth.WithRequestContext(ctx, actor)
	repo := sp04.Assistant.Repository
	c, err := repo.Create(requestCtx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: actor.Subject, Title: "实际模型只读烟测", Mode: "data_query", AuthorizationDomain: "audit", Binding: assistant.ConversationBinding{Timezone: "Asia/Shanghai"}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := repo.Submit(requestCtx, b.TenantID, c.ConversationID, actor.Subject, "real-model-smoke", assistant.SubmitMessage{Message: "请描述你可以查询的平台审计数据范围。不要查询数字；如果信息不足，返回 clarification 或 unsupported。先读取当前助手上下文。", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		current, e := repo.Turn(requestCtx, b.TenantID, turn.TurnID, actor.Subject)
		if e != nil {
			t.Fatal(e)
		}
		if current.State != "queued" && current.State != "running" {
			if current.State != "completed" && current.State != "awaiting_user" {
				t.Fatalf("actual model turn state=%s error=%s", current.State, current.ErrorCode)
			}
			var answer investigation.AssistantAnswer
			if json.Unmarshal(current.Answer, &answer) != nil || answer.SchemaVersion != "assistant-answer/v1" {
				t.Fatal("answer unavailable")
			}
			var calls int
			if db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='get_assistant_context' AND state='succeeded'`, b.TenantID, turn.JobID).Scan(&calls) != nil || calls == 0 {
				t.Fatal("model did not commit the required MCP context read")
			}
			t.Logf("actual model/MCP smoke answerKind=%s contextReads=%d", answer.AnswerKind, calls)
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("actual model smoke deadline exceeded")
}
