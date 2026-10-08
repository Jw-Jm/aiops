package openbao

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCertReloaderKeepsPrivateKeyInMemoryAndRotatesWithOpenBaoLogin(t *testing.T) {
	var signCount atomic.Int32
	var loginCount atomic.Int32
	workloadCA, workloadKey := makeTestCA(t)
	server, client := newPKITestServer(t, workloadCA, workloadKey, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/kubernetes/login":
			if r.Header.Get("X-Vault-Token") != "" || r.Header.Get("Authorization") != "" {
				t.Errorf("bootstrap token was sent to the login endpoint")
			}
			var body struct {
				JWT  string `json:"jwt"`
				Role string `json:"role"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.JWT != "projected-service-account-token" || body.Role != "ops-api-workload" {
				t.Errorf("unexpected OpenBao login identity")
			}
			loginCount.Add(1)
			writeJSON(w, map[string]any{"auth": map[string]any{"client_token": "short-lived-openbao-token", "lease_duration": 900}})
		case "/v1/pki/crl/pem":
			if r.Header.Get("X-Vault-Token") != "" {
				t.Error("projected or OpenBao token was sent to the public PKI CRL endpoint")
			}
			_, _ = w.Write(testCRLPEM(t, workloadCA, workloadKey))
		case "/v1/pki/sign/platform-workload-ops-api":
			if r.Header.Get("X-Vault-Token") != "short-lived-openbao-token" {
				t.Errorf("PKI sign request did not use the short-lived OpenBao token")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			csrPEM, _ := body["csr"].(string)
			block, _ := pem.Decode([]byte(csrPEM))
			if block == nil || block.Type != "CERTIFICATE REQUEST" {
				t.Error("CSR was missing")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			csr, err := x509.ParseCertificateRequest(block.Bytes)
			if err != nil || csr.CheckSignature() != nil {
				t.Errorf("invalid CSR: %v", err)
			}
			if len(csr.URIs) != 1 || csr.URIs[0].String() != "spiffe://ops.local/ns/ops-system/sa/ops-api" {
				t.Errorf("CSR did not bind the exact SPIFFE identity")
			}
			if len(csr.DNSNames) != 1 || csr.DNSNames[0] != "ops-api.ops-system.svc.cluster.local" {
				t.Errorf("CSR did not bind the exact service DNS name")
			}
			if _, ok := body["private_key"]; ok {
				t.Error("private key was sent to OpenBao")
			}
			count := signCount.Add(1)
			certificatePEM := signCSRForTest(t, workloadCA, workloadKey, csr, count, 45*time.Minute)
			writeJSON(w, map[string]any{"data": map[string]any{
				"certificate": certificatePEM,
				"issuing_ca":  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: workloadCA.Raw})),
				"ca_chain":    []string{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: workloadCA.Raw}))},
			}})
		default:
			t.Errorf("unexpected OpenBao route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	_ = client

	reloader, err := NewCertReloader(client, "ops-system", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := writeProjectedToken(t)
	if err := reloader.refresh(context.Background(), tokenPath); err != nil {
		t.Fatal(err)
	}
	first, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Leaf == nil || first.PrivateKey == nil || loginCount.Load() != 1 || signCount.Load() != 1 {
		t.Fatal("initial workload certificate was not installed")
	}
	if err := reloader.refresh(context.Background(), tokenPath); err != nil {
		t.Fatal(err)
	}
	second, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil || second.Leaf == nil || first.Leaf.SerialNumber.Cmp(second.Leaf.SerialNumber) == 0 {
		t.Fatal("successful rotation did not replace the active certificate")
	}
	if loginCount.Load() != 2 || signCount.Load() != 2 {
		t.Fatal("rotation did not reauthenticate with the projected token")
	}
	serviceTLSCert := signInternalServerCert(t, workloadCA, workloadKey)
	service := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Vault-Token") != "" || strings.Contains(r.URL.RawQuery, "projected-service-account-token") {
			t.Error("projected ServiceAccount or OpenBao token was forwarded to an internal service")
		}
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.PeerCertificates[0].URIs) != 1 || r.TLS.PeerCertificates[0].URIs[0].String() != "spiffe://ops.local/ns/ops-system/sa/ops-api" {
			t.Error("internal request did not carry the rotated workload mTLS certificate")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	service.TLS = &tls.Config{Certificates: []tls.Certificate{serviceTLSCert}, ClientAuth: tls.RequireAnyClientCert}
	service.StartTLS()
	defer service.Close()
	clientTLS, err := reloader.TLSClientConfig("ops-api.ops-system.svc.cluster.local")
	if err != nil {
		t.Fatal(err)
	}
	serviceClient := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: 5 * time.Second}
	response, err := serviceClient.Get(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("internal mTLS request returned %d", response.StatusCode)
	}
}

func TestCertReloaderRetainsValidCertificateAfterFailedRotationAndFailsClosedAtExpiry(t *testing.T) {
	var failSign atomic.Bool
	workloadCA, workloadKey := makeTestCA(t)
	server, client := newPKITestServer(t, workloadCA, workloadKey, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/kubernetes/login":
			writeJSON(w, map[string]any{"auth": map[string]any{"client_token": "rotation-token", "lease_duration": 900}})
		case "/v1/pki/crl/pem":
			_, _ = w.Write(testCRLPEM(t, workloadCA, workloadKey))
		case "/v1/pki/sign/platform-workload-ops-api":
			if failSign.Load() {
				http.Error(w, "signing unavailable", http.StatusServiceUnavailable)
				return
			}
			var body struct {
				CSR string `json:"csr"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			block, _ := pem.Decode([]byte(body.CSR))
			csr, err := x509.ParseCertificateRequest(block.Bytes)
			if err != nil {
				t.Error(err)
			}
			certificatePEM := signCSRForTest(t, workloadCA, workloadKey, csr, 12, time.Minute)
			writeJSON(w, map[string]any{"data": map[string]any{
				"certificate": certificatePEM,
				"issuing_ca":  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: workloadCA.Raw})),
				"ca_chain":    []string{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: workloadCA.Raw}))},
			}})
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	reloader, err := NewCertReloader(client, "ops-system", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	if err := reloader.refresh(context.Background(), writeProjectedToken(t)); err != nil {
		t.Fatal(err)
	}
	old, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	failSign.Store(true)
	if err := reloader.refresh(context.Background(), writeProjectedToken(t)); err == nil {
		t.Fatal("failed PKI renewal was reported as successful")
	}
	current, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil || current.Leaf.SerialNumber.Cmp(old.Leaf.SerialNumber) != 0 {
		t.Fatal("a failed rotation replaced or discarded the still-valid certificate")
	}
	reloader.now = func() time.Time { return old.Leaf.NotAfter.Add(time.Second) }
	if _, err := reloader.getCertificate(&tls.CertificateRequestInfo{}); err == nil {
		t.Fatal("expired workload certificate was served after renewal failure")
	}
}

