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
	"sync/atomic"
	"testing"
	"time"
)

// The rotation loop keeps the revocation list independent of the renewal: a
// failed login or a failed signing must not stop a reachable CRL from being
// applied to the still-valid leaf, and the failure must never replace the
// valid certificate.
func TestRotationLoopRefreshesCRLWhenRenewalFails(t *testing.T) {
	var failLogin, failSign atomic.Bool
	var crlFetches atomic.Int64
	workloadCA, workloadKey := makeTestCA(t)
	server, client := newPKITestServer(t, workloadCA, workloadKey, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/kubernetes/login":
			if failLogin.Load() {
				http.Error(w, "login unavailable", http.StatusServiceUnavailable)
				return
			}
			writeJSON(w, map[string]any{"auth": map[string]any{"client_token": "rotation-token", "lease_duration": 900}})
		case "/v1/pki/crl/pem":
			crlFetches.Add(1)
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
	token := writeProjectedToken(t)
	if err := reloader.refresh(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	issued, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	// The renewal is due and the signing fails: the revocation list must still
	// be refreshed and the valid leaf must stay in place.
	reloader.now = func() time.Time { return issued.Leaf.NotAfter.Add(-time.Second) }
	reloader.refreshInterval = 5 * time.Millisecond
	failSign.Store(true)
	before := crlFetches.Load()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reloader.rotationLoop(ctx, token)
		close(done)
	}()
	waitFor(t, func() bool { return crlFetches.Load() > before })
	current, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil || current.Leaf.SerialNumber.Cmp(issued.Leaf.SerialNumber) != 0 {
		t.Fatal("a failed renewal replaced or discarded the still-valid certificate")
	}
	// The same holds when the login itself fails.
	failLogin.Store(true)
	before = crlFetches.Load()
	waitFor(t, func() bool { return crlFetches.Load() > before })
	current, err = reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil || current.Leaf.SerialNumber.Cmp(issued.Leaf.SerialNumber) != 0 {
		t.Fatal("a failed login replaced or discarded the still-valid certificate")
	}
	// The loop stops with the context.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the rotation loop did not stop")
	}
}

// When the renewal works again, the loop issues a new certificate and the
// revocation list keeps being refreshed.
func TestRotationLoopRecoversAfterRenewalFailure(t *testing.T) {
	var failSign atomic.Bool
	var crlFetches atomic.Int64
	var issuedCount atomic.Int64
	workloadCA, workloadKey := makeTestCA(t)
	server, client := newPKITestServer(t, workloadCA, workloadKey, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/kubernetes/login":
			writeJSON(w, map[string]any{"auth": map[string]any{"client_token": "rotation-token", "lease_duration": 900}})
		case "/v1/pki/crl/pem":
			crlFetches.Add(1)
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
			certificatePEM := signCSRForTest(t, workloadCA, workloadKey, csr, int32(100+issuedCount.Add(1)), time.Minute)
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
	token := writeProjectedToken(t)
	if err := reloader.refresh(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	issued, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	reloader.now = func() time.Time { return issued.Leaf.NotAfter.Add(-time.Second) }
	reloader.refreshInterval = 5 * time.Millisecond
	failSign.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reloader.rotationLoop(ctx, token)
	waitFor(t, func() bool {
		current, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
		return err == nil && current.Leaf.SerialNumber.Cmp(issued.Leaf.SerialNumber) == 0
	})
	// The signing recovers: the loop issues a new certificate.
	failSign.Store(false)
	var lastErr error
	var lastSerial *big.Int
	recovered := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		current, err := reloader.getCertificate(&tls.CertificateRequestInfo{})
		lastErr = err
		if err == nil {
			lastSerial = current.Leaf.SerialNumber
			if current.Leaf.SerialNumber.Cmp(issued.Leaf.SerialNumber) != 0 {
				recovered = true
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !recovered {
		t.Fatalf("the renewal did not recover: err=%v serial=%v issued=%v", lastErr, lastSerial, issued.Leaf.SerialNumber)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the expected state was not reached")
}

// makeTestCA builds a small issuing authority for the loopback fixture.
func makeLoopbackCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(4242),
		Subject:               pkix.Name{CommonName: "loopback workload CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key
}
