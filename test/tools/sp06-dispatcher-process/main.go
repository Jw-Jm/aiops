// This acceptance entry runs the production dispatcher as an OS process so its
// lease can be recovered after SIGKILL. It never substitutes repository behavior.
package main

import (
	"context"
	"ops-platform/internal/app"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	raw, err := os.ReadFile(os.Getenv("SP06_DATABASE_URL_FILE"))
	if err != nil {
		os.Exit(2)
	}
	pool, err := app.OpenRuntimePool(ctx, string(raw), "worker_runtime_role")
	if err != nil {
		os.Exit(3)
	}
	defer pool.Close()
	closeWorker, err := app.StartSP06Worker(ctx, pool)
	if err != nil {
		os.Exit(4)
	}
	defer closeWorker()
	<-ctx.Done()
}
