package app

import (
	"context"
	"crypto/tls"
	"errors"
	"ops-platform/internal/auth"
	"testing"
)

func TestRunnerIdentityBootstrapRetriesOnlyInitialization(t *testing.T) {
	attempts := 0
	config, _, err := runnerIdentityBootstrap(t.Context(), func(context.Context) (*tls.Config, auth.WorkloadTrust, error) {
		attempts++
		if attempts == 1 {
			return nil, auth.WorkloadTrust{}, errors.New("policy not ready")
		}
		return &tls.Config{}, auth.WorkloadTrust{}, nil
	})
	if err != nil || config == nil || attempts != 2 {
		t.Fatalf("attempts=%d error=%v", attempts, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	attempts = 0
	_, _, err = runnerIdentityBootstrap(ctx, func(context.Context) (*tls.Config, auth.WorkloadTrust, error) {
		attempts++
		return nil, auth.WorkloadTrust{}, errors.New("closed")
	})
	if err == nil || attempts != 0 {
		t.Fatalf("cancel failed: attempts=%d error=%v", attempts, err)
	}
}
