package integration

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ops-platform/internal/app"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/finding"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/incident"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/investigation"
	"ops-platform/internal/policy"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sp06Port(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}
func sp06Certificates(t *testing.T, dir string) map[string]string {
	t.Helper()
	now := time.Now()
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "SP06 owned test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	files := map[string]string{}
	write := func(name, kind string, b []byte) {
		p := filepath.Join(dir, name)
		if os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: b}), 0600) != nil {
			t.Fatal("write certificate")
		}
		files[name] = p
	}
	write("ca.pem", "CERTIFICATE", der)
	crl, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(1), ThisUpdate: now.Add(-time.Second), NextUpdate: now.Add(time.Hour)}, ca, private)
	if err != nil {
		t.Fatal(err)
	}
	write("crl.pem", "X509 CRL", crl)
	for n, name := range []string{"ops-api", "ops-worker", "ops-investigator"} {
		identity, _ := auth.NewWorkloadIdentity("sp06-test", name)
		uri, _ := url.Parse(identity.URI)
		pub, key, _ := ed25519.GenerateKey(rand.Reader)
		cert, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(int64(n + 2)), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{identity.DNSName}, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}, ca, pub, private)
		if err != nil {
			t.Fatal(err)
		}
		write(name+".pem", "CERTIFICATE", cert)
		keyBytes, _ := x509.MarshalPKCS8PrivateKey(key)
		write(name+".key", "PRIVATE KEY", keyBytes)
	}
	return files
}
func TestSP06RealHolmesModelMCPJobAndSSE(t *testing.T) {
	if os.Getenv("SP06_REAL_CHAIN") != "1" {
		t.Fatal("SP06_REAL_CHAIN=1 and actual isolated dependencies are required")
	}
	root, _ := filepath.Abs("../..")
	ctx, db, workerPool, b := sp05Database(t)
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, workerPool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	fs := finding.Service{Pool: workerPool}
	f, _, err := fs.Ingest(ctx, b, sp05Envelope(b, "sp06-real-event", "sp06-real-occurrence"))
	if err != nil {
		t.Fatal(err)
	}
	if fs.RelayPass(ctx, b.TenantID, incident.Consume, 50) != nil {
		t.Fatal("incident relay")
	}
	var incidentID uuid.UUID
	if db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE finding_id=$1`, f.FindingID).Scan(&incidentID) != nil {
		t.Fatal("incident missing")
	}
	token := sp04KeycloakToken(t, ctx, b.TenantID)
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes) VALUES($1,$2,$3,'operator',$4),($1,$5,'sp06-admin','platform_admin','[]')`, b.TenantID, uuid.New(), token.Subject, `["`+b.ClusterID.String()+`"]`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	trust := configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{"integration-key": pub}}
	compiler, _ := policy.NewBundleCompiler(trust)
	registry, _ := configregistry.NewService(apiPool, trust, compiler)
	actor := auth.RequestContext{TenantID: b.TenantID, Subject: "sp06-admin", Roles: []auth.Role{auth.PlatformAdmin}}
	draft, err := createRegistryDraft(ctx, apiPool, registry, actor, configregistry.DraftCommand{Kind: configregistry.KindPolicy, LogicalName: "sp06-read-only", Content: policyRegistryContent(t, "sp06-read-only", "sp06")})
	if err != nil {
		t.Fatal(err)
	}
	version, err := signAndPublish(ctx, apiPool, registry, actor, draft, 1, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = activateRegistryVersion(ctx, apiPool, registry, actor, version, configregistry.KindPolicy, "sp06-read-only", configregistry.Scope{Type: configregistry.ScopeTenant}, 0); err != nil {
		t.Fatal(err)
	}
	ca, _ := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
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
	keyPath := filepath.Join(dir, "bao.token")
	os.WriteFile(keyPath, []byte(os.Getenv("SP03_TEST_OPENBAO_TOKEN")), 0600)
	t.Setenv("OPENBAO_ADDR", os.Getenv("SP03_TEST_OPENBAO_URL"))
	t.Setenv("OPENBAO_CA_FILE", os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	t.Setenv("OPENBAO_INVOCATION_TOKEN_FILE", keyPath)
	apiAddress, investigatorAddress := sp06Port(t), sp06Port(t)
	c := app.SP06Config{Namespace: "sp06-test", ListenAddress: apiAddress, InvestigatorEndpoint: "https://" + investigatorAddress, CertificateFile: files["ops-api.pem"], PrivateKeyFile: files["ops-api.key"], CAFile: files["ca.pem"], CRLFile: files["crl.pem"], PolicyName: "sp06-read-only", Tenants: []uuid.UUID{b.TenantID}, Budget: investigation.DefaultBudget()}
	configPath := filepath.Join(dir, "api.json")
	raw, _ := json.Marshal(c)
	os.WriteFile(configPath, raw, 0600)
	t.Setenv("SP06_RUNTIME_FILE", configPath)
	_, cursorKey, _ := ed25519.GenerateKey(rand.Reader)
	sp04 := &httpapi.SP04Handlers{Pool: apiPool, SigningKey: cursorKey, SP05: &httpapi.SP05Handlers{Pool: apiPool, Enabled: true}}
	sp04.SP05.GraphAPI = sp04
	stop, err := app.StartSP06API(ctx, apiPool, sp04, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	roles, _ := auth.NewPostgreSQLRoleBindingSource(apiPool)
	authenticator, err := auth.NewOIDCAuthenticator(ctx, os.Getenv("SP03_KEYCLOAK_TEST_ISSUER"), "ops-api", roles)
	if err != nil {
		t.Fatal(err)
	}
	publicServer := httptest.NewServer(authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/capabilities" {
			sp04.ServeCapabilities(w, r)
		} else if strings.Contains(r.URL.Path, "/investigations") {
			sp04.Investigation.ServeHTTP(w, r)
		} else {
			sp04.SP05.ServeHTTP(w, r)
		}
	})))
	defer publicServer.Close()
	config := map[string]any{"listenAddress": investigatorAddress, "jobAPI": "https://" + apiAddress, "mcpEndpoint": "https://" + apiAddress + "/mcp", "apiServerName": "ops-api.sp06-test.svc.cluster.local", "caFile": files["ca.pem"], "crlFile": files["crl.pem"], "certificateFile": files["ops-investigator.pem"], "privateKeyFile": files["ops-investigator.key"], "workerIdentity": "spiffe://ops.local/ns/sp06-test/sa/ops-worker", "modelAPIKeyFile": filepath.Join(dir, "model.key"), "model": map[string]any{"base_url": "http://127.0.0.1:11434/v1", "api_key_ref": "secret://model/api-key", "model": "llama3.1:8b-16k", "timeout": 45, "token_budget": 1024}}
	fault := os.Getenv("SP06_MODEL_FAULT")
	var providerRequests atomic.Int32
	if fault != "" {
		// Fault injection at the actual upstream OpenAI provider HTTP boundary;
		// this supplements, and never counts as, the real Ollama model gate.
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			providerRequests.Add(1)
			input, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if strings.Contains(string(input), "jobContext") || strings.Contains(string(input), "mcpContext") {
				t.Error("Context entered provider parameters")
			}
			w.Header().Set("Content-Type", "application/json")
			switch fault {
			case "429":
				w.WriteHeader(429)
				_, _ = w.Write([]byte(`{"error":{"message":"owned rate-limit fault","type":"rate_limit_error"}}`))
			case "timeout":
				time.Sleep(2 * time.Second)
				_, _ = w.Write([]byte(`{}`))
			case "unavailable":
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":{"message":"owned unavailable fault"}}`))
			case "illegal-json":
				_, _ = w.Write([]byte(`{"id":"owned","object":"chat.completion","created":1,"model":"llama3.1:8b-16k","choices":[{"index":0,"message":{"role":"assistant","content":"not valid result JSON"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`))
			case "write-tool":
				_, _ = w.Write([]byte(`{"id":"owned","object":"chat.completion","created":1,"model":"llama3.1:8b-16k","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"attack","type":"function","function":{"name":"execute_command","arguments":"{\"command\":\"id\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`))
			default:
				t.Error("unknown fault fixture")
				w.WriteHeader(500)
			}
		}))
		defer provider.Close()
		model := config["model"].(map[string]any)
		model["base_url"] = provider.URL + "/v1"
		model["timeout"] = 1
	}
	os.WriteFile(filepath.Join(dir, "model.key"), []byte("local-ollama-no-auth"), 0600)
	runtimePath := filepath.Join(dir, "investigator.json")
	raw, _ = json.Marshal(config)
	os.WriteFile(runtimePath, raw, 0600)
	process := exec.CommandContext(ctx, filepath.Join(root, "services/investigator/.venv/bin/python"), "-m", "investigator.worker")
	process.Env = append(os.Environ(), "SP06_INVESTIGATOR_FILE="+runtimePath, "PYTHONPATH="+filepath.Join(root, "services/investigator/src"))
	var output bytes.Buffer
	process.Stdout = &output
	process.Stderr = &output
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Process.Kill()
		_ = process.Wait()
		t.Log("investigator diagnostic", output.String())
	}()
	for n := 0; n < 100; n++ {
		conn, e := net.DialTimeout("tcp", investigatorAddress, 100*time.Millisecond)
		if e == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	request, _ := http.NewRequestWithContext(ctx, "POST", publicServer.URL+"/api/v1/incidents/"+incidentID.String()+"/investigations", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	request.Header.Set("Idempotency-Key", "sp06-real-chain")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 202 {
		t.Fatalf("create HTTP %d %s", response.StatusCode, body)
	}
	var envelope struct {
		Data struct {
			JobID uuid.UUID `json:"jobId"`
		}
	}
	if json.Unmarshal(body, &envelope) != nil {
		t.Fatal("job response")
	}
	jobID := envelope.Data.JobID
	jobIDs := []uuid.UUID{jobID}
	if os.Getenv("SP06_CONCURRENT_BATCH") == "1" {
		for n := 1; n < 10; n++ {
			e := sp05Envelope(b, fmt.Sprintf("sp06-concurrent-event-%d", n), "sp06-real-occurrence")
			e.SourceSequence = int64(n + 1)
			e.ObservedAt = e.ObservedAt.Add(time.Duration(n) * time.Second)
			if _, _, err = fs.Ingest(ctx, b, e); err != nil {
				t.Fatal(err)
			}
			if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
				t.Fatal(err)
			}
			r, _ := http.NewRequestWithContext(ctx, "POST", publicServer.URL+"/api/v1/incidents/"+incidentID.String()+"/investigations", strings.NewReader(`{}`))
			r.Header.Set("Authorization", "Bearer "+token.AccessToken)
			r.Header.Set("Idempotency-Key", fmt.Sprintf("sp06-concurrent-%d", n))
			res, requestErr := http.DefaultClient.Do(r)
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != 202 {
				t.Fatalf("concurrent create %d", res.StatusCode)
			}
			if json.Unmarshal(body, &envelope) != nil {
				t.Fatal("concurrent Job Contract")
			}
			for _, known := range jobIDs {
				if known == envelope.Data.JobID {
					t.Fatal("new Incident revision collapsed into old Job")
				}
			}
			jobIDs = append(jobIDs, envelope.Data.JobID)
		}
	}
	c.CertificateFile, c.PrivateKeyFile = files["ops-worker.pem"], files["ops-worker.key"]
	raw, _ = json.Marshal(c)
	os.WriteFile(configPath, raw, 0600)
	stopWorker := func() {}
	if os.Getenv("SP06_PROCESS_RECOVERY") != "1" {
		stopWorker, err = app.StartSP06Worker(ctx, workerPool)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer stopWorker()
	if os.Getenv("SP06_PROCESS_RECOVERY") == "1" {
		stopWorker()
		binary := filepath.Join(dir, "dispatcher")
		build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", binary, "./test/tools/sp06-dispatcher-process")
		build.Dir = root
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("production dispatcher process build: %v %s", err, output)
		}
		databasePath := filepath.Join(dir, "worker.database")
		if os.WriteFile(databasePath, []byte(workerPool.Config().ConnConfig.ConnString()), 0600) != nil {
			t.Fatal("private Worker credentials")
		}
		spawn := func() *exec.Cmd {
			child := exec.CommandContext(ctx, binary)
			child.Env = append(os.Environ(), "SP06_DATABASE_URL_FILE="+databasePath)
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			return child
		}
		child := spawn()
		// Observe a committed successful tool and a subsequent in-flight model.
		// Both OS processes are then killed with no graceful ledger settlement.
		var committed uuid.UUID
		interrupted := false
		for n := 0; n < 600; n++ {
			var pending int
			_ = db.QueryRowContext(ctx, `SELECT step_id FROM investigation.steps WHERE job_id=$1 AND tool_name<>'model' AND state='succeeded' ORDER BY started_at LIMIT 1`, jobID).Scan(&committed)
			_ = db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.admissions a JOIN investigation.steps s USING(tenant_id,job_id,step_id) WHERE a.job_id=$1 AND a.state='reserved' AND s.tool_name='model'`, jobID).Scan(&pending)
			if committed != uuid.Nil && pending > 0 {
				interrupted = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if committed == uuid.Nil || !interrupted {
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatal("SIGKILL case did not reach committed tool")
		}
		_ = child.Process.Kill()
		_ = child.Wait()
		_ = process.Process.Kill()
		_ = process.Wait()
		process = exec.CommandContext(ctx, filepath.Join(root, "services/investigator/.venv/bin/python"), "-m", "investigator.worker")
		process.Env = append(os.Environ(), "SP06_INVESTIGATOR_FILE="+runtimePath, "PYTHONPATH="+filepath.Join(root, "services/investigator/src"))
		process.Stdout = &output
		process.Stderr = &output
		if err = process.Start(); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 100; n++ {
			conn, e := net.DialTimeout("tcp", investigatorAddress, 100*time.Millisecond)
			if e == nil {
				conn.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		// Use the real 45-second lease, rather than altering the tested expiry.
		child = spawn()
		defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
		for n := 0; n < 150; n++ {
			var state string
			_ = db.QueryRowContext(ctx, `SELECT state FROM investigation.jobs WHERE job_id=$1`, jobID).Scan(&state)
			if state != "queued" && state != "running" {
				break
			}
			time.Sleep(time.Second)
		}
		j, err := (investigation.Repository{Pool: workerPool}).Get(ctx, b.TenantID, jobID)
		if err != nil || j.State == "queued" || j.State == "running" || j.Reserved != (investigation.Usage{}) || !j.Consumed.Within(j.Budget.Usage) {
			t.Fatalf("SIGKILL terminal recovery: %+v %v", j, err)
		}
		var generation int64
		var successful, unknown int
		if db.QueryRowContext(ctx, `SELECT fencing_epoch FROM investigation.worker_queue WHERE job_id=$1`, jobID).Scan(&generation) != nil || generation < 2 {
			t.Fatal("OS restart did not take over expired lease")
		}
		if db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE job_id=$1 AND tool_name<>'model' AND state='succeeded'`, jobID).Scan(&successful) != nil || successful != 1 {
			t.Fatalf("committed successful tool repeated: %d", successful)
		}
		if db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE job_id=$1 AND error_code='UNKNOWN_OUTCOME'`, jobID).Scan(&unknown) != nil || unknown < 1 {
			t.Fatal("unknown interrupted model lost")
		}
		t.Logf("SIGKILL of production Worker dispatcher and resident investigator; actual lease takeover generation=%d committedTool=%s successfulTools=%d unknown=%d terminal=%s", generation, committed, successful, unknown, j.State)
		return
	}
	if len(jobIDs) == 10 {
		peakRunning := 0
		for n := 0; n < 120; n++ {
			var running, ended int
			if db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state='running'),count(*) FILTER(WHERE state NOT IN('running','queued')) FROM investigation.jobs WHERE tenant_id=$1`, b.TenantID).Scan(&running, &ended) != nil {
				t.Fatal("concurrent state")
			}
			if running > peakRunning {
				peakRunning = running
			}
			e := sp05Envelope(b, fmt.Sprintf("mainline-during-investigations-%d", n), "sp06-mainline-occurrence")
			e.SourceSequence = int64(n + 1)
			e.ObservedAt = e.ObservedAt.Add(time.Duration(n) * time.Second)
			if _, _, err = fs.Ingest(ctx, b, e); err != nil {
				t.Fatal("Finding mainline failed", err)
			}
			if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
				t.Fatal("Incident mainline failed", err)
			}
			r, _ := http.NewRequestWithContext(ctx, "GET", publicServer.URL+"/api/v1/incidents/"+incidentID.String(), nil)
			r.Header.Set("Authorization", "Bearer "+token.AccessToken)
			res, requestErr := http.DefaultClient.Do(r)
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatalf("mainline HTTP %d", res.StatusCode)
			}
			if ended == 10 {
				break
			}
			time.Sleep(time.Second)
		}
		if peakRunning != 10 {
			t.Fatalf("10 concurrent correctness case was not exercised: running=%d", peakRunning)
		}
		for _, id := range jobIDs {
			j, err := (investigation.Repository{Pool: workerPool}).Get(ctx, b.TenantID, id)
			if err != nil || j.State == "queued" || j.State == "running" || j.Consumed.ModelRequests < 1 || j.Reserved != (investigation.Usage{}) || !j.Consumed.Within(j.Budget.Usage) {
				t.Fatalf("concurrent terminal/budget: %+v %v", j, err)
			}
		}
		t.Log("10 simultaneous real investigations with ongoing Finding/Incident API and durable bounded settlement; no performance/accuracy gate")
		return
	}
	var state string
	for n := 0; n < 150; n++ {
		if db.QueryRowContext(ctx, `SELECT state FROM investigation.jobs WHERE job_id=$1`, jobID).Scan(&state) != nil {
			t.Fatal("job lost")
		}
		if state != "queued" && state != "running" {
			break
		}
		time.Sleep(time.Second)
	}
	if fault != "" {
		j, err := (investigation.Repository{Pool: workerPool}).Get(ctx, b.TenantID, jobID)
		if err != nil || (j.State != "failed" && j.State != "partial") || j.Reserved != (investigation.Usage{}) || !j.Consumed.Within(j.Budget.Usage) || j.Consumed.ModelRequests < 1 {
			t.Fatalf("provider failure did not fence/settle: %+v %v", j, err)
		}
		if fault == "429" || fault == "timeout" || fault == "unavailable" {
			if providerRequests.Load() != 1 || j.Consumed.InputTokens != 16384 || j.Consumed.OutputTokens != 1024 {
				t.Fatalf("provider retry/unknown budget: requests=%d usage=%+v", providerRequests.Load(), j.Consumed)
			}
		}
		r, _ := http.NewRequestWithContext(ctx, "GET", publicServer.URL+"/api/v1/incidents/"+incidentID.String(), nil)
		r.Header.Set("Authorization", "Bearer "+token.AccessToken)
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal("model failure damaged mainline")
		}
		capRequest, _ := http.NewRequestWithContext(ctx, "GET", publicServer.URL+"/api/v1/capabilities", nil)
		capRequest.Header.Set("Authorization", "Bearer "+token.AccessToken)
		capResponse, capErr := http.DefaultClient.Do(capRequest)
		if capErr != nil {
			t.Fatal(capErr)
		}
		capBody, _ := io.ReadAll(capResponse.Body)
		capResponse.Body.Close()
		var cap struct {
			Data struct {
				APIReady      bool `json:"apiReady"`
				Investigation struct {
					Status string `json:"status"`
				} `json:"investigation"`
			} `json:"data"`
		}
		if capResponse.StatusCode != 200 || json.Unmarshal(capBody, &cap) != nil || !cap.Data.APIReady || cap.Data.Investigation.Status != "degraded" {
			t.Fatalf("model failure capability honesty: %d %s", capResponse.StatusCode, capBody)
		}
		var writes int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE job_id=$1 AND tool_name IN('execute_command','ssh','sql','fetch_url')`, jobID).Scan(&writes); err != nil || writes != 0 {
			t.Fatal("write tool was admitted")
		}
		t.Logf("actual provider HTTP fault=%s state=%s remoteRequests=%d; conservative ledger and live Incident API preserved; fixture is not the real-model acceptance", fault, j.State, providerRequests.Load())
		return
	}
	if state != "succeeded" && state != "partial" {
		var code string
		db.QueryRowContext(ctx, `SELECT error_code FROM investigation.jobs WHERE job_id=$1`, jobID).Scan(&code)
		t.Fatalf("real chain state=%s errorCode=%s", state, code)
	}
	var toolSteps, modelSteps, audits int
	if db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE tool_name<>'model'),count(*) FILTER(WHERE tool_name='model') FROM investigation.steps WHERE job_id=$1 AND state='succeeded'`, jobID).Scan(&toolSteps, &modelSteps) != nil || toolSteps < 1 || modelSteps < 1 {
		t.Fatalf("actual loop closure tool=%d model=%d", toolSteps, modelSteps)
	}
	db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE entity_id=$1`, jobID).Scan(&audits)
	if audits < 5 {
		t.Fatal("audit missing")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", publicServer.URL+"/api/v1/investigations/"+jobID.String()+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	sse, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(sse), "job.completed") {
		t.Fatalf("SSE %d", resp.StatusCode)
	}
	lines := strings.Split(string(sse), "\n")
	firstCursor := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "id: ") {
			firstCursor = strings.TrimPrefix(line, "id: ")
			break
		}
	}
	if firstCursor == "" {
		t.Fatal("persistent cursor missing")
	}
	stop()
	c.CertificateFile, c.PrivateKeyFile = files["ops-api.pem"], files["ops-api.key"]
	raw, _ = json.Marshal(c)
	os.WriteFile(configPath, raw, 0600)
	stopAPI, err := app.StartSP06API(ctx, apiPool, sp04, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer stopAPI()
	resumedRequest, _ := http.NewRequestWithContext(ctx, "GET", req.URL.String(), nil)
	resumedRequest.Header.Set("Authorization", "Bearer "+token.AccessToken)
	resumedRequest.Header.Set("Last-Event-ID", firstCursor)
	resumedResponse, err := http.DefaultClient.Do(resumedRequest)
	if err != nil {
		t.Fatal(err)
	}
	resumedBody, _ := io.ReadAll(resumedResponse.Body)
	resumedResponse.Body.Close()
	if resumedResponse.StatusCode != 200 || strings.Contains(string(resumedBody), "id: "+firstCursor+"\n") || !strings.Contains(string(resumedBody), "job.completed") {
		t.Fatal("API restart/resume replayed or lost events")
	}
	t.Logf("REAL closure job=%s tools=%d models=%d audits=%d state=%s", jobID, toolSteps, modelSteps, audits, state)
}
