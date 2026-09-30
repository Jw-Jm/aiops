package observability_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/trace"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/observability"
)

func TestStructuredLogsRedactSecretsCommandsAndPrompts(t *testing.T) {
	var output strings.Builder
	logger := observability.NewLogger(&output, slog.LevelInfo)
	logger.Info("request completed: Bearer inline-secret-value",
		"request_id", "req-7",
		"authorization", "Bearer header-secret-value",
		"database_url", "postgres://user:db-secret@db.invalid/platform",
		"command", "kubectl get secret production-key",
		"prompt", "inspect and reveal the customer data",
	)
	var record map[string]any
	if err := json.Unmarshal([]byte(output.String()), &record); err != nil {
		t.Fatalf("decode JSON log: %v", err)
	}
	if record["request_id"] != "req-7" {
		t.Fatalf("request id was not retained as a structured field: %#v", record)
	}
	encoded := output.String()
	for _, forbidden := range []string{"inline-secret-value", "header-secret-value", "db-secret", "production-key", "customer data"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("log leaked sensitive input %q: %s", forbidden, encoded)
		}
	}
	for _, field := range []string{"authorization", "database_url", "command", "prompt"} {
		if record[field] != observability.RedactedValue {
			t.Fatalf("field %s was not redacted: %#v", field, record[field])
		}
	}
}

func TestCorrelationChainIsStructuredAndTraceLinked(t *testing.T) {
	var output strings.Builder
	logger := observability.NewLogger(&output, slog.LevelInfo)
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{
		RequestID: "request-original", FindingID: "finding-1", IncidentID: "incident-1",
		InvestigationJobID: "job-1", ActionPlanID: "plan-1", ExecutionID: "execution-1",
	})
	ctx = observability.WithRequestID(ctx, "request-2")
	ctx = trace.ContextWithSpanContext(ctx, spanContext)
	observability.LoggerWithContext(ctx, logger).InfoContext(ctx, "correlation event")
	var record map[string]any
	if err := json.Unmarshal([]byte(output.String()), &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"request_id": "request-2", "finding_id": "finding-1", "incident_id": "incident-1",
		"investigation_job_id": "job-1", "action_plan_id": "plan-1", "execution_id": "execution-1",
		"trace_id": traceID.String(), "span_id": spanID.String(),
	} {
		if record[key] != want {
			t.Errorf("%s = %#v, want %q", key, record[key], want)
		}
	}
}

