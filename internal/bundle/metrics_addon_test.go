package bundle

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"gopkg.in/yaml.v3"
	"math/big"
	"ops-platform/internal/profile"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetricsAddonRequiresExplicitTrustAndExactNetwork(t *testing.T) {
	for _, raw := range []string{
		`{}`, // No implicit identity, CA, address or port defaults.
		`{"schemaVersion":1,"namespace":"ops-metrics","servingTLSSecret":"tls","servingCA":"invalid","kubeletCAConfigMap":"ca","nodeCIDRs":["192.168.1.0/24"],"apiServerCIDRs":["192.168.1.1/32"],"apiServerPort":443}`,
		`{"schemaVersion":1,"namespace":"ops-metrics","servingTLSSecret":"tls","servingCA":"invalid","kubeletCAConfigMap":"ca","nodeCIDRs":["192.168.1.1/32"],"apiServerCIDRs":["1.1.1.1/32"],"apiServerPort":443}`,
		`{"schemaVersion":1,"namespace":"ops-metrics","insecureSkipTLSVerify":true}`,
	} {
		if _, err := ReadMetricsAddonValues(strings.NewReader(raw)); err == nil {
			t.Fatal("invalid/insecure addon inputs were accepted")
		}
	}
}

func metricsTestIdentity(t *testing.T) (MetricsAddonValues, []byte, []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "independent-test-ca"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	c := MetricsAddonValues{SchemaVersion: 1, Namespace: "ops-metrics-test", ServingTLSSecret: "metrics-tls", ServingCA: string(caPEM), KubeletCAConfigMap: "kubelet-ca", NodeCIDRs: []string{"192.168.1.1/32"}, APIServerCIDRs: []string{"192.168.1.1/32", "10.96.0.1/32"}, APIServerPort: 443}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{metricsAddonName + "." + c.Namespace + ".svc", metricsAddonName + "." + c.Namespace + ".svc.cluster.local"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	return c, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
func TestMetricsAddonIndependentTrustAndScope(t *testing.T) {
	c, crt, key := metricsTestIdentity(t)
	raw, _ := json.Marshal(c)
	if _, err := ReadMetricsAddonValues(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMetricsAddonIdentity(c, crt, key, c.ServingCA); err != nil {
		t.Fatal(err)
	}
	changed := c
	changed.Namespace = "other-tenant"
	if err := ValidateMetricsAddonIdentity(changed, crt, key, c.ServingCA); err == nil {
		t.Fatal("serving certificate accepted for another namespace")
	}
	other, _, otherKey := metricsTestIdentity(t)
	if err := ValidateMetricsAddonIdentity(c, crt, otherKey, c.ServingCA); err == nil {
		t.Fatal("mismatched private key accepted")
	}
	changed = c
	changed.ServingCA = other.ServingCA
	if err := ValidateMetricsAddonIdentity(changed, crt, key, c.ServingCA); err == nil {
		t.Fatal("untrusted leaf accepted")
	}
	for _, mutate := range []func(*MetricsAddonValues){func(x *MetricsAddonValues) { x.NodeCIDRs = []string{"192.168.1.0/24"} }, func(x *MetricsAddonValues) { x.APIServerCIDRs = []string{"1.1.1.1/32"} }, func(x *MetricsAddonValues) { x.APIServerPort = 0 }, func(x *MetricsAddonValues) { x.NodeCIDRs = []string{"192.168.1.1/32", "192.168.1.1/32"} }} {
		changed = c
		mutate(&changed)
		if changed.Validate() == nil {
			t.Fatal("missing or expanded network accepted")
		}
	}
}
func TestMetricsAddonAuthenticChartHasBoundedResourcesAndPolicyPhase(t *testing.T) {
	c, _, _ := metricsTestIdentity(t)
	values, err := yaml.Marshal(c.chartValues())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(file, values, 0600); err != nil {
		t.Fatal(err)
	}
	render := func(policy bool) []byte {
		args := []string{"template", MetricsAddonRelease, "../../deploy/charts/ops-metrics-server", "--namespace", c.Namespace, "--values", file}
		if policy {
			args = append(args, "--set", "networkPolicyOnly=true")
		}
		raw, err := exec.Command("helm", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("actual locked Helm render: %v: %s", err, raw)
		}
		return raw
	}
	raw := render(false)
	var calls int
	readAbsent := func(_ context.Context, program string, args ...string) ([]byte, error) {
		if program != "kubectl" {
			t.Fatal("validation attempted mutation")
		}
		if len(args) < 5 || args[2] != "get" {
			t.Fatal("validation attempted non-read operation")
		}
		calls++
		return nil, nil
	}
	p := profile.ResolvedProfile{Kubernetes: profile.KubernetesDiscovery{Context: "test"}}
	if err := validateMetricsChart(context.Background(), raw, c, p, readAbsent); err != nil {
		t.Fatal(err)
	}
	if calls != 9 {
		t.Fatal("fixed resource ownership preflight incomplete")
	}
	if err := validateMetricsPolicy(render(true), c); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ from, to string }{{"verbs: [get]", "verbs: [get, create]"}, {"namespace: kube-system", "namespace: default"}, {"imagePullPolicy: Never", "imagePullPolicy: Always"}, {"--kubelet-use-node-status-port", "--kubelet-insecure-tls"}, {"kind: APIService", "kind: CustomResourceDefinition"}, {"192.168.1.1/32", "0.0.0.0/0"}, {"secretName: metrics-tls", "secretName: unrelated-db-root"}, {"runAsUser: 1000", "runAsUser: 0"}, {"group: metrics.k8s.io", "group: other.example"}} {
		bad := bytes.Replace(raw, []byte(change.from), []byte(change.to), 1)
		if bytes.Equal(bad, raw) {
			t.Fatalf("attack fixture did not modify authentic chart: %s", change.from)
		}
		if err := validateMetricsChart(context.Background(), bad, c, p, readAbsent); err == nil {
			t.Fatalf("tampered Metrics chart accepted: %s", change.to)
		}
	}
	existing := func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"metadata":{"uid":"shared"}}`), nil
	}
	if err := validateMetricsChart(context.Background(), raw, c, p, existing); err == nil {
		t.Fatal("existing global/namespaced resource adopted")
	}
}
