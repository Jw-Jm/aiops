package e2e_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ops-platform/internal/integrations/openbao"
)

func TestOpenBaoBootstrap(t *testing.T) {
	t.Run("initializes once and stores recovery material as 0600", func(t *testing.T) {
		server := newOpenBaoFake(t)
		defer server.Close()
		client := server.client(t, "")
		ctx := context.Background()
		before, err := client.Status(ctx)
		if err != nil || before.State != openbao.StateUninitialized {
			t.Fatalf("initial status = %#v, %v; want uninitialized", before, err)
		}

		recoveryPath := filepath.Join(t.TempDir(), "recovery.json")
		if err := client.Initialize(ctx, recoveryPath); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		info, err := os.Stat(recoveryPath)
		if err != nil {
			t.Fatalf("stat recovery file: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("recovery file mode = %04o, want 0600", info.Mode().Perm())
		}
		contents, err := os.ReadFile(recoveryPath)
		if err != nil {
			t.Fatalf("read recovery file: %v", err)
		}
		if !strings.Contains(string(contents), "share-one") || !strings.Contains(string(contents), "root-token-canary") {
			t.Fatal("recovery file does not contain the returned Shamir shares and initial token")
		}
		if err := client.Initialize(ctx, filepath.Join(t.TempDir(), "second-recovery.json")); err == nil {
			t.Fatal("repeated initialization unexpectedly succeeded")
		}
		if server.initCalls() != 1 {
			t.Fatalf("sys/init was called %d times, want once", server.initCalls())
		}
	})

	t.Run("sealed state gates configure until threshold unseal", func(t *testing.T) {
		server := newOpenBaoFake(t)
		defer server.Close()
		client := server.client(t, "root-token-canary")
		ctx := context.Background()
		if err := client.Configure(ctx); err == nil {
			t.Fatal("configure while uninitialized/sealed unexpectedly succeeded")
		}
		if got := server.configureCalls(); got != 0 {
			t.Fatalf("configure API was called %d times while sealed", got)
		}
		recoveryPath := filepath.Join(t.TempDir(), "recovery.json")
		if err := client.Initialize(ctx, recoveryPath); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		if err := client.RequireReady(ctx); err == nil {
			t.Fatal("RequireReady accepted a sealed OpenBao")
		}
		if _, err := client.Unseal(ctx, "wrong-key"); err == nil {
			t.Fatal("wrong Shamir key unexpectedly unsealed OpenBao")
		}
		partial, err := client.Unseal(ctx, "share-one")
		if err != nil || partial.State != openbao.StateSealed || partial.Progress != 1 {
			t.Fatalf("first valid key = %#v, %v; want sealed with progress 1", partial, err)
		}
		ready, err := client.Unseal(ctx, "share-two")
		if err != nil || ready.State != openbao.StateReady {
			t.Fatalf("threshold unseal = %#v, %v; want ready", ready, err)
		}
		if err := client.Configure(ctx); err != nil {
			t.Fatalf("Configure after threshold unseal: %v", err)
		}
		if got := server.pkiMaxTTLSeconds(); got < (8760 * time.Hour).Seconds() {
			t.Fatalf("PKI max_lease_ttl = %v seconds, want at least 8760h", got)
		}
	})

	t.Run("recovery file cannot be written in the repository or bundle", func(t *testing.T) {
		server := newOpenBaoFake(t)
		defer server.Close()
		repository := t.TempDir()
		bundleDir := filepath.Join(t.TempDir(), "bundle")
		if err := os.MkdirAll(bundleDir, 0o700); err != nil {
			t.Fatal(err)
		}
		client := server.clientWithPaths(t, "", repository, bundleDir)
		for _, path := range []string{
			filepath.Join(repository, "recovery.json"),
			filepath.Join(bundleDir, "recovery.json"),
		} {
			if err := client.Initialize(context.Background(), path); err == nil {
				t.Errorf("Initialize accepted restricted recovery path %s", path)
			}
			if err := os.WriteFile(path, []byte("restricted test fixture"), 0o600); err != nil {
				t.Fatalf("write restricted recovery path fixture: %v", err)
			}
			if _, err := openbao.ReadExternalRecoveryMaterial(path, repository, bundleDir); err == nil {
				t.Errorf("ReadExternalRecoveryMaterial accepted restricted path %s", path)
			}
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove restricted recovery path fixture: %v", err)
			}
		}
		insidePath := filepath.Join(repository, "symlink-target.json")
		if err := os.WriteFile(insidePath, []byte("restricted test fixture"), 0o600); err != nil {
			t.Fatalf("write restricted symlink target: %v", err)
		}
		externalLink := filepath.Join(t.TempDir(), "recovery-link.json")
		if err := os.Symlink(insidePath, externalLink); err != nil {
			t.Fatalf("create external recovery symlink fixture: %v", err)
		}
		if _, err := openbao.ReadExternalRecoveryMaterial(externalLink, repository, bundleDir); err == nil {
			t.Fatal("ReadExternalRecoveryMaterial accepted a symlink into the repository")
		}
		if server.initCalls() != 0 {
			t.Fatalf("sys/init was called %d times for a restricted output path", server.initCalls())
		}
	})

	t.Run("untrusted bootstrap CA fails closed", func(t *testing.T) {
		server := newOpenBaoFake(t)
		defer server.Close()
		wrongCA := []byte(testPKICertificate(t))
		client, err := openbao.NewClient(openbao.ClientConfig{
			Address:      server.server.URL,
			ServerName:   "127.0.0.1",
			CACertBundle: wrongCA,
		})
		if err == nil {
			_, err = client.Status(context.Background())
		}
		if err == nil {
			t.Fatal("untrusted bootstrap CA was accepted")
		}
	})

	t.Run("configuration drift is returned as a distinct state", func(t *testing.T) {
		server := newOpenBaoFake(t)
		defer server.Close()
		server.setReadyWithDrift()
		client := server.client(t, "root-token-canary")
		status, err := client.Status(context.Background())
		if err != nil || status.State != openbao.StateConfigurationDrift {
			t.Fatalf("status = %#v, %v; want configuration_drift", status, err)
		}
		statusClient := server.client(t, "")
		unverified, err := statusClient.Status(context.Background())
		if err != nil || unverified.State != openbao.StateReady || !strings.Contains(unverified.Detail, "configuration not verified") {
			t.Fatalf("status without recovery material = %#v, %v; want ready with an explicit verification limitation", unverified, err)
		}
	})

	t.Run("OpenBao unavailability blocks ready-required operations", func(t *testing.T) {
		server := newOpenBaoFake(t)
		client := server.client(t, "root-token-canary")
		server.Close()
		if err := client.RequireReady(context.Background()); err == nil || !strings.Contains(err.Error(), "OPENBAO_UNAVAILABLE") {
			t.Fatalf("RequireReady error = %v, want OPENBAO_UNAVAILABLE", err)
		}
		if err := client.Configure(context.Background()); err == nil || !strings.Contains(err.Error(), "OPENBAO_UNAVAILABLE") {
			t.Fatalf("Configure error = %v, want OPENBAO_UNAVAILABLE", err)
		}
	})
}

