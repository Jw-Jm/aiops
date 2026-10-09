package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	kube "ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"ops-platform/internal/source"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSP06ActualCurrentRCARecommendationValidator(t *testing.T) {
	sp05GoldenVertical(t, "dimm-failure", "valid", false, true)
}

// This is a freshly observed protocol fixture, not a modification to the frozen
// SP05 replay or a claim that a physical DIMM failed on the test machine.
func sp06CurrentGoldenInput(t *testing.T, input sp05GoldenInput) sp05GoldenInput {
	t.Helper()
	offset := time.Now().UTC().Add(-time.Second).Sub(input.Clock)
	b, _ := json.Marshal(input)
	var value any
	if json.Unmarshal(b, &value) != nil {
		t.Fatal("fixture decode")
	}
	var shift func(any) any
	shift = func(v any) any {
		switch x := v.(type) {
		case string:
			if parsed, err := time.Parse(time.RFC3339Nano, x); err == nil {
				return parsed.Add(offset).UTC().Format(time.RFC3339Nano)
			}
		case []any:
			for i := range x {
				x[i] = shift(x[i])
			}
		case map[string]any:
			for k, v := range x {
				x[k] = shift(v)
			}
		}
		return v
	}
	b, _ = json.Marshal(shift(value))
	if json.Unmarshal(b, &input) != nil {
		t.Fatal("shifted fixture")
	}
	return input
}

