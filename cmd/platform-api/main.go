package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ops-platform/internal/app"
	"ops-platform/internal/observability"
)

func main() {
	logger := observability.NewLogger(os.Stdout, slog.LevelInfo)
	application, err := app.NewAPI(app.ConfigFromEnv())
	if err != nil {
		logger.Error("platform-api bootstrap failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtime, err := observability.NewRuntime(ctx, "ops-platform-api")
	if err != nil {
		logger.Error("platform-api observability bootstrap failed")
		os.Exit(1)
	}
	go func() {
		if err := runtime.ServeMetrics(ctx, observability.MetricsListenAddress()); err != nil {
			runtime.Metrics.SetComponentDegraded("metrics_listener", true)
			runtime.Logger.ErrorContext(ctx, "platform-api metrics listener unavailable", "signal", "metrics")
		}
	}()
	address := os.Getenv("PLATFORM_API_ADDR")
	if address == "" {
		address = ":8080"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		runtime.Logger.Error("platform-api listener failed")
		os.Exit(1)
	}
	defer listener.Close()
	runtime.Logger.InfoContext(ctx, "platform-api starting")
	if err := application.Serve(ctx, listener, runtime); err != nil {
		runtime.Logger.ErrorContext(ctx, "platform-api stopped", "error", err)
		os.Exit(1)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Close(shutdownContext); err != nil {
		runtime.Logger.WarnContext(ctx, "platform-api trace shutdown failed", "signal", "traces")
	}
}