type openBaoFake struct {
	t              *testing.T
	server         *httptest.Server
	mu             sync.Mutex
	init           bool
	sealed         bool
	progress       int
	initCount      int
	configureCount int
	drift          bool
	pkiMaxLeaseTTL float64
	pkiCertificate string
	mounts         map[string]map[string]any
	auth           map[string]map[string]any
	objects        map[string]map[string]any
}

func newOpenBaoFake(t *testing.T) *openBaoFake {
	t.Helper()
	fake := &openBaoFake{
		t: t, sealed: true,
		pkiMaxLeaseTTL: 2764800,
		pkiCertificate: testPKICertificate(t),
		mounts:         map[string]map[string]any{}, auth: map[string]map[string]any{}, objects: map[string]map[string]any{},
	}
	fake.server = httptest.NewTLSServer(http.HandlerFunc(fake.handle))
	return fake
}

func (f *openBaoFake) Close() { f.server.Close() }

func (f *openBaoFake) client(t *testing.T, token string) *openbao.Client {
	t.Helper()
	return f.clientWithPaths(t, token, "", "")
}

func (f *openBaoFake) clientWithPaths(t *testing.T, token, repository, bundle string) *openbao.Client {
	t.Helper()
	certificate := f.server.Certificate()
	if len(certificate.Raw) == 0 {
		t.Fatal("test TLS server did not provide a certificate")
	}
	block := &pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}
	client, err := openbao.NewClient(openbao.ClientConfig{
		Address:         f.server.URL,
		ServerName:      "127.0.0.1",
		CACertBundle:    pem.EncodeToMemory(block),
		Token:           token,
		RepositoryRoot:  repository,
		BundleDirectory: bundle,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func (f *openBaoFake) initCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.initCount
}

func (f *openBaoFake) configureCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configureCount
}

func (f *openBaoFake) pkiMaxTTLSeconds() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pkiMaxLeaseTTL
}

func (f *openBaoFake) setReadyWithDrift() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.init = true
	f.sealed = false
	f.drift = true
}

