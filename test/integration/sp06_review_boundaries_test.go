package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/investigation"
	gateway "ops-platform/internal/investigation/mcp"
	"ops-platform/internal/investigation/tools"
)

type sp06D1Fixture struct{ ref *string }

func (f sp06D1Fixture) CheckScope(ctx context.Context, j investigation.Job, name string, args json.RawMessage) error {
	return tools.CheckArguments(j, name, args)
}
func (f sp06D1Fixture) Invoke(_ context.Context, _ investigation.Job, name string, _ json.RawMessage) (tools.Result, error) {
	return tools.Result{Tool: name, State: "succeeded", Data: map[string]any{"fixture": "D1 archived evidence boundary"}, EvidenceRefs: []string{*f.ref}, DegradedSources: []string{}}, nil
}

func TestSP06NarrowedContextCannotReadOrCommitD1(t *testing.T) {
	ctx, db, repo, req := sp06JobFixture(t)
	pool := repo.Pool.(*pgxpool.Pool)
	archives := sp05GoldenArchive(t, ctx, pool, req.TenantID)
	var canonical, sourceID, backend string
	if err := db.QueryRowContext(ctx, `SELECT f.resource_canonical_id,f.source_id,s.backend_logical_id FROM finding.records f JOIN platform.source_registrations s USING(tenant_id,source_id) WHERE f.tenant_id=$1 LIMIT 1`, req.TenantID).Scan(&canonical, &sourceID, &backend); err != nil {
		t.Fatal(err)
	}
	binding, err := (evidence.Repository{Pool: pool}).RegisteredBinding(ctx, evidence.Binding{Tenant: req.TenantID.String(), SourceID: sourceID, Revision: 1, SourceType: "kubernetes", BackendLogicalID: backend})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data := []byte(`{"redactedLog":"bounded D1"}`)
	ev := evidence.Evidence{SchemaVersion: "evidence/v2", EvidenceID: uuid.NewString(), TenantID: req.TenantID.String(), ResourceCanonicalID: canonical, Type: "resource_state", DataClass: "D1", SourceRegistrationID: sourceID, SourceRevision: 1, SourceScopeDigest: evidence.BindingScopeDigest(binding), SourceSystem: "kubernetes", BackendLogicalID: backend, QueryTemplateVersion: "sp05-signal/v1", QueryHash: evidence.Digest(data), EffectiveScope: req.Scope, EvaluatedAt: now, ObservedFrom: now.Add(-time.Minute), ObservedTo: now, SourceRetentionUntil: now.Add(time.Hour), TimeReliable: true, ReplayState: "archive_pending", IndependenceGroup: "native-node", DerivationEvidenceRefs: []string{}, ContentDigest: evidence.Digest(data), Data: data}
	if err = archives.Capture(ctx, ev, "", now.Add(181*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	defs, _ := tools.Catalog()
	req.ToolCatalogDigest = tools.Digest(defs)
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	l, err := repo.Claim(ctx, j.TenantID, "d1-boundary", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"incidentId":"` + j.IncidentID.String() + `"}`)
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_findings", ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536, EvidenceItems: 100}}
	if _, err = repo.BeginCall(ctx, l, call); err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(map[string]any{"evidenceRefs": []string{ev.EvidenceID}})
	if err = repo.CompleteStep(ctx, l, call.StepID, result, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(result)), EvidenceItems: 1}); err != nil {
		t.Fatal(err)
	}
	reserve := investigation.Usage{ModelRequests: 1, InputTokens: 16384, OutputTokens: 1024, ResultBytes: 65536}
	modelArgs := json.RawMessage(`{"requestDigest":"sha256:` + strings.Repeat("d", 64) + `"}`)
	model, err := repo.AllocateModel(ctx, l, reserve, modelArgs)
	if err != nil {
		t.Fatal(err)
	}
	response := json.RawMessage(`{"provider":"openai-compatible","usageKnown":true,"response":{"choices":[{"message":{"content":"D1 derived response"}}]}}`)
	used := investigation.Usage{ModelRequests: 1, InputTokens: 100, OutputTokens: 10, ResultBytes: int64(len(response))}
	if err = repo.SettleModel(ctx, l, model.StepID, response, &used, ""); err != nil {
		t.Fatal(err)
	}
	ca, _ := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), Token: os.Getenv("SP03_TEST_OPENBAO_TOKEN"), CACertBundle: ca})
	if err != nil {
		t.Fatal(err)
	}
	if err = bao.ConfigureTransit(ctx); err != nil {
		t.Fatal(err)
	}
	signer := investigation.ContextSigner{Transit: bao, Key: "investigation-signing"}
	files := sp06Certificates(t, t.TempDir())
	ca, _ = os.ReadFile(files["ca.pem"])
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	raw, _ := os.ReadFile(files["crl.pem"])
	block, _ := pem.Decode(raw)
	crl, _ := x509.ParseRevocationList(block.Bytes)
	identity, _ := auth.NewWorkloadIdentity("sp06-test", "ops-investigator")
	trust := auth.WorkloadTrust{Roots: roots, Allowed: []auth.WorkloadIdentity{identity}, RevocationList: crl, CRLFetchedAt: time.Now()}
	authorize := func(context.Context, investigation.Job, string, json.RawMessage) error { return nil }
	h := &httpapi.InvestigationHandlers{Repository: repo, Signer: signer, Trust: trust, Authorize: authorize}
	activeRef := ev.EvidenceID
	mcp, err := (gateway.Gateway{Repository: repo, Signer: signer, Trust: trust, API: sp06D1Fixture{&activeRef}, Authorize: authorize}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp)
	mux.Handle("/internal/", h.InternalHandler())
	server := httptest.NewUnstartedServer(mux)
	cert, _ := tls.LoadX509KeyPair(files["ops-api.pem"], files["ops-api.key"])
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	cert, _ = tls.LoadX509KeyPair(files["ops-investigator.pem"], files["ops-investigator.key"])
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: "ops-api.sp06-test.svc.cluster.local"}}
	defer transport.CloseIdleConnections()
	narrow := j
	narrow.Budget.AllowedDataClasses = []string{"D0"}
	jobToken, err := signer.IssueRegistered(ctx, repo, narrow, l, "platform-job-api", identity.URI, tools.Names(defs))
	if err != nil {
		t.Fatal(err)
	}
	// Admission restrictions survive a later settlement callback that lacks
	// the HTTP request context. The original Job ceiling must not restore D1.
	claims, err := signer.VerifyContext(ctx, jobToken, "platform-job-api", identity.URI)
	if err != nil {
		t.Fatal(err)
	}
	allocationProof, err := repo.AllocateTool(ctx, l, "get_ranked_evidence", args)
	if err != nil {
		t.Fatal(err)
	}
	admitted := investigation.Call{Claims: &claims, StepID: allocationProof.StepID, ContextID: claims.ContextID, JTI: allocationProof.JTI, Name: "get_ranked_evidence", ArgsDigest: allocationProof.ArgsDigest, Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536, EvidenceItems: 100}}
	if _, err = repo.BeginCall(ctx, l, admitted); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteStep(ctx, l, admitted.StepID, result, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(result)), EvidenceItems: 1}); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("settlement restored D1 from Job: %v", err)
	}
	if err = repo.FailStep(ctx, l, admitted.StepID, "RESULT_REJECTED"); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, suffix := range []string{"/steps", "/steps/" + call.StepID.String() + "/result?argsDigest=" + call.ArgsDigest, "/calls:allocate"} {
		t.Run(suffix, func(t *testing.T) {
			method, body := "GET", ""
			if suffix == "/calls:allocate" {
				method = "POST"
				b, _ := json.Marshal(map[string]any{"name": "model", "model": true, "reserve": reserve, "arguments": modelArgs})
				body = string(b)
			}
			r, _ := http.NewRequestWithContext(ctx, method, server.URL+"/internal/v1/investigations/"+j.JobID.String()+suffix, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+jobToken)
			res, err := client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != 403 || strings.Contains(string(b), ev.EvidenceID) || strings.Contains(string(b), "D1 derived response") {
				t.Fatalf("D0 Context leaked D1: status=%d", res.StatusCode)
			}
		})
	}
	mcpToken, err := signer.IssueRegistered(ctx, repo, narrow, l, "platform-mcp-gateway", identity.URI, tools.Names(defs))
	if err != nil {
		t.Fatal(err)
	}
	sc := sdk.NewClient(&sdk.Implementation{Name: "d0-boundary", Version: "1"}, nil)
	session, err := sc.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: sp06BearerTransport{transport, mcpToken}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	allocation, err := repo.AllocateTool(ctx, l, "get_incident_context", args)
	if err != nil {
		t.Fatal(err)
	}
	params := &sdk.CallToolParams{Name: "get_incident_context", Arguments: map[string]string{"incidentId": j.IncidentID.String()}}
	params.SetMeta(map[string]any{"ops/stepId": allocation.StepID.String(), "ops/jti": allocation.JTI})
	out, err := session.CallTool(ctx, params)
	if err != nil || !out.IsError {
		t.Fatalf("D0 MCP committed D1: %v", err)
	}
	var succeeded int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE step_id=$1 AND state='succeeded'`, allocation.StepID).Scan(&succeeded); err != nil || succeeded != 0 {
		t.Fatal("D1 Step committed under D0 Context")
	}

	d0 := ev
	d0.EvidenceID = uuid.NewString()
	d0.DataClass = "D0"
	if err = archives.Capture(ctx, d0, "", now.Add(181*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	activeRef = d0.EvidenceID
	d0args, _ := json.Marshal(map[string]any{"canonicalId": canonical, "depth": 1})
	d0allocation, err := repo.AllocateTool(ctx, l, "get_resource_context", d0args)
	if err != nil {
		t.Fatal(err)
	}
	d0params := &sdk.CallToolParams{Name: "get_resource_context", Arguments: map[string]any{"canonicalId": canonical, "depth": 1}}
	d0params.SetMeta(map[string]any{"ops/stepId": d0allocation.StepID.String(), "ops/jti": d0allocation.JTI})
	d0out, err := session.CallTool(ctx, d0params)
	if err != nil || d0out.IsError {
		t.Fatalf("legitimate D0 MCP denied: %v output=%+v", err, d0out)
	}
	recovered, err := repo.RecoverStep(investigation.WithDataClasses(ctx, []string{"D0"}), l, d0allocation.StepID, d0allocation.ArgsDigest)
	if err != nil || !strings.Contains(string(recovered), d0.EvidenceID) {
		t.Fatal("legitimate D0 recovery denied")
	}
}

func TestSP06RecoveryReadsChargeBytesAndExhaust(t *testing.T) {
	ctx, _, repo, req := sp06JobFixture(t)
	req.Budget.ResultBytes = 128
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	l, err := repo.Claim(ctx, j.TenantID, "read-budget", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_findings", ArgsDigest: "sha256:read-budget", Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 128}}
	if _, err = repo.BeginCall(ctx, l, call); err != nil {
		t.Fatal(err)
	}
	result := []byte(`{"evidenceRefs":[]}`)
	if err = repo.CompleteStep(ctx, l, call.StepID, result, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(result))}); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.Get(ctx, j.TenantID, j.JobID)
	recovered, err := repo.RecoverStep(ctx, l, call.StepID, call.ArgsDigest)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := repo.Get(ctx, j.TenantID, j.JobID)
	if after.Consumed.ResultBytes != before.Consumed.ResultBytes+int64(len(recovered)) || after.Consumed.ToolCalls != before.Consumed.ToolCalls {
		t.Fatal("cached tool read bypassed result byte ledger")
	}
	if _, err = repo.Steps(ctx, l); !errors.Is(err, investigation.ErrBudget) {
		t.Fatalf("internal steps did not exhaust bounded result bytes: %v", err)
	}
	final, _ := repo.Get(ctx, j.TenantID, j.JobID)
	if final.State != "partial" || final.ErrorCode != "BUDGET_EXHAUSTED" || final.Reserved != (investigation.Usage{}) {
		t.Fatal("read budget exhaustion was not durable")
	}
}

func TestSP06CachedModelReadChargesOnlyOutputBytes(t *testing.T) {
	ctx, _, repo, req := sp06JobFixture(t)
	req.Budget.AllowedDataClasses = []string{"D0"}
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	l, err := repo.Claim(ctx, j.TenantID, "cached-model", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reserve := investigation.Usage{ModelRequests: 1, InputTokens: 16384, OutputTokens: 1024, ResultBytes: 65536}
	args := json.RawMessage(`{"requestDigest":"sha256:` + strings.Repeat("e", 64) + `"}`)
	a, err := repo.AllocateModel(ctx, l, reserve, args)
	if err != nil {
		t.Fatal(err)
	}
	b := json.RawMessage(`{"provider":"openai-compatible","usageKnown":true,"response":{"choices":[{"message":{"content":"stored"}}]}}`)
	used := investigation.Usage{ModelRequests: 1, InputTokens: 100, OutputTokens: 10, ResultBytes: int64(len(b))}
	if err = repo.SettleModel(ctx, l, a.StepID, b, &used, ""); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.Get(ctx, j.TenantID, j.JobID)
	a, err = repo.AllocateModel(ctx, l, reserve, args)
	if err != nil || !a.Committed {
		t.Fatal(err)
	}
	after, _ := repo.Get(ctx, j.TenantID, j.JobID)
	if after.Consumed.ResultBytes != before.Consumed.ResultBytes+int64(len(a.Result)) || after.Consumed.ModelRequests != before.Consumed.ModelRequests || after.Consumed.InputTokens != before.Consumed.InputTokens {
		t.Fatal("cached model read bypassed byte budget or repeated model charge")
	}
}

type sp06BatchWriter struct {
	*httptest.ResponseRecorder
	afterFirst func()
	writes     int
}

func (w *sp06BatchWriter) Flush() {
	w.ResponseRecorder.Flush()
	w.writes++
	if w.writes == 1 {
		w.afterFirst()
	}
}
func TestSP06SSEBatchRechecksExpiryAndWithdrawal(t *testing.T) {
	for _, target := range []string{"expiry", "role", "source"} {
		t.Run(target, func(t *testing.T) {
			ctx, db, repo, req := sp06JobFixture(t)
			j, err := repo.CreateJob(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			l, err := repo.Claim(ctx, j.TenantID, "batch-stream", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			for range 5 {
				if err = repo.RecordDenial(ctx, l, "get_findings", "DENIED"); err != nil {
					t.Fatal(err)
				}
			}
			if err = repo.Stop(ctx, j.TenantID, j.JobID, j.Subject, false); err != nil {
				t.Fatal(err)
			}
			_, key, _ := ed25519.GenerateKey(rand.Reader)
			h := &httpapi.InvestigationHandlers{Repository: repo, CursorKey: key}
			actor := auth.RequestContext{TenantID: j.TenantID, Subject: j.Subject, Roles: []auth.Role{auth.Operator}, TokenExpiresAt: time.Now().Add(time.Minute)}
			if target == "expiry" {
				actor.TokenExpiresAt = time.Now().Add(100 * time.Millisecond)
			}
			w := &sp06BatchWriter{ResponseRecorder: httptest.NewRecorder(), afterFirst: func() {
				switch target {
				case "expiry":
					time.Sleep(120 * time.Millisecond)
				case "role":
					_, err = db.ExecContext(ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2`, j.TenantID, j.Subject)
				case "source":
					_, err = db.ExecContext(ctx, `UPDATE platform.source_registrations SET status='disabled',revision=revision+1 WHERE tenant_id=$1`, j.TenantID)
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			r := httptest.NewRequest("GET", "/api/v1/investigations/"+j.JobID.String()+"/events", nil)
			h.ServeHTTP(w, r.WithContext(auth.WithRequestContext(ctx, actor)))
			if w.writes != 1 {
				t.Fatalf("batch leaked events after %s: %d", target, w.writes)
			}
		})
	}
}

func TestSP06CancelIdempotencyBindsRequest(t *testing.T) {
	ctx, db, repo, req := sp06JobFixture(t)
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.TriggerKind = "new_evidence"
	other, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	h := &httpapi.InvestigationHandlers{Repository: repo, Budget: req.Budget, PolicyVersion: func(context.Context, auth.RequestContext, string) (string, error) { return req.PolicyVersion, nil }}
	actor := auth.RequestContext{TenantID: j.TenantID, Subject: j.Subject, Roles: []auth.Role{auth.Operator}}
	create := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/incidents/"+j.IncidentID.String()+"/investigations", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", "create-contract-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(auth.WithRequestContext(ctx, actor)))
		return w
	}
	base, _ := json.Marshal(map[string]any{"triggerRevision": req.TriggerRevision})
	if w := create(string(base)); w.Code != 202 {
		t.Fatalf("create contract %d %s", w.Code, w.Body.String())
	}
	if w := create(string(base)); w.Code != 202 {
		t.Fatalf("create replay %d", w.Code)
	}
	changed, _ := json.Marshal(map[string]any{"triggerRevision": req.TriggerRevision, "triggerKind": "manual"})
	if w := create(string(changed)); w.Code != 409 || !strings.Contains(w.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("create conflict %d %s", w.Code, w.Body.String())
	}
	cancel := func(id uuid.UUID) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/investigations/"+id.String()+":cancel", strings.NewReader(`{}`))
		r.Header.Set("Idempotency-Key", "same-cancel-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(auth.WithRequestContext(ctx, actor)))
		return w
	}
	if w := cancel(j.JobID); w.Code != 200 {
		t.Fatalf("first cancel %d", w.Code)
	}
	if w := cancel(j.JobID); w.Code != 200 {
		t.Fatalf("repeat cancel %d", w.Code)
	}
	if w := cancel(other.JobID); w.Code != 409 || !strings.Contains(w.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("cancel key rebound: %d %s", w.Code, w.Body.String())
	}
	final, _ := repo.Get(ctx, other.TenantID, other.JobID)
	if final.State != "queued" {
		t.Fatal("conflicting request cancelled second job")
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2`, j.TenantID, j.Subject); err != nil {
		t.Fatal(err)
	}
	if w := cancel(j.JobID); w.Code != 403 {
		t.Fatalf("revoked cancel replay returned %d", w.Code)
	}
}

func TestSP06ConcurrentRecoveryReadsAtomicBudget(t *testing.T) {
	ctx, _, repo, req := sp06JobFixture(t)
	req.Budget.ResultBytes = 80
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	l, err := repo.Claim(ctx, j.TenantID, "concurrent-reads", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_findings", ArgsDigest: "sha256:concurrent-read", Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 64}}
	if _, err = repo.BeginCall(ctx, l, c); err != nil {
		t.Fatal(err)
	}
	b := []byte(`{"evidenceRefs":[]}`)
	if err = repo.CompleteStep(ctx, l, c.StepID, b, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(b))}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	result := make(chan error, 20)
	for range 20 {
		wg.Go(func() { _, err := repo.RecoverStep(ctx, l, c.StepID, c.ArgsDigest); result <- err })
	}
	wg.Wait()
	close(result)
	successful := 0
	for err := range result {
		if err == nil {
			successful++
		} else if !errors.Is(err, investigation.ErrBudget) && !errors.Is(err, investigation.ErrLease) {
			t.Fatal(err)
		}
	}
	final, err := repo.Get(ctx, j.TenantID, j.JobID)
	if err != nil || successful < 1 || successful >= 20 || final.Consumed.ToolCalls != 1 || final.Consumed.ResultBytes > 80 || final.Reserved != (investigation.Usage{}) || final.State != "partial" || final.ErrorCode != "BUDGET_EXHAUSTED" {
		t.Fatalf("atomic recovery limit: success=%d consumed=%+v state=%s err=%v", successful, final.Consumed, final.State, err)
	}
}
