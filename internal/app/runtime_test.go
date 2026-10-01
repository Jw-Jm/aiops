package app

import (
	"context"
	"net"
	"testing"

	"ops-platform/internal/observability"
)

func TestAPIServingRequiresRuntimeDependencies(t *testing.T) {
	application, _ := NewAPI(AppConfig{DatabaseURL: "invalid", OIDCIssuerURL: "https://invalid", ProfilePath: "fixture"})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	runtime, _ := observability.NewRuntime(context.Background(), "test")
	defer runtime.Close(context.Background())
	if err := application.Serve(context.Background(), listener, runtime); err == nil {
		t.Fatal("API listener ran without valid persistence and identity dependencies")
	}
}

func TestWorkerServingRequiresRuntimeDependencies(t *testing.T) {
	application, _ := NewWorker(AppConfig{DatabaseURL: "invalid", OIDCIssuerURL: "https://invalid", ProfilePath: "fixture"})
	runtime, _ := observability.NewRuntime(context.Background(), "test-worker")
	defer runtime.Close(context.Background())
	if err := application.Serve(context.Background(), runtime); err == nil {
		t.Fatal("worker started without audit persistence, archive and Transit")
	}
}
