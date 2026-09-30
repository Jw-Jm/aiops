package observability

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type Runtime struct {
	Logger  *slog.Logger
	Metrics *Metrics
	Tracing *Tracing
}

func NewRuntime(ctx context.Context, serviceName string) (*Runtime, error) {
	metrics := NewMetrics()
	logger := NewLogger(os.Stdout, slog.LevelInfo)
	tracing, err := NewTracing(ctx, TracingConfigFromEnv(serviceName), metrics, logger)
	if err != nil {
		metrics.SetComponentDegraded("traces", true)
		logger.WarnContext(ctx, "trace exporter disabled", "signal", "traces")
		tracing, _ = NewTracing(ctx, TracingConfig{ServiceName: serviceName}, metrics, logger)
	}
	return &Runtime{Logger: logger, Metrics: metrics, Tracing: tracing}, nil
}

func MetricsListenAddress() string {
	if value := strings.TrimSpace(os.Getenv("PLATFORM_METRICS_ADDR")); value != "" {
		return value
	}
	return ":9090"
}

// ServeMetrics serves a private Prometheus registry. Listener failure is
// returned to the caller so the process can report degraded observability and
// continue its primary work.
func (r *Runtime) ServeMetrics(ctx context.Context, address string) error {
	if address == "" {
		return errors.New("metrics listen address is required")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		r.Metrics.SetComponentDegraded("metrics_listener", true)
		return err
	}
	r.Metrics.SetComponentDegraded("metrics_listener", false)
	server := &http.Server{
		Handler:           r.Metrics.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			r.Metrics.SetComponentDegraded("metrics_listener", true)
			_ = server.Close()
			return err
		}
		err := <-serveErrors
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		r.Metrics.SetComponentDegraded("metrics_listener", true)
		return err
	}
}

func (r *Runtime) Close(ctx context.Context) error {
	if r.Tracing == nil {
		return nil
	}
	return r.Tracing.Close(ctx)
}