func TestCertReloaderRefreshesCRLAndFailsClosedWhenStaleOrRevoked(t *testing.T) {
	var revoked atomic.Bool
	workloadCA, workloadKey := makeTestCA(t)
	server, client := newPKITestServer(t, workloadCA, workloadKey, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/kubernetes/login":
			writeJSON(w, map[string]any{"auth": map[string]any{"client_token": "rotation-token", "lease_duration": 900}})
		case "/v1/pki/crl/pem":
			serials := []*big.Int{}
			if revoked.Load() {
				serials = append(serials, big.NewInt(12))
			}
			_, _ = w.Write(testCRLPEMWithSerials(t, workloadCA, workloadKey, serials))
		case "/v1/pki/sign/platform-workload-ops-api":
			var body struct {
				CSR string `json:"csr"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			block, _ := pem.Decode([]byte(body.CSR))
			csr, err := x509.ParseCertificateRequest(block.Bytes)
			if err != nil {
				t.Error(err)
			}
			certificatePEM := signCSRForTest(t, workloadCA, workloadKey, csr, 12, 45*time.Minute)
			writeJSON(w, map[string]any{"data": map[string]any{
				"certificate": certificatePEM,
				"issuing_ca":  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: workloadCA.Raw})),
				"ca_chain":    []string{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: workloadCA.Raw}))},
			}})
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	reloader, err := NewCertReloader(client, "ops-system", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	if err := reloader.refresh(context.Background(), writeProjectedToken(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := reloader.getCertificate(&tls.CertificateRequestInfo{}); err != nil {
		t.Fatalf("fresh CRL did not allow current certificate: %v", err)
	}
	reloader.now = func() time.Time { return time.Now().UTC().Add(6 * time.Minute) }
	if _, err := reloader.getCertificate(&tls.CertificateRequestInfo{}); err == nil {
		t.Fatal("served a workload certificate while the CRL was stale")
	}
	reloader.now = func() time.Time { return time.Now().UTC() }
	revoked.Store(true)
	if err := reloader.refreshCRL(context.Background()); err == nil {
		t.Fatal("reported a newly revoked certificate as active")
	}
	if _, err := reloader.getCertificate(&tls.CertificateRequestInfo{}); err == nil {
		t.Fatal("served a workload certificate after the CRL revoked it")
	}
}

func TestCertReloaderRejectsWrongOpenBaoBootstrapCA(t *testing.T) {
	workloadCA, workloadKey := makeTestCA(t)
	server, client := newPKITestServer(t, workloadCA, workloadKey, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"auth": map[string]any{"client_token": "never-reached", "lease_duration": 900}})
	})
	defer server.Close()
	wrongRoot := makeRoot(t, "wrong root")
	badClient, err := NewClient(ClientConfig{Address: server.URL, ServerName: "localhost", CACertBundle: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: wrongRoot.Raw})})
	if err != nil {
		t.Fatal(err)
	}
	reloader, err := NewCertReloader(badClient, "ops-system", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	if err := reloader.refresh(context.Background(), writeProjectedToken(t)); err == nil {
		t.Fatal("OpenBao was contacted without the installed bootstrap CA")
	}
	_ = client
}

func newPKITestServer(t *testing.T, workloadCA *x509.Certificate, workloadKey *ecdsa.PrivateKey, handler http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	bootstrapCA, bootstrapKey := makeTestCA(t)
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	serverTemplate := &x509.Certificate{SerialNumber: big.NewInt(9921), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, bootstrapCA, &serverKey.PublicKey, bootstrapKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	serverTLSCert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: serverKeyDER}))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{serverTLSCert}}
	server.StartTLS()
	client, err := NewClient(ClientConfig{Address: server.URL, ServerName: "localhost", CACertBundle: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: bootstrapCA.Raw})})
	if err != nil {
		t.Fatal(err)
	}
	return server, client
}

func makeTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(4401), Subject: pkix.Name{CommonName: "workload test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func TestPinnedWorkloadIssuerDriftCannotReplaceActiveIdentity(t *testing.T) {
	ca, key := makeTestCA(t)
	other, otherKey := makeTestCA(t)
	var drift atomic.Bool
	server, client := newPKITestServer(t, ca, key, func(w http.ResponseWriter, r *http.Request) {
		issuer, signer := ca, key
		if drift.Load() {
			issuer, signer = other, otherKey
		}
		switch r.URL.Path {
		case "/v1/auth/kubernetes/login":
			writeJSON(w, map[string]any{"auth": map[string]any{"client_token": "ephemeral", "lease_duration": 900}})
		case "/v1/pki/sign/platform-workload-ops-api":
			var body struct {
				CSR string `json:"csr"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			block, _ := pem.Decode([]byte(body.CSR))
			csr, err := x509.ParseCertificateRequest(block.Bytes)
			if err != nil {
				t.Error(err)
				return
			}
			issuerPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuer.Raw}))
			writeJSON(w, map[string]any{"data": map[string]any{"certificate": signCSRForTest(t, issuer, signer, csr, 99, time.Hour), "issuing_ca": issuerPEM, "ca_chain": []string{issuerPEM}}})
		case "/v1/pki/crl/pem":
			_, _ = w.Write(testCRLPEM(t, issuer, signer))
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	reloader, err := NewCertReloader(client, "ops-system", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	token := writeProjectedToken(t)
	if err := reloader.StartPinned(ctx, token, roots); err != nil {
		t.Fatal(err)
	}
	first, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	drift.Store(true)
	if err := reloader.refresh(ctx, token); err == nil || !strings.Contains(err.Error(), "PINNED_TRUST") {
		t.Fatalf("untrusted issuer renewal accepted: %v", err)
	}
	active, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil || active != first {
		t.Fatal("failed renewal discarded the valid pinned identity")
	}
	otherReloader, _ := NewCertReloader(client, "ops-system", "ops-api")
	if err := otherReloader.StartPinned(ctx, token, roots); err == nil || otherReloader.current.Load() != nil {
		t.Fatal("untrusted initial issuer was published")
	}
}

