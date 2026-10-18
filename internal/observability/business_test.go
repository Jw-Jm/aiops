package observability

import (
	"context"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBusinessSnapshotUnknownInvalidationAndBoundedLabels(t *testing.T) {
	m := NewMetrics()
	read := func() string {
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
		return w.Body.String()
	}
	if !strings.Contains(read(), "platform_database_up NaN") {
		t.Fatal("unmeasured database advertised")
	}
	queues := map[string]float64{"finding_pending": 3, "finding_deadletter": 1, "incident_pending": 2, "investigation_queued": 4, "investigation_running": 5}
	results := map[string]map[string]float64{"tool": {"succeeded": 7}, "tool_admission": {"settled": 8}, "action": {"execution_unknown": 2}}
	if !m.SetBusinessSnapshot(queues, 12, results) {
		t.Fatal("valid snapshot rejected")
	}
	ctx := WithMetrics(context.Background(), m)
	MetricsFromContext(ctx).ObserveDuration("evidence_query", time.Second)
	MetricsFromContext(ctx).ObserveDuration("untrusted-tenant-label", time.Second)
	for _, line := range []string{`platform_queue_depth{queue="finding_pending"} 3`, `platform_recent_results{area="tool",state="succeeded"} 7`, `platform_recent_results{area="tool_admission",state="settled"} 8`, `platform_investigation_terminal_mean_seconds 12`, `platform_work_duration_seconds_count{area="evidence_query"} 1`, `platform_business_snapshot_available 1`} {
		if !strings.Contains(read(), line) {
			t.Fatal("missing actual measurement", line)
		}
	}
	results["tool"]["untrusted-state"] = 1
	if m.SetBusinessSnapshot(queues, 12, results) {
		t.Fatal("unbounded state admitted")
	}
	body := read()
	if strings.Contains(body, "untrusted") || !strings.Contains(body, `platform_queue_depth{queue="finding_pending"} NaN`) || !strings.Contains(body, "platform_business_snapshot_available 0") {
		t.Fatal("failed measurement advertised as empty/current", body)
	}
	delete(results["tool"], "untrusted-state")
	if !m.SetBusinessSnapshot(queues, math.NaN(), results) {
		t.Fatal("no completed jobs must retain valid queue snapshot")
	}
	MetricsFromContext(context.Background()).ObserveDuration("evidence_query", time.Second)
}
