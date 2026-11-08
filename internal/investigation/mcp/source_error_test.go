package mcp

import (
	"context"
	"errors"
	"fmt"
	"ops-platform/internal/analytics"
	"ops-platform/internal/investigation"
	"testing"
)

func TestInvalidToolArgumentsAreNotSourceOutages(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{investigation.ErrInvalid, "INVALID_ARGUMENT"},
		{fmt.Errorf("intent: %w", analytics.ErrUnsupported), "INVALID_ARGUMENT"},
		{analytics.ErrInvalid, "INVALID_ARGUMENT"},
		{analytics.ErrDenied, "INVESTIGATION_NOT_AUTHORIZED"},
		{context.DeadlineExceeded, "SOURCE_TIMEOUT"},
		{errors.New("upstream failed"), "SOURCE_DEGRADED"},
	} {
		if got := sourceReadErrorCode(tc.err); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.err, got, tc.want)
		}
	}
}
