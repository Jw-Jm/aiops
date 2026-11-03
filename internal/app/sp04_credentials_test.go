package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func projectedTestToken(t *testing.T, issuedAt, expiresAt time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test"}`))
	payload, err := json.Marshal(map[string]any{"iat": issuedAt.Unix(), "exp": expiresAt.Unix(), "sub": "system:serviceaccount:ops:ops-worker"})
	if err != nil {
		t.Fatal(err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + strings.Repeat("s", 256)
}

func writeCredentialFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A projected token file is rewritten by the kubelet. Every request must read
// the current file, and a missing, empty, multi-line or non-JWT value must fail
// closed before a request is sent.
func TestClusterCredentialIsReadPerRequestAndRejectsInvalidFiles(t *testing.T) {
	issued := time.Now().Add(-time.Minute).Truncate(time.Second)
	expires := issued.Add(600 * time.Second)
	path := writeCredentialFile(t, projectedTestToken(t, issued, expires))
	first, err := readProjectedToken(path)
	if err != nil {
		t.Fatalf("projected token rejected: %v", err)
	}
	rotated := projectedTestToken(t, expires, expires.Add(600*time.Second))
	if err := os.WriteFile(path, []byte(rotated), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := readProjectedToken(path)
	if err != nil {
		t.Fatalf("rotated projected token rejected: %v", err)
	}
	if first == second {
		t.Fatal("client did not observe the rotated token file")
	}
	for name, content := range map[string]string{
		"empty":      "",
		"whitespace": "   \n",
		"multi line": projectedTestToken(t, issued, expires) + "\n" + projectedTestToken(t, issued, expires),
		"opaque":     "static-cluster-token-without-jwt-shape",
		"short":      "a.b",
	} {
		t.Run(name, func(t *testing.T) {
			badPath := writeCredentialFile(t, content)
			if _, err := readProjectedToken(badPath); err == nil {
				t.Fatal("invalid credential file accepted")
			}
		})
	}
	if _, err := readProjectedToken(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing credential file accepted")
	}
}

func TestClusterCredentialTransportSendsOnlyTheCurrentProjectedToken(t *testing.T) {
	issued := time.Now().Add(-time.Minute).Truncate(time.Second)
	expires := issued.Add(600 * time.Second)
	path := writeCredentialFile(t, projectedTestToken(t, issued, expires))
	var seen []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	client := &http.Client{Transport: clusterCredentialTransport{base: transport, file: path}}
	request, _ := http.NewRequestWithContext(context.Background(), "GET", "https://cluster.test/api/v1/pods", nil)
	if _, err := client.Do(request); err != nil {
		t.Fatal(err)
	}
	rotated := projectedTestToken(t, expires, expires.Add(600*time.Second))
	if err := os.WriteFile(path, []byte(rotated), 0o600); err != nil {
		t.Fatal(err)
	}
	request, _ = http.NewRequestWithContext(context.Background(), "GET", "https://cluster.test/api/v1/pods", nil)
	if _, err := client.Do(request); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] == seen[1] || !strings.HasPrefix(seen[0], "Bearer ") || !strings.HasPrefix(seen[1], "Bearer ") {
		t.Fatal("rotated projected token was not used for the next request")
	}
	if request.Header.Get("Authorization") != "" {
		t.Fatal("caller request was mutated with a credential")
	}
}

// Rotation evidence must stay bounded: a digest prefix and validity window, no
// token bytes in logs.
func TestProjectionObservationRecordsOnlyDigestAndValidity(t *testing.T) {
	issued := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	expires := issued.Add(600 * time.Second)
	token := projectedTestToken(t, issued, expires)
	path := writeCredentialFile(t, token)
	metadata, err := observeProjection(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.DigestPrefix) != 8 || !metadata.IssuedAt.Equal(issued) || !metadata.ExpiresAt.Equal(expires) {
		t.Fatalf("unexpected rotation metadata: %#v", metadata)
	}
	if strings.Contains(metadata.DigestPrefix, token[:8]) {
		t.Fatal("digest prefix is token material")
	}
	var buffer strings.Builder
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchClusterCredential(ctx, logger, SP04Cluster{ClusterUID: "cluster-a", TokenFile: path})
	}()
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(path, []byte(projectedTestToken(t, expires, expires.Add(600*time.Second))), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(11 * time.Second)
	cancel()
	<-done
	logged := buffer.String()
	if !strings.Contains(logged, "credential rotated") || !strings.Contains(logged, metadata.DigestPrefix) {
		t.Fatalf("rotation was not recorded: %s", logged)
	}
	if strings.Contains(logged, token) || strings.Contains(logged, token[100:160]) {
		t.Fatal("rotation log contains token material")
	}
	if !strings.Contains(logged, "expiresAt") || !strings.Contains(logged, "issuedAt") {
		t.Fatal("rotation record omitted the token validity window")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
