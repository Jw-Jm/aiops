package bundle

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"ops-platform/internal/profile"
)

func TestInstallerRejectsMissingRuntimeBootstrapBeforeImport(t *testing.T) {
	p := importProfile()
	p.Components["keycloak"] = profile.ResolvedComponent{Mode: "external", Endpoint: "https://keycloak.identity.svc:8443"}
	p.Components["openbao"] = profile.ResolvedComponent{Mode: "external", Endpoint: "https://bao.trust.svc:8200"}
	p.Components["seaweedfs"] = profile.ResolvedComponent{Mode: "bundled", Endpoint: "https://archive.ops-system.svc:8333"}
	if _, err := platformRuntimeValues(context.Background(), p, func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"data":{}}`), nil
	}); err == nil {
		t.Fatal("missing independent OpenBao CA and archive bucket accepted")
	}
}

func TestRuntimeBootstrapBindsExternalEndpointsAndIndependentTrust(t *testing.T) {
	p := importProfile()
	p.Components["keycloak"] = profile.ResolvedComponent{Mode: "external", Endpoint: "https://keycloak.identity.svc:8443"}
	p.Components["openbao"] = profile.ResolvedComponent{Mode: "external", Endpoint: "https://bao.trust.svc:8200"}
	p.Components["seaweedfs"] = profile.ResolvedComponent{Mode: "external", Endpoint: "https://archive.storage.svc:8333"}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, public, private)
	if err != nil {
		t.Fatal(err)
	}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	trust := `{"review":"` + base64.StdEncoding.EncodeToString(public) + `"}`
	data := map[string]string{"openbao-ca.pem": ca, "oidc-ca.pem": ca, "archive-ca.pem": ca, "registry-trust.json": trust, "archive-bucket": "review-audit"}
	run := func(_ context.Context, program string, args ...string) ([]byte, error) {
		if program != "kubectl" || !strings.Contains(strings.Join(args, " "), "get configmap ops-platform-bootstrap -o json") {
			t.Fatalf("unexpected bootstrap command: %s %v", program, args)
		}
		return json.Marshal(map[string]any{"data": data})
	}
	values, err := platformRuntimeValues(context.Background(), p, run)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"oidcIssuerURL": "https://keycloak.identity.svc:8443/realms/ops", "openbaoAddress": "https://bao.trust.svc:8200", "archiveEndpoint": "https://archive.storage.svc:8333", "archiveBucket": "review-audit", "openbaoCABundle": ca, "oidcCABundle": ca, "archiveCABundle": ca} {
		if values[key] != want {
			t.Errorf("runtime %s lost the independent lock", key)
		}
	}
	for _, key := range []string{"openbao-ca.pem", "oidc-ca.pem", "archive-ca.pem", "registry-trust.json", "archive-bucket"} {
		old := data[key]
		data[key] = "invalid_"
		if _, err := platformRuntimeValues(context.Background(), p, run); err == nil {
			t.Errorf("invalid %s accepted", key)
		}
		data[key] = old
	}
	delete(data, "oidc-ca.pem")
	if _, err := platformRuntimeValues(context.Background(), p, run); err == nil {
		t.Fatal("scratch API accepted TLS issuer without independently supplied trust")
	}
	data["oidc-ca.pem"] = ca
	p.Components["keycloak"] = profile.ResolvedComponent{Mode: "external", Endpoint: "http://keycloak.identity.svc:8080"}
	if _, err := platformRuntimeValues(context.Background(), p, run); err == nil {
		t.Fatal("in-cluster plaintext OIDC accepted")
	}
}
