package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"github.com/google/uuid"
	"math/big"
	"net"
	"net/url"
	"ops-platform/internal/app"
	"ops-platform/internal/auth"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sp04Kubectl(t *testing.T, ctx context.Context, input []byte, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = bytes.NewReader(input)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("owned Kubernetes fixture command failed (%v)", args)
	}
	return raw
}
func sp04OwnedKubernetes(t *testing.T, ctx context.Context, tenant, source string) (app.SP04Cluster, string) {
	t.Helper()
	namespace := "sp04-chain-" + uuid.NewString()[:8]
	label := map[string]string{"ops.platform.test": namespace}
	objects := []map[string]any{
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace, "labels": label}},
		{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": "collector", "namespace": namespace, "labels": label}},
		{"apiVersion": "coordination.k8s.io/v1", "kind": "Lease", "metadata": map[string]any{"name": "sp04-graph", "namespace": namespace, "labels": label, "annotations": map[string]string{"ops.platform/owner-epoch": "0"}}, "spec": map[string]any{}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": map[string]any{"name": namespace, "labels": label}, "rules": []any{
			map[string]any{"apiGroups": []string{""}, "resources": []string{"pods", "nodes", "services", "configmaps", "persistentvolumeclaims", "persistentvolumes", "events"}, "verbs": []string{"get", "list", "watch"}},
			map[string]any{"apiGroups": []string{"metrics.k8s.io"}, "resources": []string{"nodes"}, "verbs": []string{"get"}},
			map[string]any{"apiGroups": []string{"apps"}, "resources": []string{"deployments", "replicasets", "statefulsets", "daemonsets"}, "verbs": []string{"get", "list", "watch"}},
			map[string]any{"apiGroups": []string{"batch"}, "resources": []string{"jobs"}, "verbs": []string{"get", "list", "watch"}},
			map[string]any{"apiGroups": []string{"storage.k8s.io"}, "resources": []string{"storageclasses", "csidrivers", "csinodes", "volumeattachments"}, "verbs": []string{"get", "list", "watch"}},
			map[string]any{"apiGroups": []string{"discovery.k8s.io"}, "resources": []string{"endpointslices"}, "verbs": []string{"get", "list", "watch"}}}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": map[string]any{"name": namespace, "labels": label}, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": namespace}, "subjects": []any{map[string]string{"kind": "ServiceAccount", "name": "collector", "namespace": namespace}}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role", "metadata": map[string]any{"name": "lease", "namespace": namespace, "labels": label}, "rules": []any{map[string]any{"apiGroups": []string{"coordination.k8s.io"}, "resources": []string{"leases"}, "resourceNames": []string{"sp04-graph"}, "verbs": []string{"get", "update"}}}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": map[string]any{"name": "lease", "namespace": namespace, "labels": label}, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "lease"}, "subjects": []any{map[string]string{"kind": "ServiceAccount", "name": "collector", "namespace": namespace}}},
	}
	raw, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": objects})
	sp04Kubectl(t, ctx, raw, "create", "-f", "-")
	// Capture exact UIDs for cleanup, using server-side delete preconditions.
	type owned struct{ kind, name, namespace, uid string }
	created := []owned{}
	for _, kind := range []string{"namespace", "clusterrole", "clusterrolebinding"} {
		var object struct {
			Metadata struct {
				UID string `json:"uid"`
			}
		}
		raw := sp04Kubectl(t, ctx, nil, "get", kind, namespace, "-o", "json")
		json.Unmarshal(raw, &object)
		created = append(created, owned{kind, namespace, "", object.Metadata.UID})
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, o := range created {
			raw, err := exec.CommandContext(cleanup, "kubectl", "get", o.kind, o.name, "-o", "json").Output()
			if err != nil {
				continue
			}
			var object struct {
				Metadata struct {
					UID    string            `json:"uid"`
					Labels map[string]string `json:"labels"`
				}
			}
			json.Unmarshal(raw, &object)
			if object.Metadata.UID != o.uid || object.Metadata.Labels["ops.platform.test"] != namespace {
				t.Error("owned cleanup identity changed")
				continue
			}
			path := map[string]string{"namespace": "/api/v1/namespaces/", "clusterrole": "/apis/rbac.authorization.k8s.io/v1/clusterroles/", "clusterrolebinding": "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/"}[o.kind] + o.name
			options, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]string{"uid": o.uid}})
			command := exec.CommandContext(cleanup, "kubectl", "delete", "--raw", path, "-f", "-")
			command.Stdin = bytes.NewReader(options)
			if err := command.Run(); err != nil {
				t.Error("owned cleanup failed")
			}
		}
	})
	var kube struct {
		Clusters []struct {
			Cluster struct {
				Server string `json:"server"`
				CA     string `json:"certificate-authority-data"`
			} `json:"cluster"`
		} `json:"clusters"`
	}
	json.Unmarshal(sp04Kubectl(t, ctx, nil, "config", "view", "--minify", "--raw", "-o", "json"), &kube)
	if len(kube.Clusters) != 1 {
		t.Fatal("current Kubernetes endpoint missing")
	}
	ca, err := base64.StdEncoding.DecodeString(kube.Clusters[0].Cluster.CA)
	if err != nil || len(ca) == 0 {
		t.Fatal("current Kubernetes CA unavailable")
	}
	directory := t.TempDir()
	caPath := filepath.Join(directory, "kubernetes-ca.pem")
	tokenPath := filepath.Join(directory, "collector.token")
	os.WriteFile(caPath, ca, 0600)
	os.WriteFile(tokenPath, sp04Kubectl(t, ctx, nil, "create", "token", "collector", "-n", namespace, "--duration=30m"), 0600)
	uid := strings.TrimSpace(string(sp04Kubectl(t, ctx, nil, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")))
	return app.SP04Cluster{Tenant: tenant, ClusterUID: uid, SourceID: source, SourceRevision: 1, BackendLogicalID: "orbstack-owned-collector", Endpoint: kube.Clusters[0].Cluster.Server, CAFile: caPath, TokenFile: tokenPath, LeaseNamespace: namespace, LeaseName: "sp04-graph"}, namespace
}
func sp04RuntimeTLS(t *testing.T, namespace string) (app.SP04Config, app.SP04Config) {
	t.Helper()
	directory := t.TempDir()
	now := time.Now().UTC()
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "SP04 owned CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, public, private)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	caPath := filepath.Join(directory, "ca.pem")
	os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	crl, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(1), ThisUpdate: now.Add(-time.Second), NextUpdate: now.Add(time.Hour)}, ca, private)
	if err != nil {
		t.Fatal(err)
	}
	crlPath := filepath.Join(directory, "crl.pem")
	os.WriteFile(crlPath, pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crl}), 0600)
	contextPublic, contextPrivate, _ := ed25519.GenerateKey(rand.Reader)
	pubPath, privPath := filepath.Join(directory, "context.pub"), filepath.Join(directory, "context.key")
	os.WriteFile(pubPath, contextPublic, 0600)
	os.WriteFile(privPath, contextPrivate, 0600)
	configs := []app.SP04Config{}
	for i, name := range []string{"ops-worker", "ops-api"} {
		identity, _ := auth.NewWorkloadIdentity(namespace, name)
		uri, _ := url.Parse(identity.URI)
		pub, key, _ := ed25519.GenerateKey(rand.Reader)
		template := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{identity.DNSName}, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
		cert, err := x509.CreateCertificate(rand.Reader, template, ca, pub, private)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := x509.MarshalPKCS8PrivateKey(key)
		certPath, keyPath := filepath.Join(directory, name+".pem"), filepath.Join(directory, name+".key")
		os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0600)
		os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600)
		configs = append(configs, app.SP04Config{Namespace: namespace, CAFile: caPath, CRLFile: crlPath, CertificateFile: certPath, PrivateKeyFile: keyPath, ServerName: "ops-worker." + namespace + ".svc.cluster.local", ContextPrivateKeyFile: privPath, ContextPublicKeyFile: pubPath, AllowedWorkerCIDRs: []string{"127.0.0.1/32"}, ArchiveBackendLogicalID: "archive-live"})
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	configs[0].ListenAddress = address
	configs[0].OwnerEndpoint = "https://" + address
	return configs[0], configs[1]
}
func sp04ConfigFile(t *testing.T, c app.SP04Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.json")
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
