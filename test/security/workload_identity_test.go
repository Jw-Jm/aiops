package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ops-platform/internal/auth"
)

func TestWorkloadIdentityRejectsWrongSANNamespaceAndServiceAccount(t *testing.T) {
	root, issuer := newWorkloadIssuer(t)
	allowed, err := auth.NewWorkloadIdentity("ops-system", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ctx := auth.WithWorkloadTrust(t.Context(), auth.WorkloadTrust{
		Roots: root, Allowed: []auth.WorkloadIdentity{allowed},
		RevocationList: issueWorkloadCRL(t, issuer, big.NewInt(99999), now), CRLFetchedAt: now,
		CRLIssuers: []*x509.Certificate{issuer.certificate}, Now: func() time.Time { return now },
	})

	for _, tc := range []struct {
		name      string
		namespace string
		service   string
	}{
		{name: "wrong namespace", namespace: "other", service: "ops-api"},
		{name: "wrong service account", namespace: "ops-system", service: "untrusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := issueWorkloadCert(t, issuer, tc.namespace, tc.service, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
			if _, err := auth.VerifyWorkload(ctx, peer); err == nil {
				t.Fatal("unexpectedly accepted a certificate outside the exact workload identity allowlist")
			}
		})
	}

	wrongSAN := issueWorkloadCertWithURI(t, issuer, mustURI(t, "spiffe://ops.local/ns/ops-system/sa/ops-worker"), time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if _, err := auth.VerifyWorkload(ctx, wrongSAN); err == nil {
		t.Fatal("unexpectedly accepted a mismatched SPIFFE SAN")
	}
	wrongDNS := issueWorkloadCertWithDNS(t, issuer, "ops-system", "ops-api", "untrusted.example", now.Add(-time.Minute), now.Add(time.Hour))
	if _, err := auth.VerifyWorkload(ctx, wrongDNS); err == nil {
		t.Fatal("unexpectedly accepted a workload certificate with a mismatched DNS SAN")
	}
}

func TestWorkloadIdentityRejectsExpiredRevokedAndUntrustedCertificate(t *testing.T) {
	root, issuer := newWorkloadIssuer(t)
	allowed, err := auth.NewWorkloadIdentity("ops-system", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expired := issueWorkloadCert(t, issuer, "ops-system", "ops-api", now.Add(-2*time.Hour), now.Add(-time.Hour))
	currentCRL := issueWorkloadCRL(t, issuer, big.NewInt(99999), now)
	ctx := auth.WithWorkloadTrust(t.Context(), auth.WorkloadTrust{
		Roots: root, Allowed: []auth.WorkloadIdentity{allowed}, RevocationList: currentCRL,
		CRLFetchedAt: now, CRLIssuers: []*x509.Certificate{issuer.certificate}, Now: func() time.Time { return now },
	})
	if _, err := auth.VerifyWorkload(ctx, expired); err == nil {
		t.Fatal("unexpectedly accepted an expired workload certificate")
	}

	valid := issueWorkloadCert(t, issuer, "ops-system", "ops-api", now.Add(-time.Minute), now.Add(time.Hour))
	crl := issueWorkloadCRL(t, issuer, valid.SerialNumber, now)
	revocationCtx := auth.WithWorkloadTrust(t.Context(), auth.WorkloadTrust{Roots: root, Allowed: []auth.WorkloadIdentity{allowed}, RevocationList: crl, CRLFetchedAt: now, Now: func() time.Time { return now }})
	if _, err := auth.VerifyWorkload(revocationCtx, valid); err == nil {
		t.Fatal("unexpectedly accepted a revoked workload certificate")
	}

	_, untrustedRoot := newWorkloadIssuer(t)
	otherIssuer := issueWorkloadCert(t, untrustedRoot, "ops-system", "ops-api", now.Add(-time.Minute), now.Add(time.Hour))
	if _, err := auth.VerifyWorkload(ctx, otherIssuer); err == nil {
		t.Fatal("unexpectedly accepted a certificate outside the bootstrap PKI trust chain")
	}
}

func TestUserTokenCannotEstablishWorkloadIdentity(t *testing.T) {
	root, issuer := newWorkloadIssuer(t)
	peer := issueWorkloadCert(t, issuer, "ops-system", "ops-api", time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if _, err := auth.VerifyWorkload(t.Context(), peer); err == nil {
		t.Fatal("a peer certificate must not be trusted without server-configured workload trust")
	}
	_ = root
}

func TestWorkloadMTLSRequiresExactIdentityAndNeverUsesBearerToken(t *testing.T) {
	root, issuer := newWorkloadIssuer(t)
	allowed, err := auth.NewWorkloadIdentity("ops-system", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	crl := issueWorkloadCRL(t, issuer, big.NewInt(99999), now)
	trust := auth.WorkloadTrust{Roots: root, Allowed: []auth.WorkloadIdentity{allowed}, RevocationList: crl, CRLFetchedAt: now, Now: func() time.Time { return now }}
	serverTLS, err := auth.WorkloadMTLSServerConfig(t.Context(), trust)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS.Certificates = []tls.Certificate{issueWorkloadServerCert(t, issuer)}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Vault-Token") != "" || r.Header.Get("ServiceAccountToken") != "" {
			t.Error("workload credential was forwarded as a bearer token")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	clientCert := issueWorkloadClientCert(t, issuer, "ops-system", "ops-api")
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: root.Clone(), ServerName: "localhost", Certificates: []tls.Certificate{clientCert}}}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("exact workload identity mTLS request failed: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("mTLS request status = %d", response.StatusCode)
	}

	wrongIdentityCert := issueWorkloadClientCert(t, issuer, "other", "ops-api")
	wrongClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: root.Clone(), ServerName: "localhost", Certificates: []tls.Certificate{wrongIdentityCert}}}, Timeout: 5 * time.Second}
	if _, err := wrongClient.Get(server.URL); err == nil {
		t.Fatal("mTLS endpoint accepted a valid certificate from an unapproved namespace")
	}
}

