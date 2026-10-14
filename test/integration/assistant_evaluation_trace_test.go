package integration

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"ops-platform/internal/analytics"
	"ops-platform/internal/investigation"
)

// Fixture-only transparent MCP transport. No model client or alternative tool
// implementation: all calls still go to the real signed production gateway.
// Only typed QueryIntent and step ID are retained; auth headers/tokens are not.
type assistantEvaluationTrace struct {
	mu               sync.Mutex
	intents          map[string]analytics.QueryIntent
	digests          map[string]string
	recoveryPaused   chan struct{}
	recoveryResume   chan struct{}
	recoveryOnce     sync.Once
	restartProcesses func()
}

func assistantEvaluationProxy(t *testing.T, address string, files map[string]string) (string, *assistantEvaluationTrace) {
	t.Helper()
	trace := &assistantEvaluationTrace{intents: map[string]analytics.QueryIntent{}, digests: map[string]string{}}
	if os.Getenv("OPS_ASSISTANT_DISPATCHER_BINARY") != "" {
		trace.recoveryPaused = make(chan struct{})
		trace.recoveryResume = make(chan struct{})
	}
	ca, err := os.ReadFile(files["ca.pem"])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("evaluation CA")
	}
	serverCert, err := tls.LoadX509KeyPair(files["ops-api.pem"], files["ops-api.key"])
	if err != nil {
		t.Fatal(err)
	}
	clientCert, err := tls.LoadX509KeyPair(files["ops-investigator.pem"], files["ops-investigator.key"])
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse("https://" + address)
	proxy := httputil.NewSingleHostReverseProxy(target)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{clientCert}, ServerName: "ops-api.sp06-test.svc.cluster.local"}}
	proxy.Transport = transport
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.PeerCertificates[0].URIs) != 1 || r.TLS.PeerCertificates[0].URIs[0].String() != "spiffe://ops.local/ns/sp06-test/sa/ops-investigator" {
			http.Error(w, "unauthorized fixture workload", 403)
			return
		}
		if r.URL.Path != "/mcp" && !strings.HasPrefix(r.URL.Path, "/internal/v1/investigations/") {
			http.NotFound(w, r)
			return
		}
		if r.Method == "POST" {
			raw, err := io.ReadAll(io.LimitReader(r.Body, (128<<10)+1))
			r.Body.Close()
			if err != nil || len(raw) > 128<<10 {
				http.Error(w, "invalid MCP request", 400)
				return
			}
			var message struct {
				Method string `json:"method"`
				Params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
					Meta      map[string]any  `json:"_meta"`
				} `json:"params"`
			}
			if trace.recoveryPaused != nil && json.Unmarshal(raw, &message) == nil && message.Method == "tools/call" && message.Params.Name == "get_dependencies" {
				paused := false
				trace.recoveryOnce.Do(func() { paused = true; close(trace.recoveryPaused) })
				if paused {
					select {
					case <-trace.recoveryResume:
					case <-r.Context().Done():
					}
					http.Error(w, "acceptance transport interrupted", http.StatusServiceUnavailable)
					return
				}
			}
			if json.Unmarshal(raw, &message) == nil && message.Method == "tools/call" && message.Params.Name == "execute_analysis_query" {
				var arguments struct {
					Intent json.RawMessage `json:"intent"`
				}
				if json.Unmarshal(message.Params.Arguments, &arguments) == nil {
					// Decode rejects open expressions, endpoints and additional properties.
					intent, err := analytics.DecodeIntent(arguments.Intent)
					step, ok := message.Params.Meta["ops/stepId"].(string)
					if err == nil && ok {
						trace.mu.Lock()
						trace.intents[step] = intent
						trace.digests[step] = investigation.ArgumentsDigest(message.Params.Arguments)
						trace.mu.Unlock()
					}
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		proxy.ServeHTTP(w, r)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	server.StartTLS()
	t.Cleanup(server.Close)
	t.Cleanup(transport.CloseIdleConnections)
	return server.URL + "/mcp", trace
}
