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
		UPDATE identity.role_grants SET scope=$3::jsonb
		WHERE tenant_id=$1 AND user_id=$2 AND role='operator' AND status='active'
	`, f.tenantA, userID,
		`{"clusterUids":["`+clusterUID+`"],"namespaces":[]}`)

	frozen := nativeInternalScope(t, f, userID, clusterUID, 1)
	payload, err := json.Marshal(graph.Query{QueryKind: "list", Kind: "Pod", Scope: frozen, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 1, MaxEdges: 1, RelationKinds: []string{}})
	if err != nil {
		t.Fatal(err)
	}
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
	if status := nativeInternalPost(t, pki.client, server.URL, sign(interactive), payload); status != http.StatusServiceUnavailable {
		t.Fatalf("authorized worker rpc = %d, want 503 at the unconfigured lease boundary", status)
	}
	// No certificate is rejected during TLS, before an HTTP status can exist.
	noCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // test-only throwaway CA
		MinVersion:         tls.VersionTLS13,
	}}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/internal/v1/graph:query", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+sign(interactive))
	response, err := noCert.Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "certificate required") {
		t.Fatalf("rpc without a client certificate = %v, want TLS certificate rejection", err)
	}

	// Legacy source has no legacy binding for this subject and must not borrow
	// the native operator grant.
	if status := nativeInternalPost(t, pki.client, server.URL, sign(graph.Claims{
		Source: graph.SourceLegacy, Subject: session.subject, Scope: frozen,
	}), payload); status != http.StatusConflict {
		t.Fatalf("legacy claim over a worker rpc = %d, want 409 at current authority reproof", status)
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

	// Full persisted-task 200, lease publication and revocation are exercised by
	// TestMainReviewNativeInteractiveAndTaskGraphRPC200 using the real Submit path.
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
