package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/integrations/openbao"
)

// This test uses a real Kubernetes API in an owned, disposable namespace.
func TestOpenBaoBootstrapTLSLive(t *testing.T) {
	kubeContext := os.Getenv("OPS_OPENBAO_TLS_TEST_CONTEXT")
	if kubeContext == "" {
		t.Skip("OPS_OPENBAO_TLS_TEST_CONTEXT must name the isolated development cluster")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	namespace := "ops-tls-test-" + uuid.NewString()[:8]
	run := func(input []byte, args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, "kubectl", append([]string{"--context", kubeContext}, args...)...)
		command.Stdin = bytes.NewReader(input)
		raw, err := command.Output()
		if err != nil {
			t.Fatalf("kubectl %s: %v", args[0], err)
		}
		return raw
	}
	run(nil, "create", "namespace", namespace)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		if err := exec.CommandContext(cleanup, "kubectl", "--context", kubeContext, "delete", "namespace", namespace, "--wait=false").Run(); err != nil {
			t.Errorf("remove owned TLS namespace: %v", err)
		}
	})
	external, repository := t.TempDir(), t.TempDir()
	caPath := filepath.Join(external, "ca.pem")
	ensure := func() error {
		return openbao.EnsureBootstrapTLS(ctx, kubeContext, namespace, "openbao", caPath, repository, "")
	}
	victim := filepath.Join(repository, "private-key.json")
	if err := os.Symlink(victim, caPath+".pending"); err != nil {
		t.Fatal(err)
	}
	if err := ensure(); err == nil {
		t.Fatal("live bootstrap accepted a dangling pending link")
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatal("protected key target created")
	}
	if raw := run(nil, "-n", namespace, "get", "secret", "ops-openbao-bootstrap-tls", "--ignore-not-found", "-o", "json"); len(bytes.TrimSpace(raw)) != 0 {
		t.Fatal("unsafe pending path caused a Secret write")
	}
	if err := os.Remove(caPath + ".pending"); err != nil {
		t.Fatal(err)
	}
	// Inject only the lost client receipt: create really commits in Kubernetes.
	realKubectl, err := exec.LookPath("kubectl")
	if err != nil {
		t.Fatal(err)
	}
	wrapperDirectory := t.TempDir()
	marker := filepath.Join(wrapperDirectory, "committed")
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nverb=\"\"\nfor arg in \"$@\"; do case \"$arg\" in get|create) verb=$arg;; esac; done\n" +
		"if [ \"$verb\" = get ] && [ -f " + quote(marker) + " ]; then exit 1; fi\n" +
		quote(realKubectl) + " \"$@\" || exit $?\n" +
		"if [ \"$verb\" = create ]; then touch " + quote(marker) + "; exit 1; fi\n"
	if err := os.WriteFile(filepath.Join(wrapperDirectory, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", wrapperDirectory+string(os.PathListSeparator)+originalPath)
	if err := ensure(); err == nil || !strings.Contains(err.Error(), "OPENBAO_TLS_SECRET_CREATE_UNKNOWN") {
		t.Fatalf("lost live receipt: %v", err)
	}
	retainedTrust, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(caPath + ".pending"); err != nil {
		t.Fatal("lost receipt destroyed pending identity")
	}
	t.Setenv("PATH", originalPath)
	if err := ensure(); err != nil {
		t.Fatal(err)
	}
	if err := ensure(); err != nil {
		t.Fatalf("confirmed identity retry: %v", err)
	}
	trust, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(trust, retainedTrust) {
		t.Fatal("unknown outcome retry replaced CA")
	}
	raw := run(nil, "-n", namespace, "get", "secret", "ops-openbao-bootstrap-tls", "-o", "json")
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(raw, &secret); err != nil {
		t.Fatal(err)
	}
	cert, err := base64.StdEncoding.DecodeString(secret.Data["tls.crt"])
	if err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(secret.Data["tls.key"])
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "bootstrap-tls-ok") }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(trust) {
		t.Fatal("invalid CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "openbao." + namespace + ".svc.cluster.local"}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("TLS handshake using the cluster Secret: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "bootstrap-tls-ok" {
		t.Fatal("TLS response failed")
	}
	originalKey := secret.Data["tls.key"]
	for _, invalidKey := range []string{"", base64.StdEncoding.EncodeToString(cert)} {
		patch, err := json.Marshal(map[string]any{"data": map[string]string{"tls.key": invalidKey}})
		if err != nil {
			t.Fatal(err)
		}
		run(patch, "-n", namespace, "patch", "secret", "ops-openbao-bootstrap-tls", "--type=merge", "--patch-file", "/dev/stdin")
		if err := ensure(); err == nil || !strings.Contains(err.Error(), "OPENBAO_TLS_SECRET_INVALID") {
			t.Fatalf("invalid live key: %v", err)
		}
	}
	patch, err := json.Marshal(map[string]any{"data": map[string]string{"tls.key": originalKey}})
	if err != nil {
		t.Fatal(err)
	}
	run(patch, "-n", namespace, "patch", "secret", "ops-openbao-bootstrap-tls", "--type=merge", "--patch-file", "/dev/stdin")
	if err := ensure(); err != nil {
		t.Fatalf("restored identity: %v", err)
	}
	after, err := os.ReadFile(caPath)
	if err != nil || !bytes.Equal(after, trust) {
		t.Fatal("trust changed during retries")
	}
	if _, err := os.Stat(caPath + ".pending"); !os.IsNotExist(err) {
		t.Fatal("pending identity remained")
	}
	t.Log("real Kubernetes pending-link rejection, committed write with lost client receipt, same-identity retry, Secret TLS handshake, invalid-key rejection and recovery passed")
}
