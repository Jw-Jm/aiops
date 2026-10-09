package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"ops-platform/internal/action"
	"ops-platform/internal/app"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if run() != nil {
		os.Exit(1)
	}
}
func run() error {
	execution := flag.String("execution-id", "", "")
	tenant := flag.String("tenant-id", "", "")
	token := flag.String("claim-token", "", "")
	endpoint := flag.String("endpoint", "", "")
	flag.Parse()
	id, err := uuid.Parse(*execution)
	if err != nil {
		return action.ErrInvalid
	}
	tid, err := uuid.Parse(*tenant)
	if err != nil {
		return action.ErrInvalid
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	client, err := app.NewCommandRunnerClient(ctx, *endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, "RUNNER_IDENTITY_UNAVAILABLE")
		return err
	}
	// No raw errors or response bodies are printed: they may carry sensitive data.
	err = (action.RunnerClient{Client: client, Endpoint: *endpoint, Token: *token, TenantID: tid, ExecutionID: id}).Run(ctx)
	if err != nil {
		var callback *action.RunnerCallbackError
		if errors.As(err, &callback) {
			fmt.Fprintf(os.Stderr, "RUNNER_CALLBACK_UNAVAILABLE status=%d\n", callback.Status)
		} else {
			fmt.Fprintln(os.Stderr, "RUNNER_LIFECYCLE_UNAVAILABLE")
		}
	}
	return err
}