func signCSRForTest(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, csr *x509.CertificateRequest, serial int32, lifetime time.Duration) string {
	t.Helper()
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(int64(serial)), Subject: csr.Subject, DNSNames: csr.DNSNames, URIs: csr.URIs, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(lifetime), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, csr.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func signInternalServerCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	identity, _ := url.Parse("spiffe://ops.local/ns/ops-system/sa/ops-api")
	template := &x509.Certificate{SerialNumber: big.NewInt(883), Subject: pkix.Name{CommonName: "ops-api.ops-system"}, DNSNames: []string{"ops-api.ops-system.svc.cluster.local"}, URIs: []*url.URL{identity}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func testCRLPEM(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) []byte {
	return testCRLPEMWithSerials(t, ca, caKey, nil)
}

func testCRLPEMWithSerials(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, serials []*big.Int) []byte {
	t.Helper()
	now := time.Now().UTC()
	revoked := make([]x509.RevocationListEntry, 0, len(serials))
	for _, serial := range serials {
		revoked = append(revoked, x509.RevocationListEntry{SerialNumber: serial, RevocationTime: now.Add(-time.Second)})
	}
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(1), ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour), RevokedCertificateEntries: revoked}, ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der})
}

func makeRoot(t *testing.T, name string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(4411), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func writeProjectedToken(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("projected-service-account-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
