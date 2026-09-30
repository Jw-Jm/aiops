package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"ops-platform/internal/observability"
)

// ObservabilityMiddleware adds a bounded request ID, W3C trace propagation,
// structured request logs, and low-cardinality HTTP metrics. It never reads or
// records request bodies, URL paths, query strings, or authorization headers.
func ObservabilityMiddleware(logger *slog.Logger, metrics *observability.Metrics, tracing *observability.Tracing) func(http.Handler) http.Handler {
	if logger == nil {
		logger = observability.NewLogger(nil, slog.LevelInfo)
	}
	if metrics == nil {
		metrics = observability.NewMetrics()
	}
	if tracing == nil {
		tracing, _ = observability.NewTracing(context.Background(), observability.TracingConfig{}, metrics, logger)
	}
	tracer := tracing.Tracer("ops-platform/httpapi")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			started := time.Now()
			requestID := request.Header.Get("X-Request-ID")
			if !observability.ValidRequestID(requestID) {
				requestID = observability.NewRequestID()
			}
			ctx := tracing.Extract(request.Context(), request.Header)
			ctx = observability.WithRequestID(ctx, requestID)
			ctx, span := tracer.Start(ctx, "HTTP "+request.Method, trace.WithSpanKind(trace.SpanKindServer))
			span.SetAttributes(
				attribute.String("http.request.method", boundedHTTPMethod(request.Method)),
				attribute.String("request.id", requestID),
			)
			writer.Header().Set("X-Request-ID", requestID)
			statusWriter := &responseStatusWriter{ResponseWriter: writer}
			var trackedWriter http.ResponseWriter = statusWriter
			if _, ok := writer.(http.Flusher); ok {
				trackedWriter = &flushingResponseStatusWriter{responseStatusWriter: statusWriter}
			}
			defer func() {
				status := statusWriter.status
				if status == 0 {
					status = http.StatusOK
				}
				span.SetAttributes(attribute.Int("http.response.status_code", status))
				if status >= http.StatusInternalServerError {
					span.SetStatus(codes.Error, "HTTP server error")
				}
				span.End()
				duration := time.Since(started)
				metrics.ObserveHTTP(request.Method, status, duration)
				observability.LoggerWithContext(ctx, logger).InfoContext(ctx, "http request complete",
					"method", boundedHTTPMethod(request.Method), "status_code", status, "duration_ms", duration.Milliseconds())
			}()
			request = request.Clone(ctx)
			request.Header.Set("X-Request-ID", requestID)
			next.ServeHTTP(trackedWriter, request)
		})
	}
}

func boundedHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

type responseStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseStatusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

type flushingResponseStatusWriter struct {
	*responseStatusWriter
}

func (w *flushingResponseStatusWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
