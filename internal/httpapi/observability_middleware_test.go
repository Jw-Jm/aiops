package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ops-platform/internal/auth"
	"ops-platform/internal/observability"
)

func TestInstrumentedGeneratedHandlerExposesMetricsEndpoint(t *testing.T) {
	metrics := observability.NewMetrics()
	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	tracing, err := observability.NewTracing(context.Background(), observability.TracingConfig{ServiceName: "ops-platform-api"}, metrics, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer tracing.Close(context.Background())
	handler := NewHandlerWithObservability(nil, logger, metrics, tracing)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "platform_process_up 1") {
		t.Fatalf("generated API handler did not expose its metrics registry: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRequestCorrelationReachesAuthenticationErrors(t *testing.T) {
	var logs strings.Builder
	handler := ObservabilityMiddleware(observability.NewLogger(&logs, slog.LevelInfo), nil, nil)(auth.RequireRole(auth.PlatformAdmin)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/tenants", nil))
	id := response.Header().Get("X-Request-ID")
	if id == "" || !strings.Contains(logs.String(), `"request_id":"`+id+`"`) || !strings.Contains(response.Body.String(), `"requestId":"`+id+`"`) {
		t.Fatal("authentication response and request log have different correlation IDs")
	}
}

func TestObservabilityMiddlewarePreservesStreamingFlush(t *testing.T) {
	metrics := observability.NewMetrics()
	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	tracing, err := observability.NewTracing(context.Background(), observability.TracingConfig{ServiceName: "ops-platform-api"}, metrics, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer tracing.Close(context.Background())
	handler := ObservabilityMiddleware(logger, metrics, tracing)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("instrumentation removed http.Flusher from the response writer")
			return
		}
		_, _ = w.Write([]byte("event: ready\n\ndata: {}\n\n"))
		flusher.Flush()
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/events", nil))
	if response.Code != http.StatusOK || !response.Flushed {
		t.Fatalf("stream response was not flushed: status=%d flushed=%v", response.Code, response.Flushed)
	}
}
