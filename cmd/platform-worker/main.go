package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ops-platform/internal/app"
	"ops-platform/internal/observability"
)

func main() {
	logger := observability.NewLogger(os.Stdout, slog.LevelInfo)
	application, err := app.NewWorker(app.ConfigFromEnv())
	if err != nil {
		logger.Error("platform-worker bootstrap failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtime, err := observability.NewRuntime(ctx, "ops-platform-worker")
	if err != nil {
		logger.Error("platform-worker observability bootstrap failed")
		os.Exit(1)
	}
	go func() {
		if err := runtime.ServeMetrics(ctx, observability.MetricsListenAddress()); err != nil {
			runtime.Metrics.SetComponentDegraded("metrics_listener", true)
			runtime.Logger.ErrorContext(ctx, "platform-worker metrics listener unavailable", "signal", "metrics")
		}
	}()
	runtime.Logger.InfoContext(ctx, "platform-worker started")
	if err := application.Serve(ctx, runtime); err != nil {
		runtime.Logger.ErrorContext(ctx, "platform-worker stopped", "error", err)
		os.Exit(1)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Close(shutdownContext); err != nil {
		runtime.Logger.WarnContext(ctx, "platform-worker trace shutdown failed", "signal", "traces")
	}
}
