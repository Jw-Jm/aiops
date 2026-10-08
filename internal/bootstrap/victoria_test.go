package bootstrap

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func validVictoriaTrust(t *testing.T) VictoriaTrustInput {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "operator source trust"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, public, key)
	if err != nil {
		t.Fatal(err)
	}
	private, _ := x509.MarshalPKCS8PrivateKey(key)
	input := VictoriaTrustInput{SchemaVersion: 1}
	for i, name := range []string{"victoriametrics", "victorialogs"} {
		service := "ops-victoria-metrics"
		if i == 1 {
			service = "ops-victoria-logs"
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), DNSNames: []string{service + ".ops-victoria-test.svc.cluster.local"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
		cert, err := x509.CreateCertificate(rand.Reader, leaf, ca, public, key)
		if err != nil {
			t.Fatal(err)
		}
		input.Sources = append(input.Sources, VictoriaTrust{Name: name, CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})), PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), Username: "explicit-read", Password: strings.Repeat(string(rune('a'+i)), 32)})
	}
	return input
}

func TestVictoriaTrustRejectsTargetAndCredentialSubstitution(t *testing.T) {
	input := validVictoriaTrust(t)
	if err := input.Validate("ops-victoria-test"); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*VictoriaTrustInput){
		func(c *VictoriaTrustInput) { c.SchemaVersion = 0 },
		func(c *VictoriaTrustInput) { c.Sources = c.Sources[:1] },
		func(c *VictoriaTrustInput) { c.Sources[1].Name = c.Sources[0].Name },
		func(c *VictoriaTrustInput) { c.Sources[1].Password = c.Sources[0].Password },
		func(c *VictoriaTrustInput) { c.Sources[0].Password = "short" },
		func(c *VictoriaTrustInput) { c.Sources[0].Username = "secret:header" },
		func(c *VictoriaTrustInput) { c.Sources[0].CA += c.Sources[0].PrivateKey },
		func(c *VictoriaTrustInput) { c.Sources[0].Certificate = c.Sources[1].Certificate },
		func(c *VictoriaTrustInput) { c.Sources[0].PrivateKey = "invalid" },
	} {
		c := validVictoriaTrust(t)
		mutate(&c)
		if err := c.Validate("ops-victoria-test"); err == nil {
			t.Fatal("substituted target or credential accepted")
		}
	}
	if err := input.Validate("ops-foreign-installation"); err == nil {
		t.Fatal("foreign namespace SAN accepted")
	}
	for _, s := range input.Sources {
		var client map[string]string
		if json.Unmarshal([]byte(s.ClientCredential()), &client) != nil || client["mode"] != "basic" || client["password"] != s.Password {
			t.Fatal("explicit client credential differs")
		}
		if strings.Contains(s.ClientCredential(), "PRIVATE KEY") {
			t.Fatal("server key disclosed to source client")
		}
	}
}