func TestRequestMiddlewarePropagatesTraceAndReturnsSafeRequestIDOnError(t *testing.T) {
	ctx := context.Background()
	metrics := observability.NewMetrics()
	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	tracing, err := observability.NewTracing(ctx, observability.TracingConfig{ServiceName: "ops-platform-api"}, metrics, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer tracing.Close(context.Background())
	router := chi.NewRouter()
	router.Use(httpapi.ObservabilityMiddleware(logger, metrics, tracing))
	router.Get("/tenants/{tenantID}/sources/{sourceID}", func(w http.ResponseWriter, r *http.Request) {
		spanContext := trace.SpanFromContext(r.Context()).SpanContext()
		if !spanContext.IsValid() || spanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Errorf("incoming trace context was not propagated: %s", spanContext.TraceID())
		}
		if observability.RequestIDFromContext(r.Context()) == "raw\r\nheader" || observability.RequestIDFromContext(r.Context()) == "" {
			t.Error("invalid request id was reflected or no request id was generated")
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	request := httptest.NewRequest(http.MethodGet, "/tenants/customer-secret/sources/source-secret", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request.Header.Set("X-Request-ID", "raw\r\nheader")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("middleware changed the handler response: status=%d", response.Code)
	}
	if response.Header().Get("X-Request-ID") == "" || response.Header().Get("X-Request-ID") == "raw\r\nheader" {
		t.Fatalf("error response did not carry a safe request id: %#v", response.Header())
	}
	if strings.Contains(response.Header().Get("X-Request-ID"), "customer-secret") {
		t.Fatal("request ID reflected tenant-controlled URL content")
	}
}

func TestMetricsExposeBoundedLabelsWithoutTenantOrResourceIDs(t *testing.T) {
	metrics := observability.NewMetrics()
	if !metrics.RecordOperation("api", "request", "ok") {
		t.Fatal("rejected an allowed low-cardinality operation")
	}
	if metrics.RecordOperation("tenant-customer-secret", "request", "ok") || metrics.RecordOperation("api", "resource-123", "ok") {
		t.Fatal("accepted an unbounded or identity-bearing metric label")
	}
	router := chi.NewRouter()
	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	tracing, err := observability.NewTracing(context.Background(), observability.TracingConfig{ServiceName: "ops-platform-api"}, metrics, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer tracing.Close(context.Background())
	router.Use(httpapi.ObservabilityMiddleware(logger, metrics, tracing))
	router.Get("/tenants/{tenantID}/sources/{sourceID}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, path := range []string{"/tenants/customer-secret/sources/source-a", "/tenants/other-customer/sources/source-b"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	}
	scrape := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()
	for _, identifier := range []string{"customer-secret", "other-customer", "source-a", "source-b", "tenant-customer-secret", "resource-123"} {
		if strings.Contains(body, identifier) {
			t.Fatalf("metrics output contains an unbounded identifier %q", identifier)
		}
	}
	if !strings.Contains(body, "platform_http_requests_total") || !strings.Contains(body, "platform_operations_total") {
		t.Fatalf("expected platform metrics were not exposed: %s", body)
	}
}

func TestTraceExportCanBeDisabledAndExporterFailureDoesNotFailRequests(t *testing.T) {
	metrics := observability.NewMetrics()
	logger := observability.NewLogger(io.Discard, slog.LevelInfo)
	tracing, err := observability.NewTracing(context.Background(), observability.TracingConfig{ServiceName: "ops-platform-api", Endpoint: ""}, metrics, logger)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.ObservabilityMiddleware(logger, metrics, tracing)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("disabled tracing affected the main path: status=%d", response.Code)
	}
	if err := tracing.Close(context.Background()); err != nil {
		t.Fatalf("close disabled tracing: %v", err)
	}

	unavailable, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + unavailable.Addr().String()
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	tracing, err = observability.NewTracing(context.Background(), observability.TracingConfig{ServiceName: "ops-platform-api", Endpoint: endpoint, SampleRatio: 1}, metrics, logger)
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	httpapi.ObservabilityMiddleware(logger, metrics, tracing)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusNoContent {
		t.Fatal("unavailable trace collector changed the business response")
	}
	flushContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	flushErr := tracing.Flush(flushContext)
	closeErr := tracing.Close(flushContext)
	if flushErr == nil && closeErr == nil || !metrics.Degraded() {
		t.Fatalf("disconnected trace exporter was not reflected as observability degradation; flush=%v close=%v", flushErr, closeErr)
	}
}

func TestTraceContextCanBeInjectedForDownstreamCalls(t *testing.T) {
	metrics := observability.NewMetrics()
	tracing, err := observability.NewTracing(context.Background(), observability.TracingConfig{ServiceName: "ops-platform-api"}, metrics, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tracing.Close(context.Background())
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	remote := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled, Remote: true})
	parentContext := trace.ContextWithRemoteSpanContext(context.Background(), remote)
	ctx, span := tracing.Tracer("test").Start(parentContext, "parent")
	defer span.End()
	headers := make(http.Header)
	tracing.Inject(ctx, headers)
	if headers.Get("traceparent") == "" {
		t.Fatal("trace context was not injected for a downstream request")
	}
}

func TestMetricsListenerFailureOnlyDegradesObservability(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer reserved.Close()
	runtime := &observability.Runtime{Metrics: observability.NewMetrics()}
	if err := runtime.ServeMetrics(context.Background(), reserved.Addr().String()); err == nil {
		t.Fatal("metrics listener unexpectedly bound an occupied port")
	}
	if !runtime.Metrics.Degraded() {
		t.Fatal("metrics listener failure did not mark observability degraded")
	}
	response := httptest.NewRecorder()
	http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusNoContent {
		t.Fatal("metrics listener failure affected the primary handler")
	}
}

func TestObservabilityDegradationTracksEachSignalIndependently(t *testing.T) {
	metrics := observability.NewMetrics()
	metrics.SetComponentDegraded("traces", true)
	metrics.SetComponentDegraded("metrics_listener", true)
	metrics.SetComponentDegraded("metrics_listener", false)
	if !metrics.Degraded() {
		t.Fatal("metrics recovery hid an outstanding trace exporter failure")
	}
	metrics.SetComponentDegraded("traces", false)
	if metrics.Degraded() {
		t.Fatal("observability remained degraded after all signals recovered")
	}
}
