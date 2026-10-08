//go:build pre_sp07_live

package app

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ops-platform/internal/auth"
	"ops-platform/internal/integrations/openbao"
)

// This opt-in gate requires an actual, isolated persistent OpenBao initialized
// by opsctl and actual Kubernetes TokenRequest identities. It has no fixture
// transport and no test refresh thread; the production lifecycle owns refresh.
func TestLiveRuntimePKIRotationAndRevocation(t *testing.T) {
	directory := os.Getenv("PRE_SP07_PKI_DIRECTORY")
	namespace := os.Getenv("PRE_SP07_PKI_NAMESPACE")
	if directory == "" || namespace == "" {
		t.Fatal("explicit isolated PKI inputs required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	configuration := SP04Config{IdentityMode: "openbao-kubernetes", Namespace: namespace, CAFile: filepath.Join(directory, "workload-ca.pem"), ServerName: "ops-worker." + namespace + ".svc.cluster.local"}
	t.Setenv("OPENBAO_PROJECTED_TOKEN_FILE", filepath.Join(directory, "ops-worker.token"))
	serverTLS, serverTrust, err := configuration.tls(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENBAO_PROJECTED_TOKEN_FILE", filepath.Join(directory, "ops-api.token"))
	clientTLS, _, err := configuration.tls(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := auth.VerifyWorkload(auth.WithWorkloadTrust(ctx, serverTrust), r.TLS.PeerCertificates[0]); err != nil {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	transport := &http.Transport{TLSClientConfig: clientTLS, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	check := func() {
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 204 {
			t.Fatalf("authorized mTLS returned %d", response.StatusCode)
		}
	}
	check()
	first, err := clientTLS.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	_, fetched := serverTrust.CRLSource.CurrentWorkloadCRL()
	deadline := time.Now().Add(150 * time.Second)
	rotated := false
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
		active, err := clientTLS.GetClientCertificate(&tls.CertificateRequestInfo{})
		if err == nil && active.Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) != 0 {
			rotated = true
			break
		}
	}
	if !rotated {
		t.Fatal("production PKI lifecycle did not renew the live bounded-TTL certificate")
	}
	_, refreshed := serverTrust.CRLSource.CurrentWorkloadCRL()
	if !refreshed.After(fetched) {
		t.Fatal("production CRL lifecycle did not refresh")
	}
	check()
	active, err := clientTLS.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(os.Getenv("OPENBAO_CA_FILE"))
	if err != nil {
		t.Fatal("isolated operator CA unavailable")
	}
	recovery, err := openbao.ReadExternalRecoveryMaterial(filepath.Join(directory, "recovery.json"), "../..", "../../bundle")
	if err != nil {
		t.Fatal("isolated operator material unavailable")
	}
	operator, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("OPENBAO_ADDR"), ServerName: os.Getenv("OPENBAO_SERVER_NAME"), CACertBundle: ca, Token: recovery.RootToken, ServiceDomain: namespace + ".svc.cluster.local"})
	if err != nil || operator.RevokeWorkloadCertificate(ctx, active.Leaf.SerialNumber) != nil {
		t.Fatal("isolated certificate revocation failed")
	}
	deadline = time.Now().Add(75 * time.Second)
	rejected := false
	for time.Now().Before(deadline) {
		if _, err := auth.VerifyWorkload(auth.WithWorkloadTrust(ctx, serverTrust), active.Leaf); err != nil {
			rejected = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if !rejected {
		t.Fatal("production CRL refresh did not reject the revoked leaf")
	}
	if response, err := client.Get(server.URL); err == nil {
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal("revoked identity still reached authorized HTTP")
		}
	}
	t.Log("actual Kubernetes auth, in-process OpenBao leaf renewal and CRL refresh, positive mTLS before/after renewal, and live revocation rejection passed")
}