func TestWorkloadIdentityUsesFreshCRLSourceAndFailsClosedWhenStale(t *testing.T) {
	root, issuer := newWorkloadIssuer(t)
	allowed, err := auth.NewWorkloadIdentity("ops-system", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	peer := issueWorkloadCert(t, issuer, "ops-system", "ops-api", now.Add(-time.Minute), now.Add(time.Hour))
	source := &mutableWorkloadCRL{crl: issueWorkloadCRL(t, issuer, big.NewInt(99999), now), fetchedAt: now}
	ctx := auth.WithWorkloadTrust(t.Context(), auth.WorkloadTrust{
		Roots: root, Allowed: []auth.WorkloadIdentity{allowed}, CRLSource: source,
		CRLIssuers: []*x509.Certificate{issuer.certificate}, Now: func() time.Time { return now },
	})
	if _, err := auth.VerifyWorkload(ctx, peer); err != nil {
		t.Fatalf("fresh dynamic CRL rejected valid workload: %v", err)
	}
	source.fetchedAt = now.Add(-6 * time.Minute)
	if _, err := auth.VerifyWorkload(ctx, peer); err == nil {
		t.Fatal("accepted a workload while its CRL source was stale")
	}
	source.crl = issueWorkloadCRL(t, issuer, peer.SerialNumber, now)
	source.fetchedAt = now
	if _, err := auth.VerifyWorkload(ctx, peer); err == nil {
		t.Fatal("accepted a certificate revoked by the refreshed CRL")
	}
}

type mutableWorkloadCRL struct {
	crl       *x509.RevocationList
	fetchedAt time.Time
}

func (s *mutableWorkloadCRL) CurrentWorkloadCRL() (*x509.RevocationList, time.Time) {
	return s.crl, s.fetchedAt
}

type workloadIssuer struct {
	certificate *x509.Certificate
	privateKey  *ecdsa.PrivateKey
}

func newWorkloadIssuer(t *testing.T) (*x509.CertPool, workloadIssuer) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(9001), Subject: pkix.Name{CommonName: "test workload issuer"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool, workloadIssuer{certificate: cert, privateKey: key}
}

func issueWorkloadCert(t *testing.T, issuer workloadIssuer, namespace, serviceAccount string, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	return issueWorkloadCertWithDNS(t, issuer, namespace, serviceAccount, serviceAccount+"."+namespace+".svc.cluster.local", notBefore, notAfter)
}

func issueWorkloadCertWithURI(t *testing.T, issuer workloadIssuer, identity *url.URL, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(identity.Path, "/"), "/")
	if len(parts) != 4 {
		t.Fatal("invalid test workload URI")
	}
	return issueWorkloadCertWithDNS(t, issuer, parts[1], parts[3], parts[3]+"."+parts[1]+".svc.cluster.local", notBefore, notAfter)
}

func issueWorkloadCertWithDNS(t *testing.T, issuer workloadIssuer, namespace, serviceAccount, dnsName string, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	identity := mustURI(t, "spiffe://ops.local/ns/"+namespace+"/sa/"+serviceAccount)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: identity.String()}, DNSNames: []string{dnsName}, URIs: []*url.URL{identity}, NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer.certificate, &key.PublicKey, issuer.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func issueWorkloadCRL(t *testing.T, issuer workloadIssuer, serial *big.Int, now time.Time) *x509.RevocationList {
	t.Helper()
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(1), ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour), RevokedCertificateEntries: []x509.RevocationListEntry{{SerialNumber: serial, RevocationTime: now.Add(-time.Second)}}}, issuer.certificate, issuer.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	list, err := x509.ParseRevocationList(der)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func issueWorkloadClientCert(t *testing.T, issuer workloadIssuer, namespace, serviceAccount string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri := mustURI(t, "spiffe://ops.local/ns/"+namespace+"/sa/"+serviceAccount)
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: serviceAccount}, DNSNames: []string{serviceAccount + "." + namespace + ".svc.cluster.local"}, URIs: []*url.URL{uri}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer.certificate, &key.PublicKey, issuer.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func issueWorkloadServerCert(t *testing.T, issuer workloadIssuer) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(99998), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer.certificate, &key.PublicKey, issuer.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func mustURI(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
