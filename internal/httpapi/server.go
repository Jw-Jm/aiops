package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	generated "ops-platform/gen/api"
	"ops-platform/internal/observability"
)

// ErrorEnvelope is the generated public API error response contract.
type ErrorEnvelope = generated.ErrorEnvelope

// NewHandler adapts the generated server interface to the platform's Chi router.
func NewHandler(server generated.ServerInterface) http.Handler {
	metrics := observability.NewMetrics()
	logger := observability.NewLogger(nil, slog.LevelInfo)
	tracing, _ := observability.NewTracing(context.Background(), observability.TracingConfig{ServiceName: "ops-platform-api"}, metrics, logger)
	return NewHandlerWithObservability(server, logger, metrics, tracing)
}

// NewHandlerWithObservability keeps the API routes and scrape endpoint on one
// listener while sharing the same meters and tracer with middleware.
func NewHandlerWithObservability(server generated.ServerInterface, logger *slog.Logger, metrics *observability.Metrics, tracing *observability.Tracing) http.Handler {
	if metrics == nil {
		metrics = observability.NewMetrics()
	}
	router := chi.NewRouter()
	router.Use(ObservabilityMiddleware(logger, metrics, tracing))
	router.Get("/metrics", metrics.Handler().ServeHTTP)
	return generated.HandlerFromMux(server, router)
}
