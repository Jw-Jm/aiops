package integration

// MR-017: real signed envelope -> mTLS Worker entry.
//
// The test signs the internal context with graph.SignContext, sends it over a real
// TLS listener, and lets the real graph.InternalHandler verify the client
// certificate through auth.VerifyWorkload and authorize the request against a
// worker_runtime_role pool. A request that passes authorization reaches the lease
// stage (GRAPH_NOT_READY); a refused one returns FORBIDDEN or STALE_CONTEXT before
// any graph work. That difference is the authorization boundary under review.
//
// It also pins the worker privilege boundary: the worker role can call the narrow
// resolvers but has no direct identity table access.
//
// Opt-in (native identity test guard); compiled, not executed, locally. A fully
// contract-valid 200 also needs live published graph state, which is remote.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
)

type nativeInternalPKI struct {
	serverCert tls.Certificate
	trust      auth.WorkloadTrust
	client     *http.Client
}

func newNativeInternalPKI(t *testing.T) nativeInternalPKI {
	t.Helper()
	now := time.Now().UTC()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "native-internal-ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	issue := func(serial int64, usage x509.ExtKeyUsage, uri url.URL) tls.Certificate {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: uri.String()},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(2 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
			URIs:     []*url.URL{&uri},
			DNSNames: []string{strings.Split(uri.Path, "/")[4] + "." + strings.Split(uri.Path, "/")[2] + ".svc.cluster.local"},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	apiURI := url.URL{Scheme: "spiffe", Host: "ops.local", Path: "/ns/native-internal/sa/ops-api"}
	serverURI := url.URL{Scheme: "spiffe", Host: "ops.local", Path: "/ns/native-internal/sa/ops-worker"}
	clientCert := issue(2, x509.ExtKeyUsageClientAuth, apiURI)
	serverCert := issue(3, x509.ExtKeyUsageServerAuth, serverURI)
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: now, NextUpdate: now.Add(time.Hour),
	}, caCert, caKey)
	if err != nil {
		t.Fatal(err)
	}
	crl, err := x509.ParseRevocationList(crlDER)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewWorkloadIdentity("native-internal", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	identity.URI = apiURI.String()
	return nativeInternalPKI{
		serverCert: serverCert,
		trust: auth.WorkloadTrust{
			Roots: roots, Allowed: []auth.WorkloadIdentity{identity},
			RevocationList: crl, CRLFetchedAt: now, Now: func() time.Time { return now },
		},
		client: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{clientCert},
			// The test server certificate is self-signed by the throwaway CA; the
			// client certificate is still verified by the Worker trust bundle.
			InsecureSkipVerify: true, //nolint:gosec // test-only throwaway CA
			MinVersion:         tls.VersionTLS13,
		}}},
	}
}