func sp06GoldenInvestigationValidator(t *testing.T, ctx context.Context, db *sql.DB, pool *pgxpool.Pool, archives *evidence.ArchiveService, trust configregistry.SignatureVerifier, g *graph.Graph, binding source.BoundSourceContext, primary string, registryKey ed25519.PrivateKey) {
	t.Helper()
	var iid uuid.UUID
	var base, revision int64
	if err := db.QueryRowContext(ctx, `SELECT incident_id,revision,current_rca_revision FROM incident.records WHERE tenant_id=$1 AND resource_canonical_id=$2`, binding.TenantID, primary).Scan(&iid, &base, &revision); err != nil {
		t.Fatal(err)
	}
	rev, err := (rca.Repository{Pool: pool}).Read(ctx, binding.TenantID, iid.String(), revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(rev.Result.Candidates) == 0 {
		t.Fatal("no deterministic candidates")
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'sp06-operator','operator',$3,$4)`, binding.TenantID, uuid.New(), `["`+binding.ClusterID.String()+`"]`, `[ {"clusterId":"`+binding.ClusterID.String()+`","namespace":"apps"} ]`); err != nil {
		t.Fatal(err)
	}
	version := sp06Policy(t, ctx, db, pool, binding.TenantID, registryKey)
	scope, err := (graph.Authorization{Pool: pool}).Effective(ctx, binding.TenantID.String(), "sp06-operator", binding.ClusterUID)
	if err != nil {
		t.Fatal(err)
	}

	// The current Graph read uses its production TLS handler and a native
	// Kubernetes Lease, not a callback returning the stored RCA revision.
	collector, ns := sp04OwnedKubernetes(t, ctx, binding.TenantID.String(), binding.SourceID.String())
	ca, _ := os.ReadFile(collector.CAFile)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	token, _ := os.ReadFile(collector.TokenFile)
	nativeTransport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer nativeTransport.CloseIdleConnections()
	kc, err := kube.NewClient(collector.Endpoint, &http.Client{Transport: nativeLeaseContractTransport{nativeTransport, strings.TrimSpace(string(token)), t}}, 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	files := sp06Certificates(t, t.TempDir())
	caPEM, _ := os.ReadFile(files["ca.pem"])
	block, _ := pem.Decode(caPEM)
	certCA, _ := x509.ParseCertificate(block.Bytes)
	crlPEM, _ := os.ReadFile(files["crl.pem"])
	block, _ = pem.Decode(crlPEM)
	crl, _ := x509.ParseRevocationList(block.Bytes)
	roots = x509.NewCertPool()
	roots.AddCert(certCA)
	apiIdentity, _ := auth.NewWorkloadIdentity("sp06-test", "ops-api")
	workerIdentity, _ := auth.NewWorkloadIdentity("sp06-test", "ops-worker")
	apiCert, err := tls.LoadX509KeyPair(files["ops-api.pem"], files["ops-api.key"])
	if err != nil {
		t.Fatal(err)
	}
	workerCert, err := tls.LoadX509KeyPair(files["ops-worker.pem"], files["ops-worker.key"])
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	lease := &graph.Lease{Client: kc, Graph: g, Mirror: graph.Repository{Pool: pool}, Namespace: ns, Name: "sp04-graph"}
	handler := graph.InternalHandler{Graph: g, Lease: lease, Key: pub, Authorization: graph.Authorization{Pool: pool}, SourceAuthorities: rev.InputManifest.GraphSources, Trust: auth.WorkloadTrust{Roots: roots, Allowed: []auth.WorkloadIdentity{apiIdentity}, RevocationList: crl, CRLFetchedAt: time.Now(), CRLIssuers: []*x509.Certificate{certCA}}}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if e := contract.Validate("https://ops.local/schemas/internal-graph-query/v1", body); e != nil {
			t.Logf("actual graph query schema failure: %v payload=%s", e, body)
		}
		handler.ServeHTTP(w, r)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{workerCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	lease.Endpoint = server.URL
	if err = lease.Tick(ctx); err != nil {
		t.Fatal("native current Graph ownership: ", err)
	}
	// A live owner renews while the full tool/validator suite runs. A single
	// Tick expires its real six-second routing mirror, even with a fixed fact
	// clock; extending that deadline would conceal the ownership requirement.
	leaseCtx, stopLease := context.WithCancel(ctx)
	leaseDone := make(chan struct{})
	leaseErrors := make(chan error, 1)
	go func() {
		defer close(leaseDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				if e := lease.Tick(leaseCtx); e != nil && leaseCtx.Err() == nil {
					select {
					case leaseErrors <- e:
					default:
					}
				}
			}
		}
	}()
	defer func() {
		stopLease()
		<-leaseDone
		select {
		case e := <-leaseErrors:
			t.Errorf("actual Graph owner renewal failed: %v", e)
		default:
		}
	}()
	graphTransport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{apiCert}, ServerName: workerIdentity.DNSName}}
	defer graphTransport.CloseIdleConnections()
	sp04 := &httpapi.SP04Handlers{Pool: pool, Client: &http.Client{Transport: graphTransport, Timeout: 4 * time.Second}, SigningKey: priv, EndpointAllowed: func(endpoint string) bool { return endpoint == server.URL }}
	sp05 := &httpapi.SP05Handlers{Pool: pool, Enabled: true, GraphAPI: sp04}
	sp04.SP05 = sp05
	repo := investigation.Repository{Pool: pool, Trust: trust, CurrentRCA: sp05.CurrentInvestigationRCA}
	defs, err := tools.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	job, err := repo.CreateJob(ctx, investigation.CreateInvestigationRequest{TenantID: binding.TenantID, IncidentID: iid, Subject: "sp06-operator", TriggerRevision: base, TriggerKind: "manual", PolicyVersion: version, Scope: scope, ToolCatalogDigest: tools.Digest(defs), Budget: investigation.DefaultBudget()})
	if err != nil {
		t.Fatal(err)
	}
	l, err := repo.Claim(ctx, job.TenantID, "current-validator", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if e := persistence.WithTenantTx(ctx, pool, job.TenantID, func(tx pgx.Tx) error {
		return rca.CheckGraphSources(ctx, tx, job.TenantID, rev.InputManifest.GraphSources, rev.InputManifest.Graph)
	}); e != nil {
		t.Fatal("current source authority: ", e)
	}
	for _, ref := range rev.InputManifest.Evidence {
		if _, e := (evidence.Repository{Pool: pool}).Get(ctx, job.TenantID, uuid.MustParse(ref.EvidenceID), scope); e != nil {
			t.Fatalf("current evidence %s: %v", ref.EvidenceID, e)
		}
	}
	for _, n := range rev.InputManifest.Graph.Nodes {
		if !scope.Allows(n.CanonicalID, n.Namespace) {
			t.Fatalf("current graph scope %s namespace %s", n.CanonicalID, n.Namespace)
		}
	}
	actual, err := sp05.CurrentInvestigationRCA(ctx, job)
	if err != nil || actual != revision {
		t.Fatalf("production current RCA gate: revision=%d err=%v", actual, err)
	}
	semantic := httpapi.InvestigationTools{SP04: sp04, SP05: sp05}
	sp06ActualSemanticMCP(t, ctx, repo, job, l, defs, semantic, trust, binding.ClusterID, rev.InputManifest.Graph.Nodes, primary)
	args, _ := json.Marshal(map[string]string{"incidentId": iid.String()})
	output, err := semantic.Invoke(ctx, job, "get_ranked_evidence", args)
	if err != nil || output.Partial || len(output.EvidenceRefs) == 0 {
		t.Fatalf("actual ranked Evidence tool: partial=%t refs=%d err=%v", output.Partial, len(output.EvidenceRefs), err)
	}
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_ranked_evidence", ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536, EvidenceItems: 100}}
	if _, err = repo.BeginCall(ctx, l, call); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(output)
	if err = repo.CompleteStep(ctx, l, call.StepID, encoded, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(encoded)), EvidenceItems: int64(len(output.EvidenceRefs))}); err != nil {
		t.Fatal(err)
	}
	p := investigation.Proposal{SchemaVersion: "investigation-result/v1", Status: "probable", Summary: "Bounded evidence supports the existing deterministic candidate; this is an advisory result.", EvidenceRefs: output.EvidenceRefs, CandidateUpdates: []investigation.CandidateSuggestion{{CandidateKey: rev.Result.Candidates[0].Key, Reason: "Independent observations support the admitted Recipe."}}, ActionPlans: []json.RawMessage{}, DegradedSources: []string{}}
	rawPlan, err := os.ReadFile("../fixtures/contracts/valid/action-plan-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	json.Unmarshal(rawPlan, &plan)
	plan["tenantId"] = job.TenantID.String()
	plan["incidentId"] = iid.String()
	plan["rcaRevision"] = revision
	plan["targetCanonicalId"] = primary
	plan["state"] = "suggested"
	plan["dataRisk"] = "D0"
	encoded, _ = json.Marshal(plan)
	p.ActionPlans = []json.RawMessage{encoded}
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, pool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal("formal API database identity: ", err)
	}
	defer apiPool.Close()
	validator := repo
	validator.Pool = apiPool
	invalid := p
	invalid.CandidateUpdates = []investigation.CandidateSuggestion{{CandidateKey: "sha256:" + strings.Repeat("f", 64), Reason: "model-ranked"}}
	encoded, _ = json.Marshal(invalid)
	if err = validator.Complete(ctx, l, encoded); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("forged candidate accepted: %v", err)
	}
	plan["executionId"] = uuid.NewString()
	encoded, _ = json.Marshal(plan)
	invalid = p
	invalid.ActionPlans = []json.RawMessage{encoded}
	encoded, _ = json.Marshal(invalid)
	if err = validator.Complete(ctx, l, encoded); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("execution handle accepted: %v", err)
	}
	delete(plan, "executionId")
	foreignTenant := uuid.NewString()
	plan["tenantId"] = foreignTenant
	plan["targetCanonicalId"] = strings.Replace(primary, job.TenantID.String(), foreignTenant, 1)
	encoded, _ = json.Marshal(plan)
	invalid.ActionPlans = []json.RawMessage{encoded}
	encoded, _ = json.Marshal(invalid)
	if err = validator.Complete(ctx, l, encoded); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("foreign tenant rejection class: %v contract=%v proposal=%s", err, contract.Validate("https://ops.local/schemas/investigation-result/v1", encoded), encoded)
	}
	// Historical rows deliberately survive deletion for Evidence/replay. A
	// different same-scope target must still be current before a recommendation
	// can name it; validating the Incident's current RCA does not check this row.
	retired, err := resource.ParseCanonicalID(primary)
	if err != nil {
		t.Fatal(err)
	}
	retired.StableID = uuid.NewString()
	retiredTarget := retired.String()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at,deleted_at) SELECT tenant_id,$3,cluster_id,kind,namespace,name,metadata,observed_at,clock_timestamp() FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2`, job.TenantID, primary, retiredTarget); err != nil {
		t.Fatal("prepare isolated retained target row: ", err)
	}
	plan["tenantId"] = job.TenantID.String()
	plan["targetCanonicalId"] = retiredTarget
	encoded, _ = json.Marshal(plan)
	invalid.ActionPlans = []json.RawMessage{encoded}
	encoded, _ = json.Marshal(invalid)
	if err = validator.Complete(ctx, l, encoded); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("deleted same-scope ActionPlan target accepted: %v", err)
	}
	encoded, _ = json.Marshal(p)
	if err = validator.Complete(ctx, l, encoded); err != nil {
		t.Fatal("valid recommendation rejected: ", err)
	}
	var recommendations, executions, confirmations int
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM action.plans WHERE tenant_id=$1),(SELECT count(*) FROM action.executions WHERE tenant_id=$1),(SELECT count(*) FROM action.risk_acknowledgements WHERE tenant_id=$1)`, job.TenantID).Scan(&recommendations, &executions, &confirmations); err != nil || recommendations != 1 || executions != 0 || confirmations != 0 {
		t.Fatalf("validated suggestion granted execution authority: plans=%d executions=%d confirmations=%d err=%v", recommendations, executions, confirmations, err)
	}
	final, err := repo.Get(ctx, job.TenantID, job.JobID)
	if err != nil || final.State != "succeeded" {
		t.Fatalf("final advisory result: %s %v", final.State, err)
	}
	unchanged, err := (rca.Repository{Pool: pool}).Read(ctx, job.TenantID, iid.String(), revision)
	if err != nil || finding.Hash(unchanged) != finding.Hash(rev) {
		t.Fatal("model proposal changed deterministic RCA")
	}
	t.Log("actual native Lease + mTLS Graph + current signed Recipe/facts + authorized ranked Evidence + Ledger + Go advisory validator; forged candidate, foreign tenant, deleted same-scope target and execution handle rejected; current target accepted and deterministic RCA unchanged")
}
