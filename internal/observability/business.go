package observability

import (
	"context"
	"math"
	"time"
)

type metricsContextKey struct{}

func WithMetrics(ctx context.Context, m *Metrics) context.Context {
	return context.WithValue(ctx, metricsContextKey{}, m)
}
func MetricsFromContext(ctx context.Context) *Metrics {
	m, _ := ctx.Value(metricsContextKey{}).(*Metrics)
	return m
}

var businessQueues = []string{"finding_pending", "finding_deadletter", "incident_pending", "investigation_queued", "investigation_running"}

var businessStates = map[string][]string{"tool_admission": {"reserved", "settled", "unknown"}, "tool": {"planned", "running", "succeeded", "failed", "skipped", "cancelled"}, "action": {"queued", "prepared", "dispatching", "running", "succeeded", "failed", "cancelled", "execution_unknown"}}

func (m *Metrics) ObserveDuration(area string, d time.Duration) {
	if m == nil || d < 0 {
		return
	}
	switch area {
	case "evidence_query", "evidence_read", "incident_aggregation":
		m.workDuration.WithLabelValues(area).Observe(d.Seconds())
	}
}
func (m *Metrics) SetDatabaseAvailable(up bool) {
	if up {
		m.databaseUp.Set(1)
	} else {
		m.databaseUp.Set(0)
	}
}
func (m *Metrics) InvalidateBusinessSnapshot() {
	m.businessAvailable.Set(0)
	m.jobDuration.Set(math.NaN())
	for area, states := range businessStates {
		for _, state := range states {
			m.recentResults.WithLabelValues(area, state).Set(math.NaN())
		}
	}
	for _, q := range businessQueues {
		m.queueDepth.WithLabelValues(q).Set(math.NaN())
	}
}

// Publish only a complete bounded DB snapshot. Missing/failed measurements are
// NaN, never an empty queue or a zero-duration successful job.
func (m *Metrics) SetBusinessSnapshot(queues map[string]float64, meanJobSeconds float64, results map[string]map[string]float64) bool {
	if len(queues) != len(businessQueues) || math.IsInf(meanJobSeconds, 0) || meanJobSeconds < 0 {
		m.InvalidateBusinessSnapshot()
		return false
	}
	for _, q := range businessQueues {
		v, ok := queues[q]
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			m.InvalidateBusinessSnapshot()
			return false
		}
	}
	if len(results) != len(businessStates) {
		m.InvalidateBusinessSnapshot()
		return false
	}
	for area, states := range results {
		allowed, ok := businessStates[area]
		if !ok {
			m.InvalidateBusinessSnapshot()
			return false
		}
		for state, value := range states {
			found := false
			for _, a := range allowed {
				if a == state {
					found = true
				}
			}
			if !found || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				m.InvalidateBusinessSnapshot()
				return false
			}
		}
	}
	for area, states := range businessStates {
		for _, state := range states {
			m.recentResults.WithLabelValues(area, state).Set(results[area][state])
		}
	}
	for _, q := range businessQueues {
		m.queueDepth.WithLabelValues(q).Set(queues[q])
	}
	m.jobDuration.Set(meanJobSeconds)
	m.businessSampledAt.SetToCurrentTime()
	m.businessAvailable.Set(1)
	return true
}
