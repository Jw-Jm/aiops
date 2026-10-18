package app

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"math"
	"net"
	"net/http/httptest"
	"ops-platform/internal/observability"
	"strings"
	"testing"
)

func TestBusinessMetricsCanceledPassCannotPublishStaleOrEmptyQueues(t *testing.T) {
	config, err := pgxpool.ParseConfig("postgres://unavailable@127.0.0.1:1/unavailable")
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		t.Error("canceled sample attempted a network dial")
		return nil, errors.New("no network in fixture")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	m := observability.NewMetrics()
	if !m.SetBusinessSnapshot(map[string]float64{"finding_pending": 2, "finding_deadletter": 0, "incident_pending": 1, "investigation_queued": 0, "investigation_running": 1}, math.NaN(), map[string]map[string]float64{"tool": {}, "tool_admission": {}, "action": {}}) {
		t.Fatal("fixture")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	collectBusinessMetrics(canceled, pool, m)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, line := range []string{"platform_database_up 0", "platform_business_snapshot_available 0", `platform_queue_depth{queue="finding_pending"} NaN`} {
		if !strings.Contains(w.Body.String(), line) {
			t.Fatalf("failed sample retained fabricated/stale measurement: %s", line)
		}
	}
}
