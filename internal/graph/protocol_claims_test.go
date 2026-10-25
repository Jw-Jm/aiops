package graph

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, private
}

func claimPayload(t *testing.T, scope Scope) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"scope": scope})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestVerifyContextRequiresAnExplicitKnownSource pins the source model the review
// asked for: legacy, native interactive and trusted persisted task are the only
// accepted values, and a native source must carry exactly its own reference.
func TestVerifyContextRequiresAnExplicitKnownSource(t *testing.T) {
	public, private := testKeyPair(t)
	scope := Scope{Tenant: "tenant-a", Cluster: "cluster-a", ClusterScoped: true}
	payload := claimPayload(t, scope)
	base := func() Claims {
		return Claims{
			Source:      SourceLegacy,
			Audience:    "platform-graph-query",
			Subject:     "user:one",
			Scope:       scope,
			Deadline:    time.Now().Add(10 * time.Second),
			QueryDigest: ScopeDigest(json.RawMessage(payload)),
		}
	}
	sign := func(claims Claims) string {
		t.Helper()
		token, err := SignContext(private, claims)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	verify := func(token string) error {
		t.Helper()
		_, err := VerifyContext(public, token, "platform-graph-query", payload)
		return err
	}

	legacy := base()
	if err := verify(sign(legacy)); err != nil {
		t.Fatalf("legacy claim rejected: %v", err)
	}

	interactive := base()
	interactive.Source = SourceNativeInteractive
	interactive.Native = &NativeProof{SessionID: uuid.Must(uuid.NewV7()).String()}
	if err := verify(sign(interactive)); err != nil {
		t.Fatalf("native interactive claim rejected: %v", err)
	}

	task := base()
	task.Source = SourceNativeTask
	task.Native = &NativeProof{JobID: uuid.Must(uuid.NewV7()).String()}
	if err := verify(sign(task)); err != nil {
		t.Fatalf("native task claim rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Claims){
		"empty source":           func(c *Claims) { c.Source = "" },
		"unknown source":         func(c *Claims) { c.Source = "oidc" },
		"legacy carrying native": func(c *Claims) { c.Native = &NativeProof{SessionID: uuid.Must(uuid.NewV7()).String()} },
		"native without proof":   func(c *Claims) { c.Source = SourceNativeInteractive; c.Native = nil },
		"interactive with job": func(c *Claims) {
			c.Source = SourceNativeInteractive
			c.Native = &NativeProof{JobID: uuid.Must(uuid.NewV7()).String()}
		},
		"task with session": func(c *Claims) {
			c.Source = SourceNativeTask
			c.Native = &NativeProof{SessionID: uuid.Must(uuid.NewV7()).String()}
		},
		"interactive bad uuid":    func(c *Claims) { c.Source = SourceNativeInteractive; c.Native = &NativeProof{SessionID: "not-a-uuid"} },
		"task without either":     func(c *Claims) { c.Source = SourceNativeTask; c.Native = &NativeProof{} },
		"subject prefix fallback": func(c *Claims) { c.Subject = "" },
	} {
		claims := base()
		mutate(&claims)
		if err := verify(sign(claims)); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// TestVerifyContextBindsPayloadDeadlineAndSignature keeps the existing internal
// envelope guarantees while the source field is added.
func TestVerifyContextBindsPayloadDeadlineAndSignature(t *testing.T) {
	public, private := testKeyPair(t)
	otherPublic, otherPrivate := testKeyPair(t)
	_ = otherPublic
	scope := Scope{Tenant: "tenant-a", Cluster: "cluster-a", ClusterScoped: true}
	payload := claimPayload(t, scope)
	claims := Claims{
		Source: SourceLegacy, Audience: "platform-graph-query", Subject: "user:one",
		Scope: scope, Deadline: time.Now().Add(10 * time.Second),
		QueryDigest: ScopeDigest(json.RawMessage(payload)),
	}
	token, err := SignContext(private, claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyContext(public, token, "platform-graph-query", payload); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if _, err := VerifyContext(public, token, "platform-audit-verify", payload); err == nil {
		t.Fatal("audience mismatch accepted")
	}
	if _, err := VerifyContext(public, token, "platform-graph-query", []byte(`{"scope":{}}`)); err == nil {
		t.Fatal("payload substitution accepted")
	}
	if _, err := VerifyContext(public, token, "platform-graph-query", []byte(`{"scope":{"tenant":"tenant-b","cluster":"cluster-a","clusterScoped":true}}`)); err == nil {
		t.Fatal("tenant substitution accepted")
	}
	forged, err := SignContext(otherPrivate, claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyContext(public, forged, "platform-graph-query", payload); err == nil {
		t.Fatal("foreign signature accepted")
	}

	expired := claims
	expired.Deadline = time.Now().Add(-time.Second)
	if token, err = SignContext(private, expired); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyContext(public, token, "platform-graph-query", payload); err == nil {
		t.Fatal("expired token accepted")
	}
	far := claims
	far.Deadline = time.Now().Add(31 * time.Second)
	if token, err = SignContext(private, far); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyContext(public, token, "platform-graph-query", payload); err == nil {
		t.Fatal("token beyond the 30 second window accepted")
	}

	// Re-encoding the same claims with an unknown field must be refused instead of
	// silently ignored.
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"queryDigest"`, `"injected":"value","queryDigest"`, 1))
	signature := ed25519.Sign(private, raw)
	extra := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(signature)
	if _, err := VerifyContext(public, extra, "platform-graph-query", payload); err == nil {
		t.Fatal("unknown claim field accepted")
	}
}
