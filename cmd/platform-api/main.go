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
	if _, err := app.NewAPI(app.ConfigFromEnv()); err != nil {
		slog.Error("platform-api bootstrap failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
