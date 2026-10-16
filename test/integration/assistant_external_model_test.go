package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/modelprofile"
)

// This opt-in gateway belongs only to the isolated test harness. Production
// profiles retain private egress admission; no external weight admission is implied.
func assistantAcceptanceModel(t *testing.T, p *modelprofile.Profile) string {
	t.Helper()
	path := os.Getenv("OPS_ASSISTANT_DEEPSEEK_KEY_FILE")
	if path == "" {
		return "local-ollama-no-auth"
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("external acceptance key requires a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		t.Fatal("external acceptance key unavailable")
	}
	token := uuid.NewString()
	diagnostics, err := os.OpenFile(path+".errors-"+uuid.NewString(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("private external diagnostics unavailable")
	}
	t.Cleanup(func() { _ = diagnostics.Close() })
	gateway := httptest.NewServer(assistantAcceptanceGateway("https://api.deepseek.com", strings.TrimSpace(string(raw)), token, diagnostics))
	t.Cleanup(gateway.Close)
	p.Version = "deepseek-external-acceptance/v1"
	p.ModelID = "deepseek-flash"
	p.ModelVersion = "deepseek-flash"
	p.BaseURL = gateway.URL + "/v1"
	t.Log("external acceptance: deepseek-flash via api.deepseek.com; fixture profile, paid upstream; original 16k/2048/120s limits retained")
	return token
}

func assistantAcceptanceGateway(endpoint, key, token string, diagnostics io.Writer) http.Handler {
	target, _ := url.Parse(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 120 * time.Second}
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "acceptance upstream unavailable", http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(r *http.Response) error {
		if r.StatusCode >= 400 {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 65536))
			if err != nil {
				return err
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			// Provider errors stay in private remote diagnostics, never test logs.
			_ = json.NewEncoder(diagnostics).Encode(map[string]any{"status": r.StatusCode, "body": strings.ReplaceAll(strings.ReplaceAll(string(raw), key, "[redacted]"), token, "[redacted]")})
		}
		return nil
	}
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		r.Header = http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer " + key}}
	}
	var calls atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "acceptance request denied", http.StatusForbidden)
			return
		}
		if calls.Add(1) > 24 {
			http.Error(w, "acceptance call cap reached", http.StatusTooManyRequests)
			return
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body) != nil || string(body["model"]) != `"deepseek-flash"` {
			http.Error(w, "invalid acceptance payload", http.StatusBadRequest)
			return
		}
		// Keep the frozen output budget for answers rather than hidden reasoning.
		body["thinking"] = json.RawMessage(`{"type":"disabled"}`)
		var format struct {
			Type   string `json:"type"`
			Schema struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		if json.Unmarshal(body["response_format"], &format) == nil && format.Type == "json_schema" {
			// DeepSeek supports JSON mode, not provider-enforced response schemas.
			// Carry the exact schema as instructions; platform validation stays strict.
			var messages []map[string]json.RawMessage
			if len(format.Schema.Schema) == 0 || json.Unmarshal(body["messages"], &messages) != nil {
				http.Error(w, "invalid acceptance schema", http.StatusBadRequest)
				return
			}
			instruction, _ := json.Marshal("Return JSON conforming to this required schema: " + string(format.Schema.Schema))
			messages = append([]map[string]json.RawMessage{{"role": json.RawMessage(`"system"`), "content": instruction}}, messages...)
			body["messages"], _ = json.Marshal(messages)
			body["response_format"] = json.RawMessage(`{"type":"json_object"}`)
		}
		raw, _ := json.Marshal(body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
		proxy.ServeHTTP(w, r)
	})
}

func TestAssistantAcceptanceGateway(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer upstream-secret" || r.Header.Get("X-Private") != "" || r.URL.Path != "/v1/chat/completions" {
			t.Error("unexpected upstream request")
		}
		var body struct {
			Thinking struct {
				Type string `json:"type"`
			} `json:"thinking"`
			Format struct {
				Type string `json:"type"`
			} `json:"response_format"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Thinking.Type != "disabled" || body.Format.Type != "json_object" || len(body.Messages) != 2 || !strings.Contains(body.Messages[0].Content, `"additionalProperties":false`) || body.Messages[1].Content != "original" {
			t.Error("schema translation changed the original request")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	h := assistantAcceptanceGateway(upstream.URL, "upstream-secret", "local-token", io.Discard)
	for i := 0; i < 27; i++ {
		path, token, expected := "/v1/chat/completions", "local-token", http.StatusOK
		if i == 0 {
			token, expected = "wrong", http.StatusForbidden
		}
		if i == 1 {
			path, expected = "/v1/models", http.StatusForbidden
		}
		if i == 26 {
			expected = http.StatusTooManyRequests
		}
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"deepseek-flash","messages":[{"role":"user","content":"original"}],"response_format":{"type":"json_schema","json_schema":{"schema":{"type":"object","additionalProperties":false}}}}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Private", "do-not-forward")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatalf("request %d: status %d, want %d", i, w.Code, expected)
		}
	}
	if calls != 24 {
		t.Fatalf("upstream calls %d", calls)
	}
}
