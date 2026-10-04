package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSP06RealTransitScopedSigningRotationAndTakeoverContext(t *testing.T) {
	ctx, db, repo, req := sp06JobFixture(t)
	ca, err := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("Bao CA")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}, Timeout: 5 * time.Second}
	request := func(method, path string, body any, token string) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequestWithContext(ctx, method, os.Getenv("SP03_TEST_OPENBAO_URL")+"/v1/"+path, bytes.NewReader(raw))
		r.Header.Set("X-Vault-Token", token)
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("owned Bao request unavailable")
		}
		defer response.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(response.Body, 65536))
		var value map[string]any
		_ = json.Unmarshal(b, &value)
		return response.StatusCode, value
	}
	rootToken := os.Getenv("SP03_TEST_OPENBAO_TOKEN")
	name := "sp06-signing-" + uuid.NewString()
	if status, _ := request("POST", "transit/keys/"+name, map[string]any{"type": "ecdsa-p256", "exportable": false, "allow_plaintext_backup": false}, rootToken); status != 204 && status != 200 {
		t.Fatalf("create signing key %d", status)
	}
	policy := `path "transit/keys/` + name + `" {capabilities=["read"]} path "transit/sign/` + name + `" {capabilities=["update"]}`
	if status, _ := request("PUT", "sys/policies/acl/"+name, map[string]any{"policy": policy}, rootToken); status != 204 {
		t.Fatal("scoped signing policy")
	}
	status, out := request("POST", "auth/token/create", map[string]any{"policies": []string{name}, "no_default_policy": true, "ttl": "10m"}, rootToken)
	if status != 200 {
		t.Fatal("scoped signer identity")
	}
	authData, ok := out["auth"].(map[string]any)
	if !ok {
		t.Fatal("signer token response")
	}
	scopedToken, _ := authData["client_token"].(string)
	scoped, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), CACertBundle: ca, Token: scopedToken})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), CACertBundle: ca, Token: rootToken})
	if err != nil {
		t.Fatal(err)
	}
	signer := investigation.ContextSigner{Transit: scoped, Key: name}
	defs, err := tools.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	req.ToolCatalogDigest = tools.Digest(defs)
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repo.Claim(ctx, j.TenantID, "context-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	identity := "spiffe://ops.local/ns/sp06-test/sa/ops-investigator"
	token, err := signer.IssueRegistered(ctx, repo, j, lease, "platform-mcp-gateway", identity, []string{"get_findings"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.VerifyContext(ctx, token, "platform-mcp-gateway", identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = signer.VerifyContext(ctx, token, "platform-mcp-gateway", identity); err != nil {
		t.Fatal("Verify consumed nonce")
	}
	if _, err = signer.VerifyContext(ctx, token, "platform-job-api", identity); !errors.Is(err, investigation.ErrDenied) {
		t.Fatal("wrong audience accepted")
	}
	if _, err = signer.VerifyContext(ctx, token, "platform-mcp-gateway", "spiffe://ops.local/ns/sp06-test/sa/other"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatal("wrong workload accepted")
	}
	expanded := claims
	expanded.ContextID = uuid.New()
	expanded.Scope.Namespaces = append(expanded.Scope.Namespaces, "other")
	if err = repo.RegisterContext(ctx, lease, expanded); !errors.Is(err, investigation.ErrLease) {
		t.Fatal("expanded scope registered", err)
	}
	sp06CheckContextRenewal(t, ctx, repo, j, lease, signer)
	allocation, err := repo.AllocateTool(ctx, lease, "get_findings", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	call := investigation.Call{Claims: &claims, StepID: allocation.StepID, ContextID: claims.ContextID, JTI: allocation.JTI, Name: "get_findings", ArgsDigest: investigation.ArgumentsDigest([]byte(`{}`)), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 1024}}
	if _, err = repo.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BeginCall(ctx, lease, call); !errors.Is(err, investigation.ErrReplay) {
		t.Fatal("issued context replay accepted", err)
	}
	for _, path := range []string{"transit/export/signing-key/" + name, "transit/keys/" + name + "/rotate", "transit/keys/evidence-archive"} {
		if status, _ := request("GET", path, nil, scopedToken); status != 403 && status != 405 {
			t.Fatalf("signer escaped policy %d", status)
		}
	}
	if err = admin.TransitRotate(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err = signer.VerifyContext(ctx, token, "platform-mcp-gateway", identity); err != nil {
		t.Fatal("rotation lost unexpired retained verification version")
	}
	if status, _ := request("POST", "transit/keys/"+name+"/config", map[string]any{"min_decryption_version": 2}, rootToken); status != 204 && status != 200 {
		t.Fatal("verification version retirement")
	}
	if _, err = signer.VerifyContext(ctx, token, "platform-mcp-gateway", identity); !errors.Is(err, investigation.ErrDenied) {
		t.Fatal("retired kid accepted")
	}
	next, err := signer.IssueRegistered(ctx, repo, j, lease, "platform-mcp-gateway", identity, []string{"get_findings"})
	if err != nil {
		t.Fatal(err)
	}
	nextClaims, err := signer.VerifyContext(ctx, next, "platform-mcp-gateway", identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.worker_queue SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, j.JobID); err != nil {
		t.Fatal(err)
	}
	lease, err = repo.Claim(ctx, j.TenantID, "takeover", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.Authenticate(ctx, nextClaims, j.ToolCatalogDigest); !errors.Is(err, investigation.ErrLease) {
		t.Fatal("old Context survived takeover", err)
	}
	steps, err := repo.Steps(ctx, lease)
	if err != nil || len(steps) != 1 || steps[0].State != "failed" || steps[0].ErrorCode != "UNKNOWN_OUTCOME" {
		t.Fatal("takeover did not preserve unknown history", err)
	}
	got, err := repo.Get(ctx, j.TenantID, j.JobID)
	if err != nil || got.Consumed.ToolCalls != 1 || got.Reserved != (investigation.Usage{}) {
		t.Fatal("takeover reset budget")
	}
	t.Log("actual scoped non-exportable ES256 Transit key, retained/retired kid rotation, verify-only Context, DB nonce boundary and generation takeover; no signer root token in runtime/model")
}

func sp06CheckContextRenewal(t *testing.T, ctx context.Context, repo investigation.Repository, j investigation.Job, l investigation.Lease, signer investigation.ContextSigner) {
	t.Helper()
	files := sp06Certificates(t, t.TempDir())
	ca, _ := os.ReadFile(files["ca.pem"])
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	crlBytes, _ := os.ReadFile(files["crl.pem"])
	block, _ := pem.Decode(crlBytes)
	crl, _ := x509.ParseRevocationList(block.Bytes)
	identity, _ := auth.NewWorkloadIdentity("sp06-test", "ops-investigator")
	allowed := true
	h := &httpapi.InvestigationHandlers{Repository: repo, Signer: signer, Trust: auth.WorkloadTrust{Roots: roots, Allowed: []auth.WorkloadIdentity{identity}, RevocationList: crl, CRLFetchedAt: time.Now()}, Authorize: func(context.Context, investigation.Job, string, json.RawMessage) error {
		if !allowed {
			return investigation.ErrDenied
		}
		return nil
	}}
	server := httptest.NewUnstartedServer(h.InternalHandler())
	certificate, _ := tls.LoadX509KeyPair(files["ops-api.pem"], files["ops-api.key"])
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	clientCert, _ := tls.LoadX509KeyPair(files["ops-investigator.pem"], files["ops-investigator.key"])
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{clientCert}, ServerName: "ops-api.sp06-test.svc.cluster.local"}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	narrower := j
	narrower.Budget.AllowedDataClasses = []string{"D0"}
	token, err := signer.IssueRegistered(ctx, repo, narrower, l, "platform-job-api", identity.URI, []string{"get_findings"})
	if err != nil {
		t.Fatal(err)
	}
	post := func(token, suffix string, body string) (int, []byte) {
		r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/internal/v1/investigations/"+j.JobID.String()+suffix, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, b
	}
	status, b := post(token, "/contexts:renew", "{}")
	if status != 200 {
		t.Fatalf("renew status %d", status)
	}
	var result struct {
		Data map[string]string `json:"data"`
	}
	if json.Unmarshal(b, &result) != nil {
		t.Fatal("renew response")
	}
	for field, aud := range map[string]string{"jobContext": "platform-job-api", "mcpContext": "platform-mcp-gateway"} {
		claims, err := signer.VerifyContext(ctx, result.Data[field], aud, identity.URI)
		if err != nil || !slices.Equal(claims.AllowedTools, []string{"get_findings"}) || !slices.Equal(claims.AllowedDataClasses, []string{"D0"}) {
			t.Fatalf("Context renewal widened permissions: tools=%v classes=%v err=%v", claims.AllowedTools, claims.AllowedDataClasses, err)
		}
	}
	defs, _ := tools.Catalog()
	token, err = signer.IssueRegistered(ctx, repo, j, l, "platform-job-api", identity.URI, tools.Names(defs))
	if err != nil {
		t.Fatal(err)
	}
	allowed = false
	status, _ = post(token, "/calls:allocate", `{"model":true,"name":"model","arguments":{"requestDigest":"sha256:`+strings.Repeat("a", 64)+`"},"reserve":{"modelRequests":1,"inputTokens":16384,"outputTokens":1024,"resultBytes":65536}}`)
	if status != 403 {
		t.Fatalf("current policy denied model admission status=%d", status)
	}
	after, err := repo.Get(ctx, j.TenantID, j.JobID)
	if err != nil || after.Reserved != (investigation.Usage{}) || after.Consumed != (investigation.Usage{}) {
		t.Fatal("policy-denied model altered Ledger budget")
	}
	t.Log("actual Transit/TLS Job API renewal preserves narrowed tools/classes and model allocation rechecks current policy before any reservation")
}
