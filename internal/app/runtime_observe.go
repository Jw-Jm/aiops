package app

import (
	"log/slog"

	"ops-platform/internal/observability"
)

// optionalRuntime returns the caller-supplied observability runtime, if any.
// Background listeners accept it as an optional argument so existing callers
// keep compiling while production wiring passes the real runtime.
func optionalRuntime(runtimes []*observability.Runtime) *observability.Runtime {
	if len(runtimes) > 0 {
		return runtimes[0]
	}
	return nil
}

// observeStartupFailure reports a background listener that stopped serving.
// It prefers the redacting runtime logger and the bounded operation counters so
// the failure is observable instead of silently swallowed.
func observeStartupFailure(runtime *observability.Runtime, area, message string, err error) {
	if runtime != nil && runtime.Metrics != nil {
		runtime.Metrics.RecordOperation(area, "error", "unavailable")
	}
	if runtime != nil && runtime.Logger != nil {
		runtime.Logger.Error(message, "error", err)
		return
	}
	slog.Error(message, "error", err)
}
