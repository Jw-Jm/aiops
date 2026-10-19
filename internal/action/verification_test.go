package action

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/resource"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func probeTarget(kind string) string {
	return resource.CanonicalID{Domain: "k8s", Tenant: "tenant", Scope: "cluster", APIGroup: "core", Kind: kind, StableID: "uid"}.String()
}
func TestVerificationSpecIsBoundToKindsAndFixedGET(t *testing.T) {
	for _, tc := range []struct {
		spec   VerificationSpec
		target string
		valid  bool
	}{
		{VerificationSpec{Kind: "tcp", Port: 80}, probeTarget("Service"), true},
		{VerificationSpec{Kind: "http", Port: 80, Path: "/healthz", ExpectedStatus: 200}, probeTarget("Service"), true},
		{VerificationSpec{Kind: "http", Port: 80, Path: "//outside", ExpectedStatus: 200}, probeTarget("Service"), false},
		{VerificationSpec{Kind: "http", Port: 80, Path: "/path?token=secret", ExpectedStatus: 200}, probeTarget("Service"), false},
		{VerificationSpec{Kind: "tcp", Port: 80, Path: "/"}, probeTarget("Service"), false},
		{VerificationSpec{Kind: "tcp", Port: 0}, probeTarget("Service"), false},
		{VerificationSpec{Kind: "pvc_read_write"}, probeTarget("PersistentVolumeClaim"), true},
		{VerificationSpec{Kind: "pvc_read_write", Path: "/business"}, probeTarget("PersistentVolumeClaim"), false},
		{VerificationSpec{Kind: "tcp", Port: 80}, probeTarget("Pod"), false},
	} {
		if tc.spec.Valid(tc.target) != tc.valid {
			t.Fatal(tc)
		}
	}
	b := Binding{Verification: &VerificationSpec{Kind: "http", Port: 80, Path: "/healthz", ExpectedStatus: 200}}
	original := b.Digest()
	changed := *b.Verification
	changed.ExpectedStatus = 201
	b.Verification = &changed
	if b.Digest() == original {
		t.Fatal("HTTP assertion excluded from confirmation")
	}
	var object unstructured.Unstructured
	_ = json.Unmarshal([]byte(`{"spec":{"clusterIP":"127.0.0.1","ports":[{"port":80}]}}`), &object)
	verdict, _, _, _ := probeService(context.Background(), VerificationSpec{Kind: "tcp", Port: 80}, object)
	if verdict != "inconclusive" {
		t.Fatal("loopback service address dialed")
	}
}
func TestNetworkVerificationActuallyConnectsAndChecksHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(200)
			_, _ = w.Write([]byte("healthy"))
		case "/redirect":
			w.Header().Set("Location", "/healthz")
			w.WriteHeader(302)
		case "/large":
			_, _ = w.Write([]byte(strings.Repeat("x", (64<<10)+1)))
		}
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	v, _, _, _ := runNetworkProbe(context.Background(), VerificationSpec{Kind: "tcp"}, address, "")
	if v != "resolved" {
		t.Fatal("TCP never connected", v)
	}
	spec := VerificationSpec{Kind: "http", Path: "/healthz", ExpectedStatus: 200, ExpectedBodyDigest: Digest([]byte("healthy"))}
	v, r, status, digest := runNetworkProbe(context.Background(), spec, address, "")
	if v != "resolved" || r != "http_assertions_met" || status != 200 || digest != spec.ExpectedBodyDigest {
		t.Fatal(v, r, status, digest)
	}
	spec.ExpectedBodyDigest = Digest([]byte("other"))
	v, r, _, _ = runNetworkProbe(context.Background(), spec, address, "")
	if v != "not_resolved" || r != "http_body_mismatch" {
		t.Fatal(v, r)
	}
	spec.Path = "/redirect"
	v, r, status, _ = runNetworkProbe(context.Background(), spec, address, "")
	if v != "not_resolved" || r != "http_status_mismatch" || status != 302 {
		t.Fatal("redirect followed", v, r, status)
	}
	spec.Path = "/large"
	v, r, _, _ = runNetworkProbe(context.Background(), spec, address, "")
	if v != "inconclusive" {
		t.Fatal("oversize response accepted", v, r)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	v, r, _, _ = runNetworkProbe(ctx, VerificationSpec{Kind: "tcp"}, address, "")
	if v != "inconclusive" || r != "probe_timeout" {
		t.Fatal(v, r)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	_ = listener.Close()
	v, r, _, _ = runNetworkProbe(context.Background(), VerificationSpec{Kind: "tcp"}, closed, "")
	if v != "not_resolved" || r != "connection_refused" {
		t.Fatal(v, r)
	}
}
func TestReadWriteVerificationOnlyUsesItsNonceFile(t *testing.T) {
	dir := t.TempDir()
	business := filepath.Join(dir, "business")
	if err := os.WriteFile(business, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	verdict, reason, cleanup := probeReadWrite(context.Background(), dir, uuid.New())
	if verdict != "resolved" || reason != "read_write_verified" || !cleanup {
		t.Fatal(verdict, reason, cleanup)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "business" {
		t.Fatal("probe left files", entries, err)
	}
	data, _ := os.ReadFile(business)
	if string(data) != "untouched" {
		t.Fatal("business file modified")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, _, clean := probeReadWrite(ctx, dir, uuid.New())
	if v != "inconclusive" || !clean {
		t.Fatal("cancelled probe passed or leaked file", v, clean)
	}
}
func TestVerificationResultCannotCrossExecutionOrSkipAssertions(t *testing.T) {
	started := time.Now().Add(-2 * time.Second)
	finished := started.Add(time.Second)
	b := Binding{Verification: &VerificationSpec{Kind: "http", Port: 80, Path: "/healthz", ExpectedStatus: 200}, VerificationNamespace: "runners", TargetUID: "uid"}
	e := Execution{ID: uuid.New(), Binding: b, StartedAt: &started, CompletedAt: &finished, RequestDigest: b.Digest()}
	result := VerificationResult{SchemaVersion: "command-verification/v1", ExecutionID: e.ID, RequestDigest: e.RequestDigest, TargetUID: "uid", Kind: "http", Vantage: "runner/runners", StartedAt: started, FinishedAt: finished, Verdict: "resolved", Reason: "http_assertions_met", HTTPStatus: 200, BodyDigest: Digest([]byte("body"))}
	if !result.Valid(e) {
		t.Fatal(result)
	}
	for _, change := range []func(*VerificationResult){func(r *VerificationResult) { r.ExecutionID = uuid.New() }, func(r *VerificationResult) { r.TargetUID = "recreated" }, func(r *VerificationResult) { r.RequestDigest = "other" }, func(r *VerificationResult) { r.Vantage = "runner/other" }, func(r *VerificationResult) { r.StartedAt = started.Add(-time.Second) }, func(r *VerificationResult) { r.HTTPStatus = 500 }, func(r *VerificationResult) { r.BodyDigest = "" }} {
		bad := result
		change(&bad)
		if bad.Valid(e) {
			t.Fatal("bad receipt accepted", bad)
		}
	}
}

func TestTypedRunnerReturnsOnlyReceiptAndDoesNotReuseKubernetesCredentials(t *testing.T) {
	t.Setenv("OPS_VERIFICATION_NAMESPACE", "runners")
	calls := 0
	kube := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer K8S_PRIVATE" {
			t.Error("missing subject credential")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"svc","namespace":"ns","uid":"uid","resourceVersion":"1"},"spec":{"clusterIP":"127.0.0.1","ports":[{"port":80}]}}`))
	}))
	defer kube.Close()
	ns := "ns"
	b := Binding{Verification: &VerificationSpec{Kind: "tcp", Port: 80}, Target: probeTarget("Service"), TargetUID: "uid", Namespace: &ns, VerificationNamespace: "runners", VerificationTargetName: "svc"}
	var output []byte
	var result *VerificationResult
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Runner-Token") != strings.Repeat("a", 43) {
			t.Error("missing execution token")
		}
		if strings.HasSuffix(r.URL.Path, "/output") {
			var chunk OutputChunk
			if err := json.NewDecoder(r.Body).Decode(&chunk); err != nil {
				t.Error(err)
			}
			output = chunk.Bytes
		} else if strings.HasSuffix(r.URL.Path, "/finish") {
			var payload struct {
				Verification *VerificationResult `json:"verification"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			result = payload.Verification
		} else {
			t.Error("unexpected callback", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer callback.Close()
	id := uuid.New()
	client := RunnerClient{Client: callback.Client(), Endpoint: callback.URL, Token: strings.Repeat("a", 43), TenantID: uuid.New(), ExecutionID: id}
	claim := ClaimedCommand{ExecutionID: id, Command: "ops-verify tcp", VerificationBinding: &b, VerificationTargetName: "svc", Credentials: RunnerCredentials{Kubernetes: &KubeCredential{Endpoint: kube.URL, CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: kube.Certificate().Raw}), Token: "K8S_PRIVATE", Namespace: ns}}}
	if err := client.runVerification(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result == nil || result.Verdict != "inconclusive" || result.Vantage != "runner/runners" || string(output) != string(Canonical(result)) || strings.Contains(string(output), "K8S_PRIVATE") {
		t.Fatal("invalid receipt path", calls, result)
	}
	claim.Command = "ops-verify tcp; echo injected"
	if err := client.runVerification(context.Background(), claim); err == nil {
		t.Fatal("typed probe accepted Bash")
	}
}

func TestReadWriteVerificationRejectsExistingSymlinkWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "business")
	if err := os.WriteFile(outside, []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	link := filepath.Join(dir, "probe-"+id.String())
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	verdict, _, cleanup := probeReadWrite(context.Background(), dir, id)
	if verdict != "inconclusive" || cleanup {
		t.Fatal("existing path used as probe", verdict, cleanup)
	}
	body, _ := os.ReadFile(outside)
	if string(body) != "protected" {
		t.Fatal("business data overwritten")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("unowned entry removed", err)
	}
}
