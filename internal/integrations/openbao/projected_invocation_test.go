package openbao

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentInvocationSigningRenewsRotatedProjectedTokenAndFailsClosed(t *testing.T) {
	ca, key := makeTestCA(t)
	publicDER, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	var logins, transitCalls atomic.Int32
	var deny atomic.Bool
	server, client := newPKITestServer(t, ca, key, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/kubernetes/login" {
			if r.Header.Get("X-Vault-Token") != "" {
				t.Error("Transit credential sent on Kubernetes login")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if body["role"] != "ops-api-workload" {
				t.Error("incorrect workload authentication scope")
			}
			logins.Add(1)
			if deny.Load() {
				w.WriteHeader(403)
				return
			}
			writeJSON(w, map[string]any{"auth": map[string]any{"client_token": "bao-" + body["jwt"], "lease_duration": 900}})
			return
		}
		transitCalls.Add(1)
		want := "bao-projected-service-account-token"
		if logins.Load() > 1 {
			want = "bao-rotated-projected-token"
		}
		if r.Header.Get("X-Vault-Token") != want {
			t.Error("Transit call used stale credential")
		}
		switch r.URL.Path {
		case "/v1/transit/keys/investigation-signing":
			writeJSON(w, map[string]any{"data": map[string]any{"type": "ecdsa-p256", "keys": map[string]any{"1": map[string]any{"public_key": publicPEM}}}})
		case "/v1/transit/sign/investigation-signing":
			writeJSON(w, map[string]any{"data": map[string]any{"signature": "vault:v1:" + base64.RawURLEncoding.EncodeToString(make([]byte, 64))}})
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	token := writeProjectedToken(t)
	signer, err := NewProjectedInvocationSigning(client, token, "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	signer.now = func() time.Time { return now }
	if _, _, err := signer.SigningKeys(context.Background(), "investigation-signing"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("rotated-projected-token"), 0400); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	var group sync.WaitGroup
	for range 10 {
		group.Go(func() {
			if _, err := signer.SignJWS(context.Background(), "investigation-signing", 1, []byte("owned Context")); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if logins.Load() != 2 || transitCalls.Load() != 11 {
		t.Fatalf("concurrent renewal count: logins=%d transit=%d", logins.Load(), transitCalls.Load())
	}
	now = now.Add(10 * time.Minute)
	deny.Store(true)
	if _, err := signer.SignJWS(context.Background(), "investigation-signing", 1, []byte("must reject")); err == nil || transitCalls.Load() != 11 {
		t.Fatal("failed authentication fell back to previous credential")
	}
}
