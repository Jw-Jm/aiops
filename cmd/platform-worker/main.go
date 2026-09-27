package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ops-platform/internal/app"
)

func main() {
	if _, err := app.NewWorker(app.ConfigFromEnv()); err != nil {
		slog.Error("platform-worker bootstrap failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
