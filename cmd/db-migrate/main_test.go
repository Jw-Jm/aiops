package main

import (
	"context"
	"strings"
	"testing"
)

func TestNegativeTargetIsRejectedBeforeConnecting(t *testing.T) {
	err := migrate(context.Background(), "postgres://invalid.invalid/unused", "migrations", -1)
	if err == nil || !strings.Contains(err.Error(), "target must be non-negative") {
		t.Fatalf("negative target attempted database access instead of being rejected: %v", err)
	}
}
