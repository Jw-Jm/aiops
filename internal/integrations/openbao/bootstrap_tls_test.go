package openbao

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKubectl installs a script that emulates the two kubectl calls of the TLS
// bootstrap. It is a transport double, not a Kubernetes capability claim: the
// cluster behaviour itself is verified on the real environment.
func fakeKubectl(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -e
STATE="` + state + `"
verb=""
for argument in "$@"; do
  case "$argument" in
    get|create) verb="$argument" ;;
  esac
done
if [ "$verb" = "get" ]; then
  if [ -f "$STATE/lookup-broken" ]; then
    echo "the server is currently unable to handle the request" >&2
    exit 1
  fi
  if [ -f "$STATE/secret.json" ]; then
    if [ "` + mode + `" = "write-then-fail-lookup-lost" ]; then
      echo "the server is currently unable to handle the request" >&2
      exit 1
    fi
    cat "$STATE/secret.json"
  fi
  exit 0
fi
if [ "$verb" = "create" ]; then
  case "` + mode + `" in
    fail-before-send)
      echo "connection refused" >&2
      exit 1
      ;;
    write-then-fail|write-then-fail-lookup-lost)
      cat > "$STATE/secret.json"
      echo "the server was unable to return a response" >&2
      exit 1
      ;;
    *)
      cat > "$STATE/secret.json"
      exit 0
      ;;
  esac
