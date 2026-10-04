package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/contract"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/investigation"
	gateway "ops-platform/internal/investigation/mcp"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/policy"
	"ops-platform/internal/resource"
)

// All domain bindings run through the standard SDK and actual PG, current
// signed OPA, native Graph Lease/TLS and archived Evidence. Sources absent from
// this explicit hardware protocol fixture must report degradation honestly.
func sp06ActualSemanticMCP(t *testing.T, ctx context.Context, repo investigation.Repository, job investigation.Job, lease investigation.Lease, defs []tools.Definition, semantic httpapi.InvestigationTools, trust configregistry.SignatureVerifier, clusterID uuid.UUID, nodes []resource.Entity, primary string) {
	t.Helper()
	ca, err := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), Token: os.Getenv("SP03_TEST_OPENBAO_TOKEN"), CACertBundle: ca})
	if err != nil {
		t.Fatal(err)
	}
	if err = bao.ConfigureTransit(ctx); err != nil {
		t.Fatal(err)
	}
	signer := investigation.ContextSigner{Transit: bao, Key: "investigation-signing"}
	compiler, err := policy.NewBundleCompiler(trust)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := configregistry.NewService(repo.Pool, trust, compiler)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := policy.NewEvaluator(registry, compiler, "sp06-read-only")
	if err != nil {
		t.Fatal(err)
	}
	names := tools.Names(defs)
	scope := configregistry.Scope{Type: configregistry.ScopeCluster, ClusterID: clusterID}
	authorize := func(ctx context.Context, j investigation.Job, name string, _ json.RawMessage) error {
		decision, err := evaluator.Evaluate(ctx, policy.PolicyInput{Request: auth.RequestContext{TenantID: j.TenantID, Subject: j.Subject}, PrincipalType: policy.PrincipalAgent, RequestType: policy.RequestTool, Scope: scope, Invocation: &policy.InvocationContext{Verified: true, TenantID: j.TenantID, Scope: scope, AllowedTools: names}, ToolName: name, AllowedTools: names, ToolReadOnly: true, Risk: policy.RiskLow})
		if err != nil || !decision.Allow || decision.PolicyVersion != j.PolicyVersion {
			return investigation.ErrDenied
		}
		return nil
	}
	files := sp06Certificates(t, t.TempDir())
	ca, _ = os.ReadFile(files["ca.pem"])
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	raw, _ := os.ReadFile(files["crl.pem"])
	block, _ := pem.Decode(raw)
	crl, _ := x509.ParseRevocationList(block.Bytes)
	identity, _ := auth.NewWorkloadIdentity("sp06-test", "ops-investigator")
	workload := auth.WorkloadTrust{Roots: roots, Allowed: []auth.WorkloadIdentity{identity}, RevocationList: crl, CRLFetchedAt: time.Now()}
	handler, err := (gateway.Gateway{Repository: repo, Signer: signer, Trust: workload, API: semantic, Authorize: authorize}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	certificate, _ := tls.LoadX509KeyPair(files["ops-api.pem"], files["ops-api.key"])
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	certificate, _ = tls.LoadX509KeyPair(files["ops-investigator.pem"], files["ops-investigator.key"])
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{certificate}, ServerName: "ops-api.sp06-test.svc.cluster.local", MinVersion: tls.VersionTLS13}}
	defer transport.CloseIdleConnections()
	token, err := signer.IssueRegistered(ctx, repo, job, lease, "platform-mcp-gateway", identity.URI, names)
	if err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "sp06-actual-domain-bindings", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: sp06BearerTransport{transport, token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 16 {
		t.Fatal("actual standard handshake/catalog", err)
	}
	k8sID := ""
	for _, n := range nodes {
		if strings.HasPrefix(n.CanonicalID, "k8s+v1://") && job.Scope.Allows(n.CanonicalID, n.Namespace) {
			k8sID = n.CanonicalID
			break
		}
	}
	if k8sID == "" {
		t.Fatal("actual fixture lacks authorized Kubernetes binding")
	}
	// Rejected query scope must not consume the nonce or reserve any budget.
	// These still have real, allocated Job Step identities and signed context.
	for name, args := range map[string]map[string]any{
		"foreign tenant":   {"canonicalId": strings.Replace(k8sID, job.TenantID.String(), uuid.NewString(), 1)},
		"unknown resource": {"canonicalId": strings.TrimSuffix(k8sID, strings.Split(k8sID, "/")[len(strings.Split(k8sID, "/"))-1]) + uuid.NewString()},
		"expanded time":    {"resourceCanonicalId": k8sID, "type": "log", "timeRange": map[string]string{"from": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339), "to": time.Now().UTC().Format(time.RFC3339)}},
	} {
		tool := "get_resource_context"
		if name != "expanded time" {
			args["depth"] = 1
		}
		if name == "expanded time" {
			tool = "query_logs"
		}
		if name == "expanded time" {
			args["queryTemplate"] = "node-events/v1"
			args["budget"] = map[string]int{"timeoutMs": 3000, "maxBytes": 65536}
		}
		before, err := repo.Get(ctx, job.TenantID, job.JobID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(args)
		for _, d := range defs {
			if d.Name == tool && d.Validate(encoded) != nil {
				t.Fatalf("%s scope test must have valid schema", name)
			}
		}
		allocation, err := repo.AllocateTool(ctx, lease, tool, encoded)
		if err != nil {
			t.Fatal(err)
		}
		call := &sdk.CallToolParams{Name: tool, Arguments: args}
		call.SetMeta(map[string]any{"ops/stepId": allocation.StepID.String(), "ops/jti": allocation.JTI})
		result, err := session.CallTool(ctx, call)
		if err != nil || !result.IsError {
			t.Fatalf("%s scope denial: %v %+v", name, err, result)
		}
		after, err := repo.Get(ctx, job.TenantID, job.JobID)
		if err != nil || after.Consumed != before.Consumed || after.Reserved != before.Reserved {
			t.Fatalf("%s denial charged budget: %v", name, err)
		}
		steps, err := repo.Steps(ctx, lease)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range steps {
			if step.StepID == allocation.StepID {
				t.Fatalf("%s scope denial began a Step", name)
			}
		}
	}
	count := 0
	for _, d := range defs {
		if d.Disabled != "" {
			continue
		}
		raw, err := os.ReadFile("../fixtures/contracts/valid/mcp-" + d.Name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		args := map[string]any{}
		json.Unmarshal(raw, &args)
		if _, ok := args["incidentId"]; ok {
			args["incidentId"] = job.IncidentID.String()
		}
		if d.Name == "get_findings" {
			args = map[string]any{"incidentId": job.IncidentID.String()}
		}
		if _, ok := args["canonicalId"]; ok {
			args["canonicalId"] = primary
			if d.Name == "query_kubernetes" {
				args["canonicalId"] = k8sID
			}
		}
		if _, ok := args["resourceCanonicalId"]; ok {
			args["resourceCanonicalId"] = k8sID
		}
		if _, ok := args["timeRange"]; ok || d.Name == "query_deepflow" {
			args["timeRange"] = map[string]string{"from": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "to": time.Now().UTC().Format(time.RFC3339)}
		}
		encoded, _ := json.Marshal(args)
		if err = d.Validate(encoded); err != nil {
			t.Fatalf("%s actual arguments: %v", d.Name, err)
		}
		allocation, err := repo.AllocateTool(ctx, lease, d.Name, encoded)
		if err != nil {
			t.Fatal(err)
		}
		call := &sdk.CallToolParams{Name: d.Name, Arguments: args}
		call.SetMeta(map[string]any{"ops/stepId": allocation.StepID.String(), "ops/jti": allocation.JTI})
		result, err := session.CallTool(ctx, call)
		if err != nil {
			t.Fatalf("%s actual domain call: %v result=%+v", d.Name, err, result)
		}
		data, _ := json.Marshal(result.StructuredContent)
		var output tools.Result
		if json.Unmarshal(data, &output) != nil || tools.ValidateOutput(data) != nil {
			t.Fatalf("%s actual output contract: %s", d.Name, data)
		}
		if output.State != "succeeded" && output.State != "partial" && output.State != "degraded" {
			t.Fatalf("%s invalid actual state %s", d.Name, output.State)
		}
		if result.IsError != (output.State != "succeeded") {
			t.Fatalf("%s MCP error flag disagrees with state %s", d.Name, output.State)
		}
		if d.Name == "get_ranked_evidence" && len(output.EvidenceRefs) == 0 {
			t.Fatal("current ranked Evidence mapping lost IDs")
		}
		t.Logf("actual signed OPA/SDK/domain binding tool=%s state=%s partial=%t refs=%d", d.Name, output.State, output.Partial, len(output.EvidenceRefs))
		count++
	}
	if count != 15 {
		t.Fatalf("actual nonvirtual binding count=%d", count)
	}
	semantic.SP04.Investigation = &httpapi.InvestigationHandlers{Repository: repo}
	for _, cluster := range []string{"", job.Scope.Cluster, "unauthorized-cluster"} {
		r := httptest.NewRequest("GET", "/api/v1/capabilities", nil)
		q := r.URL.Query()
		if cluster != "" {
			q.Set("clusterUid", cluster)
		}
		r.URL.RawQuery = q.Encode()
		r = r.WithContext(auth.WithRequestContext(ctx, auth.RequestContext{TenantID: job.TenantID, Subject: job.Subject, Roles: []auth.Role{auth.Operator}, RequestID: uuid.NewString()}))
		w := httptest.NewRecorder()
		semantic.SP04.ServeCapabilities(w, r)
		if cluster == "unauthorized-cluster" {
			if w.Code != 403 {
				t.Fatalf("foreign cluster capability status=%d", w.Code)
			}
			continue
		}
		var response struct {
			RequestID string          `json:"requestId"`
			Data      json.RawMessage `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.RequestID == "" || contract.Validate("https://ops.local/schemas/platform-capabilities/v1", response.Data) != nil {
			t.Fatalf("typed current capability observation: %d %s", w.Code, w.Body.Bytes())
		}
		var data map[string]any
		json.Unmarshal(response.Data, &data)
		if data["apiReady"] != true || cluster == "" && (data["graphReady"] != nil || data["sourceDegraded"] != nil) || cluster != "" && data["graphReady"] != true {
			r.URL.Path = "/api/v1/resources"
			q.Set("limit", "1")
			r.URL.RawQuery = q.Encode()
			diagnostic := httptest.NewRecorder()
			semantic.SP04.ServeHTTP(diagnostic, r)
			t.Logf("capability resource API diagnostic: %d %s", diagnostic.Code, diagnostic.Body.Bytes())
			t.Fatalf("capabilities claimed unobserved readiness: %s", response.Data)
		}
	}
}