func (f *openBaoFake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	switch {
	case r.Method == http.MethodGet && (path == "sys/seal-status" || path == "sys/health"):
		f.writeJSON(w, http.StatusOK, map[string]any{
			"initialized": f.init, "sealed": f.sealed, "t": 2, "n": 3,
			"progress": f.progress, "version": "2.7.0",
		})
	case r.Method == http.MethodPut && path == "sys/init":
		if f.init {
			f.writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"already initialized"}})
			return
		}
		var request struct {
			Shares    int `json:"secret_shares"`
			Threshold int `json:"secret_threshold"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Shares != 3 || request.Threshold != 2 {
			f.writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"invalid init parameters"}})
			return
		}
		f.init = true
		f.initCount++
		f.writeJSON(w, http.StatusOK, map[string]any{
			"keys_base64": []string{"share-one", "share-two", "share-three"},
			"root_token":  "root-token-canary",
		})
	case r.Method == http.MethodPost && path == "sys/unseal":
		var request struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || (request.Key != "share-one" && request.Key != "share-two" && request.Key != "share-three") {
			f.writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"invalid key"}})
			return
		}
		f.progress++
		if f.progress >= 2 {
			f.sealed = false
			f.progress = 0
		}
		f.writeJSON(w, http.StatusOK, map[string]any{"initialized": f.init, "sealed": f.sealed, "t": 2, "n": 3, "progress": f.progress, "version": "2.7.0"})
	case r.Method == http.MethodGet && path == "sys/mounts":
		mounts := map[string]any{}
		for name, mount := range f.mounts {
			mounts[name] = mount
		}
		if f.drift {
			mounts["transit/"] = map[string]any{"type": "kv"}
		}
		f.writeJSON(w, http.StatusOK, map[string]any{"data": mounts})
	case r.Method == http.MethodGet && path == "sys/mounts/pki/tune":
		f.writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"max_lease_ttl": f.pkiMaxLeaseTTL}})
	case r.Method == http.MethodPost && path == "sys/mounts/pki/tune":
		var value map[string]any
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			f.writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"invalid body"}})
			return
		}
		duration, err := time.ParseDuration(fmt.Sprint(value["max_lease_ttl"]))
		if err != nil {
			f.writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"invalid max_lease_ttl"}})
			return
		}
		f.pkiMaxLeaseTTL = duration.Seconds()
		f.configureCount++
		f.writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{}})
	case r.Method == http.MethodPost && strings.HasPrefix(path, "sys/mounts/"):
		if f.sealed {
			f.writeJSON(w, http.StatusForbidden, map[string]any{"errors": []string{"sealed"}})
			return
		}
		var value map[string]any
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			f.writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"invalid body"}})
			return
		}
		f.mounts[strings.TrimPrefix(path, "sys/mounts/")+"/"] = map[string]any{"type": value["type"]}
		f.configureCount++
		f.writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{}})
	case r.Method == http.MethodGet && path == "sys/auth":
		auth := map[string]any{}
		for name, method := range f.auth {
			auth[name] = method
		}
		f.writeJSON(w, http.StatusOK, map[string]any{"data": auth})
	case r.Method == http.MethodPost && strings.HasPrefix(path, "sys/auth/"):
		if f.sealed {
			f.writeJSON(w, http.StatusForbidden, map[string]any{"errors": []string{"sealed"}})
			return
		}
		var value map[string]any
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			f.writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"invalid body"}})
			return
		}
		f.auth[strings.TrimPrefix(path, "sys/auth/")+"/"] = map[string]any{"type": value["type"]}
		f.configureCount++
		f.writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{}})
	case r.Method == http.MethodGet && strings.HasPrefix(path, "sys/") && path != "sys/seal-status" && path != "sys/health":
		if value, ok := f.objects[path]; ok {
			f.writeJSON(w, http.StatusOK, map[string]any{"data": value})
		} else {
			f.writeJSON(w, http.StatusNotFound, map[string]any{"errors": []string{"not found"}})
		}
	case r.Method == http.MethodGet && (strings.HasPrefix(path, "auth/") || strings.HasPrefix(path, "transit/") || strings.HasPrefix(path, "pki/") || strings.HasPrefix(path, "ssh/")):
		if value, ok := f.objects[path]; ok {
			f.writeJSON(w, http.StatusOK, map[string]any{"data": value})
		} else if path == "pki/issuer/default/json" {
			f.writeJSON(w, http.StatusInternalServerError, map[string]any{"errors": []string{"no default issuer currently configured"}})
		} else {
			f.writeJSON(w, http.StatusNotFound, map[string]any{"errors": []string{"not found"}})
		}
	case r.Method == http.MethodPost && (strings.HasPrefix(path, "sys/") || strings.HasPrefix(path, "auth/") || strings.HasPrefix(path, "transit/") || strings.HasPrefix(path, "pki/") || strings.HasPrefix(path, "ssh/")):
		if f.sealed {
			f.writeJSON(w, http.StatusForbidden, map[string]any{"errors": []string{"sealed"}})
			return
		}
		var value map[string]any
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			f.writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"invalid body"}})
			return
		}
		if path == "pki/root/generate/internal" {
			f.objects["pki/issuer/default/json"] = map[string]any{"certificate": f.pkiCertificate}
		} else if path == "ssh/config/ca" {
			value["public_key"] = "ssh-ed25519 test-signing-public-key"
		}
		f.objects[path] = value
		f.configureCount++
		f.writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{}})
	default:
		f.writeJSON(w, http.StatusNotFound, map[string]any{"errors": []string{fmt.Sprintf("unknown test endpoint %s", path)}})
	}
}

func (f *openBaoFake) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		f.t.Errorf("encode fake OpenBao response: %v", err)
	}
}

func testPKICertificate(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate fake PKI key: %v", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "OpenBao test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(400 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create fake PKI certificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