fi
exit 0
`
	path := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state
}

func bootstrapPaths(t *testing.T) (caFile, repositoryRoot string) {
	t.Helper()
	base := t.TempDir()
	caFile = filepath.Join(base, "bootstrap", "openbao-ca.pem")
	if err := os.MkdirAll(filepath.Dir(caFile), 0o700); err != nil {
		t.Fatal(err)
	}
	repositoryRoot = filepath.Join(base, "repository")
	if err := os.MkdirAll(repositoryRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	return caFile, repositoryRoot
}

// A successful bootstrap creates the Secret, keeps one CA trust file with exactly
// 0600 permissions and removes the pending material.
func TestEnsureBootstrapTLSCreatesSecretAndTrust(t *testing.T) {
	state := fakeKubectl(t, "ok")
	caFile, repositoryRoot := bootstrapPaths(t)
	if err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", caFile, repositoryRoot, ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	info, err := os.Stat(caFile)
	if err != nil {
		t.Fatalf("the CA trust file is missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("CA trust permissions = %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(state, "secret.json")); err != nil {
		t.Fatalf("the TLS Secret was not created: %v", err)
	}
	if _, err := os.Stat(pendingMaterialPath(caFile)); err == nil {
		t.Fatal("the pending material stayed after the identity was in place")
	}
	if err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", caFile, repositoryRoot, ""); err != nil {
		t.Fatalf("idempotent bootstrap: %v", err)
	}
}

// A write whose result is unknown keeps the unique CA trust and the pending
// identity: the create may still land, and the trust anchor is never destroyed.
func TestEnsureBootstrapTLSKeepsTrustAndIdentityWhenOutcomeIsUnknown(t *testing.T) {
	state := fakeKubectl(t, "fail-before-send")
	caFile, repositoryRoot := bootstrapPaths(t)
	caBefore, err := os.ReadFile(caFile)
	_ = caBefore
	_ = err
	err = EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", caFile, repositoryRoot, "")
	if err == nil || !strings.Contains(err.Error(), "OPENBAO_TLS_SECRET_CREATE_UNKNOWN") {
		t.Fatalf("bootstrap = %v", err)
	}
	trust, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("the trust anchor was destroyed: %v", err)
	}
	if len(trust) == 0 {
		t.Fatal("the trust anchor is empty")
	}
	pendingPath := pendingMaterialPath(caFile)
	info, err := os.Stat(pendingPath)
	if err != nil {
		t.Fatalf("the pending identity was destroyed: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("the pending material permissions = %o", info.Mode().Perm())
	}
	var material bootstrapPendingMaterial
	encoded, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &material); err != nil {
		t.Fatal(err)
	}
	if material.Certificate == "" || material.PrivateKey == "" || material.CADigest == "" {
		t.Fatalf("the pending material is incomplete: %+v", material)
	}
	// The pending material carries the leaf key only; a CA key is never part of
	// it.
	if strings.Contains(string(encoded), "CA PRIVATE KEY") {
		t.Fatal("the pending material carries a CA key")
	}

	// The create lands later: the next run finds the identity in the cluster and
	// converges without a new identity and without changing the trust anchor.
	secret, err := json.Marshal(map[string]any{"data": map[string]string{
		"tls.crt": material.Certificate,
		"tls.key": material.PrivateKey,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "secret.json"), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", caFile, repositoryRoot, ""); err != nil {
		t.Fatalf("the confirmation run = %v", err)
	}
	after, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(trust) {
		t.Fatal("the trust anchor changed during the retry")
	}
	if _, err := os.Stat(pendingPath); err == nil {
		t.Fatal("the pending material stayed after the identity was confirmed")
	}
}

// A Secret of another source is never trusted and never overwritten.
func TestEnsureBootstrapTLSRefusesForeignSecret(t *testing.T) {
	state := fakeKubectl(t, "ok")
	caFile, repositoryRoot := bootstrapPaths(t)
	if err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", caFile, repositoryRoot, ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	foreign := `{"data":{"tls.crt":"Zm9yZWlnbg=="}}`
	if err := os.WriteFile(filepath.Join(state, "secret.json"), []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", caFile, repositoryRoot, "")
	if err == nil || !strings.Contains(err.Error(), "OPENBAO_") {
		t.Fatalf("a foreign Secret was accepted: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(state, "secret.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != foreign {
		t.Fatal("the existing Secret was overwritten")
	}
}

// A Secret that holds another key for the same certificate is not the submitted
// identity and is refused.
func TestEnsureBootstrapTLSRefusesKeyMismatch(t *testing.T) {
	state := fakeKubectl(t, "ok")
	caFile, repositoryRoot := bootstrapPaths(t)
	if err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", caFile, repositoryRoot, ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	encoded, err := os.ReadFile(filepath.Join(state, "secret.json"))
	if err != nil {
		t.Fatal(err)
	}
	var secret kubernetesSecret
	if err := json.Unmarshal(encoded, &secret); err != nil {
		t.Fatal(err)
	}
	// Keep the genuine certificate; only the key is changed.
	_, _, otherKey, err := generateBootstrapCertificates("openbao", "ops")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{base64.StdEncoding.EncodeToString(otherKey), "", "not-base64"} {
		secret.Data["tls.key"] = key
		mismatched, err := json.Marshal(secret)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, "secret.json"), mismatched, 0o600); err != nil {
			t.Fatal(err)
		}
		err = EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", caFile, repositoryRoot, "")
		if err == nil || !strings.Contains(err.Error(), "OPENBAO_TLS_SECRET_INVALID") {
			t.Fatalf("invalid key was not rejected: %v", err)
		}
	}

}

// A trust anchor without a verifiable pending identity fails closed: the user's
// trust anchor is never destroyed and the recovery stays explicit.
func TestEnsureBootstrapTLSCaWithoutIdentityFailsClosed(t *testing.T) {
	fakeKubectl(t, "ok")
	caFile, repositoryRoot := bootstrapPaths(t)
	if err := os.WriteFile(caFile, []byte("stale anchor"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", caFile, repositoryRoot, "")
	if err == nil || !strings.Contains(err.Error(), "OPENBAO_TLS_TRUST_UNRECOVERABLE") {
		t.Fatalf("the unrecoverable trust was not reported: %v", err)
	}
	contents, readErr := os.ReadFile(caFile)
	if readErr != nil {
		t.Fatalf("the trust anchor was destroyed: %v", readErr)
	}
	if string(contents) != "stale anchor" {
		t.Fatal("the trust anchor was replaced")
	}
}

func TestEnsureBootstrapTLSRejectsPendingLinks(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			fakeKubectl(t, "fail-before-send")
			ca, repo := bootstrapPaths(t)
			victim := filepath.Join(repo, "protected.json")
			if existing {
				if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(victim, pendingMaterialPath(ca)); err != nil {
				t.Fatal(err)
			}
			if err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", ca, repo, ""); err == nil {
				t.Fatal("pending link accepted")
			}
			raw, err := os.ReadFile(victim)
			if existing {
				if err != nil || string(raw) != "keep" {
					t.Fatal("protected file changed")
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("protected file created")
			}
			if _, err := os.Stat(ca); !os.IsNotExist(err) {
				t.Fatal("CA created before pending validation")
			}
		})
	}
}

func TestEnsureBootstrapTLSAcceptsExternalCAAlias(t *testing.T) {
	fakeKubectl(t, "ok")
	ca, repo := bootstrapPaths(t)
	if err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", ca, repo, ""); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.Symlink(ca, alias); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBootstrapTLS(context.Background(), "fixture", "ops", "openbao", alias, repo, ""); err != nil {
		t.Fatalf("valid external CA alias: %v", err)
	}
}

func TestPendingWriterNeverOverwritesExistingEntry(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{"existing", "link"} {
		if err := os.WriteFile(filepath.Join(dir, "existing"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if name == "link" {
			if err := os.Symlink("existing", filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writePendingMaterial(root, name, bootstrapPendingMaterial{PrivateKey: "secret"}); err == nil {
			t.Fatal("existing pending entry overwritten")
		}
		raw, err := os.ReadFile(filepath.Join(dir, "existing"))
		if err != nil || string(raw) != "keep" {
			t.Fatal("existing target changed")
		}
	}
}
