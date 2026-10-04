package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/investigation"
	gateway "ops-platform/internal/investigation/mcp"
	"ops-platform/internal/investigation/tools"
	"os"
	"strings"
	"testing"
	"time"
)

type sp06BearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b sp06BearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	q := r.Clone(r.Context())
	q.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(q)
}

// State fixtures exercise the protocol/ledger boundary for every tool. They are
// labelled fixtures; real business and model closure is a separate mandatory gate.
type sp06StateFixture struct{ state *string }

func (f sp06StateFixture) CheckScope(_ context.Context, j investigation.Job, name string, args json.RawMessage) error {
	return tools.CheckArguments(j, name, args)
}

func (f sp06StateFixture) Invoke(ctx context.Context, _ investigation.Job, name string, _ json.RawMessage) (tools.Result, error) {
	if *f.state == "timeout" {
		<-ctx.Done()
		return tools.Result{}, ctx.Err()
	}
	out := tools.Result{Tool: name, State: *f.state, Data: map[string]any{"fixture": "protocol-state-matrix", "password": "must-not-leak"}, EvidenceRefs: []string{}, DegradedSources: []string{}, Partial: *f.state != "succeeded"}
	if *f.state == "degraded" {
		out.DegradedSources = []string{"fixture-source"}
	}
	if *f.state == "oversized" {
		out.State = "succeeded"
		out.Data = map[string]any{"log": strings.Repeat("x", 1<<20)}
	}
	return out, nil
}
func TestSP06StandardMCPToolStateReplayAndDenialMatrix(t *testing.T) {
	ctx, _, repo, req := sp06JobFixture(t)
	caBytes, err := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), Token: os.Getenv("SP03_TEST_OPENBAO_TOKEN"), CACertBundle: caBytes})
	if err != nil {
		t.Fatal(err)
	}
	if err = bao.ConfigureTransit(ctx); err != nil {
		t.Fatal(err)
	}
	signer := investigation.ContextSigner{Transit: bao, Key: "investigation-signing"}
	files := sp06Certificates(t, t.TempDir())
	caBytes, _ = os.ReadFile(files["ca.pem"])
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caBytes)
	crlBytes, _ := os.ReadFile(files["crl.pem"])
	block, _ := pem.Decode(crlBytes)
	crl, _ := x509.ParseRevocationList(block.Bytes)
	identity, _ := auth.NewWorkloadIdentity("sp06-test", "ops-investigator")
	trust := auth.WorkloadTrust{Roots: roots, Allowed: []auth.WorkloadIdentity{identity}, RevocationList: crl, CRLFetchedAt: time.Now()}
	defs, err := tools.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	req.ToolCatalogDigest = tools.Digest(defs)
	state := "succeeded"
	deny := false
	handler, err := (gateway.Gateway{Repository: repo, Signer: signer, Trust: trust, API: sp06StateFixture{&state}, Authorize: func(context.Context, investigation.Job, string, json.RawMessage) error {
		if deny {
			return investigation.ErrDenied
		}
		return nil
	}}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	certificate, _ := tls.LoadX509KeyPair(files["ops-api.pem"], files["ops-api.key"])
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	clientCert, _ := tls.LoadX509KeyPair(files["ops-investigator.pem"], files["ops-investigator.key"])
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{clientCert}, ServerName: "ops-api.sp06-test.svc.cluster.local", MinVersion: tls.VersionTLS13}}
	defer transport.CloseIdleConnections()
	for index, wanted := range []string{"succeeded", "partial", "degraded"} {
		req.TriggerKind = []string{"manual", "new_evidence", "incident_policy"}[index]
		job, err := repo.CreateJob(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		lease, err := repo.Claim(ctx, job.TenantID, "matrix-worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		token, err := signer.IssueRegistered(ctx, repo, job, lease, "platform-mcp-gateway", identity.URI, tools.Names(defs))
		if err != nil {
			t.Fatal(err)
		}
		client := sdk.NewClient(&sdk.Implementation{Name: "sp06-matrix", Version: "1"}, nil)
		session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: sp06BearerTransport{transport, token}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := session.ListTools(ctx, nil)
		defer session.Close()
		if err != nil || len(catalog.Tools) != 16 {
			t.Fatal("standard initialize/list handshake", err)
		}
		for _, d := range defs {
			if d.Disabled != "" {
				continue
			}
			raw, err := os.ReadFile("../fixtures/contracts/valid/mcp-" + d.Name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			// The frozen public-contract examples use tenant-a. Bind the
			// protocol-state fixture to this actual owned Job's tenant.
			raw = []byte(strings.ReplaceAll(string(raw), "tenant-a", job.TenantID.String()))
			var args map[string]any
			if json.Unmarshal(raw, &args) != nil {
				t.Fatal("fixture")
			}
			if _, ok := args["incidentId"]; ok {
				args["incidentId"] = job.IncidentID.String()
			}
			if d.Name == "get_findings" {
				args = map[string]any{"incidentId": job.IncidentID.String()}
			}
			if d.Name == "query_kubernetes" {
				args["canonicalId"] = "k8s+v1://" + job.TenantID.String() + "/sp05-cluster/core/Node/node-a"
			}
			if d.Name == "query_deepflow" {
				args["resourceCanonicalId"] = "k8s+v1://" + job.TenantID.String() + "/sp05-cluster/core/Node/node-a"
				args["timeRange"] = map[string]string{"from": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "to": time.Now().UTC().Format(time.RFC3339)}
			}
			encoded, _ := json.Marshal(args)
			if d.Validate(encoded) != nil {
				t.Fatalf("v2 fixture invalid %s: %s", d.Name, encoded)
			}
			allocation, err := repo.AllocateTool(ctx, lease, d.Name, encoded)
			if err != nil {
				t.Fatal(err)
			}
			params := &sdk.CallToolParams{Name: d.Name, Arguments: args}
			params.SetMeta(map[string]any{"ops/stepId": allocation.StepID.String(), "ops/jti": allocation.JTI})
			deny = true
			result, err := session.CallTool(ctx, params)
			if err != nil || !result.IsError {
				t.Fatalf("%s denied fixture: %v", d.Name, err)
			}
			deny = false
			state = wanted
			result, err = session.CallTool(ctx, params)
			if err != nil {
				t.Fatalf("%s %s: %v", d.Name, wanted, err)
			}
			encoded, _ = json.Marshal(result.StructuredContent)
			var out tools.Result
			if json.Unmarshal(encoded, &out) != nil || out.State != wanted || tools.ValidateOutput(encoded) != nil {
				t.Fatalf("%s wrong state %s", d.Name, encoded)
			}
			if strings.Contains(string(encoded), "must-not-leak") {
				t.Fatal("secret leak")
			}
			if out.Budget.Consumed.ToolCalls != 1 || out.Budget.Remaining.ToolCalls < 1 || out.Budget.Consumed.ResultBytes != int64(len(encoded)) {
				t.Fatalf("response budget differs from committed ledger %s", encoded)
			}
			result, err = session.CallTool(ctx, params)
			if err != nil || !result.IsError {
				t.Fatalf("%s replay accepted", d.Name)
			}
		}
		for _, name := range []string{"execute_command", "kubectl_exec", "ssh", "sql", "fetch_url", "unknown"} {
			if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
				t.Fatalf("unregistered tool accepted %s", name)
			}
		}
		vm, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "query_kubevirt", Arguments: map[string]any{}})
		if err != nil || !vm.IsError {
			t.Fatal("disabled virtualization returned success")
		}
		vmJSON, _ := json.Marshal(vm.StructuredContent)
		if !strings.Contains(string(vmJSON), "disabled/unverified") {
			t.Fatal("disabled capability status missing")
		}
		for _, mode := range []string{"timeout", "oversized"} {
			state = mode
			args := map[string]any{"canonicalId": "k8s+v1://" + job.TenantID.String() + "/sp05-cluster/core/Node/" + mode, "depth": 0}
			encoded, _ := json.Marshal(args)
			allocation, err := repo.AllocateTool(ctx, lease, "get_resource_context", encoded)
			if err != nil {
				t.Fatal(err)
			}
			p := &sdk.CallToolParams{Name: "get_resource_context", Arguments: args}
			p.SetMeta(map[string]any{"ops/stepId": allocation.StepID.String(), "ops/jti": allocation.JTI})
			result, err := session.CallTool(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(result.StructuredContent)
			var out tools.Result
			if json.Unmarshal(b, &out) != nil {
				t.Fatal("result contract")
			}
			if mode == "timeout" && (out.State != "failed" || out.ErrorCode != "SOURCE_TIMEOUT") {
				t.Fatalf("timeout state %s", b)
			}
			if mode == "oversized" && (out.State != "partial" || !out.Partial || len(b) > 65536) {
				t.Fatalf("truncation state %s", b)
			}
		}
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "get_incident_context", Arguments: map[string]any{"incidentId": job.IncidentID.String(), "tenantId": uuid.NewString()}})
		if err == nil && !result.IsError {
			t.Fatal("schema/tenant injection accepted")
		}
		session.Close()
		if err = repo.Cancel(ctx, lease); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("15 tools x succeeded/partial/degraded/current-denied with official Streamable HTTP, real Transit and PostgreSQL; disabled VM and unregistered writes remain unavailable")
}
