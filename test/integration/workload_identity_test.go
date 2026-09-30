package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/url"
	"os"
	"testing"
	"time"

	"ops-platform/internal/integrations/openbao"
)

func TestRealOpenBaoWorkloadPKIIssuanceAndRevocation(t *testing.T) {
	address := os.Getenv("SP03_TEST_OPENBAO_URL")
	token := os.Getenv("SP03_TEST_OPENBAO_TOKEN")
	caPath := os.Getenv("SP03_TEST_OPENBAO_CA_FILE")
	if address == "" || token == "" || caPath == "" {
		t.Skip("isolated OpenBao test endpoint, token, and bootstrap CA are required")
	}
	caBundle, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal("read isolated OpenBao bootstrap CA")
	}
	client, err := openbao.NewClient(openbao.ClientConfig{Address: address, ServerName: "localhost", CACertBundle: caBundle, Token: token, ServiceDomain: "ops-system.svc.cluster.local"})
	if err != nil {
		t.Fatalf("initialize isolated OpenBao client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := client.ConfigureWorkloadPKI(ctx); err != nil {
		t.Fatalf("configure workload roles in isolated OpenBao: %v", err)
	}
	if err := client.ConfigureWorkloadAuthentication(ctx); err != nil {
		t.Fatalf("configure ServiceAccount-bound auth roles and policies in isolated OpenBao: %v", err)
	}
	const namespace, serviceAccount = "ops-system", "ops-api"
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("generate in-process workload private key")
	}
	csr := workloadCSR(t, privateKey, namespace, serviceAccount)
	certificate, err := client.SignWorkloadCSR(ctx, namespace, serviceAccount, csr, privateKey)
	if err != nil {
		t.Fatalf("sign workload CSR through isolated OpenBao: %v", err)
	}
	if certificate.Leaf.NotAfter.Sub(certificate.Leaf.NotBefore) > time.Hour+time.Minute || len(certificate.Leaf.URIs) != 1 || certificate.Leaf.URIs[0].String() != "spiffe://ops.local/ns/ops-system/sa/ops-api" {
		t.Fatal("OpenBao issued a workload certificate with an unexpected identity or lifetime")
	}
	if len(certificate.Certificate) < 2 {
		t.Fatal("OpenBao workload certificate response omitted its issuer chain")
	}
	issuer, err := x509.ParseCertificate(certificate.Certificate[1])
	if err != nil {
		t.Fatal("parse OpenBao workload issuer")
	}
	crl, err := client.ReadWorkloadCRL(ctx)
	if err != nil || crl.CheckSignatureFrom(issuer) != nil {
		t.Fatalf("read and verify the isolated OpenBao PKI CRL: %v", err)
	}
	if err := client.RevokeWorkloadCertificate(ctx, certificate.Leaf.SerialNumber); err != nil {
		t.Fatalf("revoke workload certificate in isolated OpenBao: %v", err)
	}
	crl, err = client.ReadWorkloadCRL(ctx)
	if err != nil || crl.CheckSignatureFrom(issuer) != nil {
		t.Fatalf("read updated isolated OpenBao CRL: %v", err)
	}
	revoked := false
	for _, entry := range crl.RevokedCertificateEntries {
		if entry.SerialNumber.Cmp(certificate.Leaf.SerialNumber) == 0 {
			revoked = true
			break
		}
	}
	if !revoked {
		t.Fatal("updated OpenBao CRL did not include the revoked workload serial")
	}
	wrongNamespaceCSR := workloadCSR(t, privateKey, "other", serviceAccount)
	if _, err := client.SignWorkloadCSR(ctx, namespace, serviceAccount, wrongNamespaceCSR, privateKey); err == nil {
		t.Fatal("workload signing accepted a CSR with a different namespace SAN")
	}
	t.Logf("isolated OpenBao workload role=platform-workload-ops-api certificate_serial=%s cert_ttl=%s revoked_crl_verified=true", certificate.Leaf.SerialNumber.Text(16), certificate.Leaf.NotAfter.Sub(certificate.Leaf.NotBefore).Round(time.Second))
}

func workloadCSR(t *testing.T, privateKey *ecdsa.PrivateKey, namespace, serviceAccount string) []byte {
	t.Helper()
	dnsName := serviceAccount + "." + namespace + ".svc.cluster.local"
	identity, err := url.Parse("spiffe://ops.local/ns/" + namespace + "/sa/" + serviceAccount)
	if err != nil {
		t.Fatal(err)
	}
	request := &x509.CertificateRequest{
		DNSNames: []string{dnsName},
		URIs:     []*url.URL{identity},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, request, privateKey)
	if err != nil {
		t.Fatal("create workload CSR")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}
