package integration

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

const reviewWorkloadNamespace = "ops-sp03-review-20261001"

func liveWorkloadClient(t *testing.T) *openbao.Client {
	t.Helper()
	address, root, caFile := os.Getenv("SP03_TEST_WORKLOAD_OPENBAO_URL"), os.Getenv("SP03_TEST_WORKLOAD_OPENBAO_TOKEN"), os.Getenv("SP03_TEST_WORKLOAD_OPENBAO_CA_FILE")
	if address == "" || root == "" || caFile == "" {
		t.Skip("isolated Kubernetes TokenReview/OpenBao authorization and environment are required")
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal("read isolated workload bootstrap CA")
	}
	client, err := openbao.NewClient(openbao.ClientConfig{Address: address, ServerName: "localhost", CACertBundle: ca, Token: root, ServiceDomain: reviewWorkloadNamespace + ".svc.cluster.local"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRealProjectedPodTokensAuthenticateOpenBaoAndWorkloadMTLS(t *testing.T) {
	client := liveWorkloadClient(t)
	tokenDir := os.Getenv("SP03_TEST_KUBERNETES_TOKEN_DIR")
	if tokenDir == "" {
		t.Fatal("actual Pod projected token directory is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := client.Configure(ctx); err != nil {
		t.Fatalf("bootstrap isolated OpenBao against real Kubernetes TokenReview: %v", err)
	}
	reloaders := map[string]*openbao.CertReloader{}
	for _, sa := range []string{"ops-api", "ops-investigator", "ops-command-runner"} {
		reloader, err := openbao.NewCertReloader(client, reviewWorkloadNamespace, sa)
		if err != nil {
			t.Fatal(err)
		}
		if err := reloader.Start(ctx, filepath.Join(tokenDir, sa)); err != nil {
			t.Fatalf("actual projected Pod token login for %s: %v", sa, err)
		}
		reloaders[sa] = reloader
	}
	for _, file := range []string{"ops-unauthorized", "other-namespace", "wrong-audience"} {
		jwt, err := os.ReadFile(filepath.Join(tokenDir, file))
		if err != nil {
			t.Fatal("read negative identity Pod token")
		}
		if _, _, err := client.KubernetesLogin(ctx, string(jwt), "ops-api-workload"); err == nil {
			t.Fatalf("TokenReview accepted %s", file)
		}
	}
	serverIdentity := reloaders["ops-api"]
	material, err := serverIdentity.TrustMaterial()
	if err != nil {
		t.Fatal(err)
	}
	allowed := []auth.WorkloadIdentity{}
	for _, sa := range []string{"ops-investigator", "ops-command-runner"} {
		identity, _ := auth.NewWorkloadIdentity(reviewWorkloadNamespace, sa)
		allowed = append(allowed, identity)
	}
	trust := auth.WorkloadTrust{Roots: material.Roots, Intermediates: material.Intermediates, Allowed: allowed, CRLIssuers: material.CRLIssuers, CRLSource: serverIdentity}
	serverTLS, err := auth.WorkloadMTLSServerConfig(ctx, trust)
	if err != nil {
		t.Fatal(err)
	}
	apiTLS, err := serverIdentity.TLSClientConfig("")
	if err != nil {
		t.Fatal(err)
	}
	serverTLS.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return apiTLS.GetClientCertificate(&tls.CertificateRequestInfo{})
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range []string{"Authorization", "X-Vault-Token", "ServiceAccountToken"} {
			if r.Header.Get(header) != "" {
				t.Errorf("internal request forwarded %s", header)
			}
		}
		identity, err := auth.VerifyWorkload(auth.WithWorkloadTrust(r.Context(), trust), r.TLS.PeerCertificates[0])
		if err != nil {
			t.Error(err)
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Workload-Identity", identity.URI)
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	for _, sa := range []string{"ops-investigator", "ops-command-runner"} {
		tlsConfig, err := reloaders[sa].TLSClientConfig("ops-api." + reviewWorkloadNamespace + ".svc.cluster.local")
		if err != nil {
			t.Fatal(err)
		}
		transport := &http.Transport{TLSClientConfig: tlsConfig}
		defer transport.CloseIdleConnections()
		httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, err := httpClient.Get(server.URL)
		if err != nil {
			t.Fatalf("real OpenBao mTLS handshake for %s: %v", sa, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent || response.Header.Get("Workload-Identity") != "spiffe://ops.local/ns/"+reviewWorkloadNamespace+"/sa/"+sa {
			t.Fatal("internal mTLS identity mismatch")
		}
	}
	// A user bearer value without a workload certificate cannot complete TLS.
	roots := apiTLS.RootCAs
	unauthenticated := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "ops-api." + reviewWorkloadNamespace + ".svc.cluster.local", MinVersion: tls.VersionTLS13}}, Timeout: 5 * time.Second}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	request.Header.Set("Authorization", "Bearer user-token-cannot-substitute")
	if response, err := unauthenticated.Do(request); err == nil {
		response.Body.Close()
		t.Fatal("bearer-only request completed workload mTLS")
	}
	t.Log("real Pod-bound TokenReview accepted three workload identities, denied unauthorized SA/namespace/audience; Investigator and test Runner mTLS completed without forwarded JWT")
}
