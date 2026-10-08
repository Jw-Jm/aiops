package bootstrap

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func validEnvironment(t *testing.T) (EnvironmentInput, []EnvironmentSecret) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "independent bootstrap test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	trust, _ := json.Marshal(map[string]string{"test-registry": base64.StdEncoding.EncodeToString(pub)})
	c := EnvironmentInput{SchemaVersion: 1, Context: "explicit-test-context", Namespace: "ops-fresh-test", PublicTrust: map[string]string{"openbao-ca.pem": caPEM, "oidc-ca.pem": caPEM, "archive-ca.pem": caPEM, "archive-bucket": "current-evidence", "registry-trust.json": string(trust)}}
	secrets := []EnvironmentSecret{
		{"ops-postgresql-auth", map[string]string{"username": "bootstrap_admin", "password": "private-fixture-only"}},
		{"ops-keycloak-auth", map[string]string{"username": "bootstrap_admin", "password": "private-fixture-only"}},
		{"ops-keycloak-database", map[string]string{"username": "ops_keycloak", "password": "private-fixture-only"}},
		{"ops-seaweedfs-auth", map[string]string{"accessKey": "explicit-admin", "secretKey": "private-fixture-only"}},
		{"ops-seaweedfs-iam", map[string]string{"s3.json": `{"identities":[{"name":"bootstrap-admin","credentials":[{"accessKey":"explicit-admin","secretKey":"private-fixture-only"}],"actions":["Admin"]}]}`}},
	}
	for i, item := range []struct{ name, service string }{{"ops-openbao-bootstrap-tls", "ops-openbao"}, {"ops-keycloak-tls", "ops-keycloak"}, {"ops-seaweedfs-tls", "ops-seaweedfs-s3"}} {
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: item.service}, DNSNames: []string{item.service + "." + c.Namespace + ".svc.cluster.local"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
		der, err := x509.CreateCertificate(rand.Reader, leaf, ca, pub, key)
		if err != nil {
			t.Fatal(err)
		}
		private, _ := x509.MarshalPKCS8PrivateKey(key)
		secrets = append(secrets, EnvironmentSecret{item.name, map[string]string{"tls.crt": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), "tls.key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))}})
	}
	return c, secrets
}
func TestEnvironmentBootstrapRejectsEmptyOrUnboundedArchiveIAM(t *testing.T) {
	for _, raw := range []string{`{"identities":[]}`, `{"identities":[{}]}`, `{"identities":[{"name":"foreign-admin","credentials":[{"accessKey":"foreign","secretKey":"foreign"}],"actions":["Admin"]}]}`} {
		c, secrets := validEnvironment(t)
		for i := range secrets {
			if secrets[i].Name == "ops-seaweedfs-iam" {
				secrets[i].Data["s3.json"] = raw
			}
		}
		if err := c.Validate(secrets); err == nil {
			t.Fatal("missing or unrestricted IAM accepted")
		}
	}
	c, secrets := validEnvironment(t)
	if err := c.Validate(secrets); err != nil {
		t.Fatal(err)
	}
}