func nativeInternalWorkerServer(t *testing.T, handler graph.InternalHandler, pki nativeInternalPKI) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{pki.serverCert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// nativeInternalPost sends one signed graph query and returns the status.
func nativeInternalPost(t *testing.T, client *http.Client, endpoint, token string, payload []byte) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint+"/internal/v1/graph:query", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Graph-Context", token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("worker rpc: %v", err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

// TestNativeInternalWorkerRPC proves the signed source and the worker role still
// land on the same authorization boundary as the in-process resolver.
func TestNativeInternalWorkerRPC(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	password := "mfa-hardening-rpc-password"
	_, userID, session, _ := nativeMFAHardeningEnrolled(t, f, "mfa.rpc.", password)
	sessionID := nativeInternalSessionID(t, session.sessionRef)

	clusterUID := "internal-rpc-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	clusterID := uuid.Must(uuid.NewV7())
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		INSERT INTO platform.cluster_registrations
			(tenant_id, cluster_id, cluster_uid, display_name, status, revision)
		VALUES ($1,$2,$3,'internal rpc','active',1)
	`, f.tenantA, clusterID, clusterUID)
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		INSERT INTO identity.role_grants
			(tenant_id, user_id, binding_id, role, scope, authorization_version, status)
		VALUES ($1,$2,$3,'operator',$4::jsonb,1,'active')
	`, f.tenantA, userID, uuid.Must(uuid.NewV7()),
		`{"clusterUids":["`+clusterUID+`"],"namespaces":[]}`)

	frozen := nativeInternalScope(t, f, userID, clusterUID, 1)
	payload := []byte(`{"queryKind":"entity","canonicalId":"node:one","expectedOwnerEpoch":1,"maxDepth":1,"maxNodes":1,"maxEdges":1,"scope":` + mustJSON(t, frozen) + `,"relationKinds":[]}`)
	pki := newNativeInternalPKI(t)
	handler := graph.InternalHandler{
		Key:           f.internalPublicKey,
		Authorization: graph.Authorization{Pool: f.workerPool},
		Trust:         pki.trust,
	}
	server := nativeInternalWorkerServer(t, handler, pki)

	sign := func(claims graph.Claims) string {
		t.Helper()
		claims.Audience = "platform-graph-query"
		claims.QueryDigest = graph.ScopeDigest(json.RawMessage(payload))
		if claims.Deadline.IsZero() {
			claims.Deadline = time.Now().Add(10 * time.Second)
		}
		token, err := graph.SignContext(f.internalSigningKey, claims)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	interactive := graph.Claims{
		Source:  graph.SourceNativeInteractive,
		Native:  &graph.NativeProof{SessionID: sessionID.String()},
		Subject: session.subject,
		Scope:   frozen,
	}
	// Authorized: reaches the lease stage (no lease configured in this test).
	if status := nativeInternalPost(t, pki.client, server.URL, sign(interactive), payload); status == http.StatusForbidden || status == http.StatusConflict {
		t.Fatalf("authorized worker rpc = %d, want it to pass the authorization boundary", status)
	}
	// The same request without the worker-verified client certificate must fail.
	if status := nativeInternalPost(t, &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // test-only throwaway CA
		MinVersion:         tls.VersionTLS13,
	}}}, server.URL, sign(interactive), payload); status != http.StatusForbidden {
		t.Fatalf("rpc without a client certificate = %d, want 403", status)
	}

	// Legacy source has no legacy binding for this subject and must not borrow
	// the native operator grant.
	if status := nativeInternalPost(t, pki.client, server.URL, sign(graph.Claims{
		Source: graph.SourceLegacy, Subject: session.subject, Scope: frozen,
	}), payload); status != http.StatusForbidden {
		t.Fatalf("legacy claim over a worker rpc = %d, want 403", status)
	}
	// A widened signed scope is refused.
	widened := frozen
	widened.Namespaces = append(widened.Namespaces, "default")
	if status := nativeInternalPost(t, pki.client, server.URL, sign(graph.Claims{
		Source: graph.SourceNativeInteractive, Native: &graph.NativeProof{SessionID: sessionID.String()},
		Subject: session.subject, Scope: widened,
	}), payload); status == 0 || status == http.StatusOK {
		t.Fatalf("widened scope over a worker rpc = %d, want a refusal", status)
	}
	// Another tenant's session reference is refused.
	foreign := frozen
	foreign.Tenant = f.tenantB.String()
	foreignToken := sign(graph.Claims{
		Source: graph.SourceNativeInteractive, Native: &graph.NativeProof{SessionID: sessionID.String()},
		Subject: session.subject, Scope: foreign,
	})
	if status := nativeInternalPost(t, pki.client, server.URL, foreignToken, payload); status != http.StatusForbidden && status != http.StatusConflict {
		t.Fatalf("foreign tenant rpc = %d, want 403/409", status)
	}

	// Worker privilege boundary: no direct identity table access.
	var count int
	if err := f.workerPool.QueryRow(f.ctx, `SELECT count(*) FROM identity.accounts`).Scan(&count); err == nil {
		t.Fatal("worker_runtime_role can read identity.accounts directly")
	}
	// The narrow resolver is callable through the worker pool.
	if err := f.workerPool.QueryRow(f.ctx, `SELECT count(*) FROM identity.native_internal_session_shared($1,$2)`,
		f.tenantA, sessionID).Scan(&count); err != nil {
		t.Fatalf("worker role cannot call the narrow resolver: %v", err)
	}

	// Persisted task source: the worker re-proves the whole frozen task.
	// The persisted positive needs a real policy registry chain (drafts, versions,
	// activations) before the task can be Valid; that fixture is not built yet, so
	// this section is skipped instead of asserting a fabricated task.
	t.Skip("persisted-task RPC positive pending a real policy registry fixture")
	taskJob := nativeInternalV3Task(t, f, userID, session.subject, clusterUID, frozen, session.authorizationV)
	taskClaims := graph.Claims{Source: graph.SourceNativeTask, Native: &graph.NativeProof{JobID: taskJob.String()},
		Subject: session.subject, Scope: frozen, GrantSnapshotDigest: graph.ScopeDigest(frozen)}
	if status := nativeInternalPost(t, pki.client, server.URL, sign(taskClaims), payload); status == http.StatusForbidden || status == http.StatusConflict {
		t.Fatalf("persisted task rpc = %d, want it to pass the task authority re-proof", status)
	}
	// Cancelling the task invalidates the persisted authority.
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		UPDATE investigation.jobs SET state='cancelled' WHERE tenant_id=$1 AND job_id=$2
	`, f.tenantA, taskJob)
	if status := nativeInternalPost(t, pki.client, server.URL, sign(taskClaims), payload); status != http.StatusForbidden {
		t.Fatalf("cancelled task rpc = %d, want 403", status)
	}

}

// nativeInternalV3Task persists one complete v3 assistant task whose frozen
// authority is the native operator grant of this account. The deferred assistant
// foreign keys require every row in a single transaction.
func nativeInternalV3Task(t *testing.T, f *nativeMFAHardeningFixture, userID uuid.UUID, subject, clusterUID string, frozen graph.Scope, authorizationVersion int) uuid.UUID {
	t.Helper()
	conversationID := uuid.Must(uuid.NewV7())
	turnID := uuid.Must(uuid.NewV7())
	jobID := uuid.Must(uuid.NewV7())
	messageID := uuid.Must(uuid.NewV7())
	frozenDigest := graph.ScopeDigest(frozen)
	scopeJSON := mustJSON(t, frozen)
	inputDigest := "sha256:" + strings.Repeat("1", 64)
	contextDigest := "sha256:" + strings.Repeat("2", 64)
	binding := map[string]any{
		"conversationId":       conversationID.String(),
		"turnId":               turnID.String(),
		"taskKind":             "resource_troubleshooting",
		"inputDigest":          inputDigest,
		"contextDigest":        contextDigest,
		"grantSnapshot":        json.RawMessage(scopeJSON),
		"grantSnapshotDigest":  frozenDigest,
		"authorizationSource":  "native",
		"authorizationUserId":  userID.String(),
		"authorizationVersion": authorizationVersion,
		"authorizationDomain":  "operational",
		"catalogVersion":       "assistant-tools/v2",
		"promptVersion":        "assistant/v1",
	}
	bindingJSON := mustJSON(t, binding)
	tx, err := f.adminDB.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO investigation.assistant_conversations
			(tenant_id, conversation_id, owner_subject, title, mode, authorization_domain, cluster_uid, binding)
			VALUES ($1,$2,$3,'internal authority','troubleshoot','operational',$4,'{}'::jsonb)`,
			[]any{f.tenantA, conversationID, subject, clusterUID}},
		{`INSERT INTO investigation.assistant_messages
			(tenant_id, message_id, conversation_id, sequence, role, content)
			VALUES ($1,$2,$3,1,'user','{"text":"q"}'::jsonb)`,
			[]any{f.tenantA, messageID, conversationID}},
		{`INSERT INTO investigation.jobs
			(tenant_id, job_id, state, schema_version, requesting_subject, trigger_kind,
			 policy_version, effective_scope, effective_scope_digest, tool_catalog_digest,
			 budget, budget_reserved, budget_consumed, expires_at, assistant_binding)
			VALUES ($1,$2,'running','investigation-job/v3',$3,'manual','policy-v1',$4::jsonb,$5,
			 'catalog-v1','{}'::jsonb,'{}'::jsonb,'{}'::jsonb, clock_timestamp() + interval '30 minutes',$6::jsonb)`,
			[]any{f.tenantA, jobID, subject, scopeJSON, frozenDigest, bindingJSON}},
		{`INSERT INTO investigation.assistant_turns
			(tenant_id, turn_id, conversation_id, job_id, user_message_id, input,
			 input_digest, context_digest, state, base_conversation_revision)
			VALUES ($1,$2,$3,$4,$5,'{"text":"q"}'::jsonb,$6,$7,'running',1)`,
			[]any{f.tenantA, turnID, conversationID, jobID, messageID, inputDigest, contextDigest}},
	}
	for index, statement := range statements {
		if _, err := tx.ExecContext(f.ctx, "SET LOCAL ROLE schema_owner"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(f.ctx, "SELECT set_config('app.tenant_id',$1,true)", f.tenantA.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("task statement %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("task commit: %v", err)
	}
	return jobID
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
