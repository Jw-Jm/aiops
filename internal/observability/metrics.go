package observability

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry          *prometheus.Registry
	httpRequests      *prometheus.CounterVec
	httpDuration      *prometheus.HistogramVec
	operations        *prometheus.CounterVec
	queryCompleteness *prometheus.CounterVec
	exportFailures    *prometheus.CounterVec
	degraded          prometheus.Gauge
	processUp         prometheus.Gauge
	auditDelay        prometheus.Gauge
	degradedMu        sync.Mutex
	degradedState     uint32
}

var degradationBits = map[string]uint32{"traces": 1, "metrics_listener": 2, "runtime": 4, "audit_signing": 8}

var operationSet = map[string]map[string]struct{}{
	"kubernetes": {"query": {}, "error": {}},
	"evidence":   {"query": {}, "read": {}, "error": {}},
	"archive":    {"write": {}, "read": {}, "cleanup": {}, "error": {}},
	"resource":   {"resolve": {}, "error": {}},
	"api":        {"request": {}, "error": {}},
	"graph":      {"query": {}, "error": {}},
	"finding":    {"create": {}, "update": {}, "list": {}, "error": {}},
	"incident":   {"create": {}, "update": {}, "transition": {}, "error": {}},
	"outbox":     {"publish": {}, "retry": {}, "backlog": {}, "error": {}},
	"adapter":    {"call": {}, "retry": {}, "error": {}},
	"agent":      {"job": {}, "model_call": {}, "error": {}},
	"action":     {"plan": {}, "execution": {}, "error": {}},
}

var operationResults = map[string]struct{}{"ok": {}, "error": {}, "retry": {}, "rejected": {}, "unavailable": {}}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "platform", Name: "http_requests_total", Help: "HTTP requests handled by the platform process.",
		}, []string{"method", "status_class"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "platform", Name: "http_request_duration_seconds", Help: "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method"}),
		queryCompleteness: prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "platform", Name: "query_completeness_total", Help: "Semantic completeness and freshness of actual Graph/Evidence responses, independent of HTTP success."}, []string{"area", "completeness", "freshness"}),
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "platform", Name: "operations_total", Help: "Bounded platform control-plane operation counts.",
		}, []string{"area", "operation", "result"}),
		exportFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "platform", Name: "observability_export_failures_total", Help: "Observability exporter failures by signal.",
		}, []string{"signal"}),
		degraded: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "platform", Name: "observability_degraded", Help: "Whether a configured platform observability export is degraded.",
		}),
		processUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "platform", Name: "process_up", Help: "Whether the platform process metrics runtime is initialized.",
		}),
		auditDelay: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "platform", Name: "audit_signing_delay_seconds", Help: "Age of the oldest audit record not yet covered by a signed segment; alert above 600 seconds.",
		}),
	}
	m.processUp.Set(1)
	registry.MustRegister(m.httpRequests, m.httpDuration, m.operations, m.exportFailures, m.degraded, m.processUp, m.auditDelay, m.queryCompleteness)
	return m
}

func (m *Metrics) SetAuditSigningDelay(seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	m.auditDelay.Set(seconds)
}

// Handler exposes only the platform's private registry, not the process-global
// default registry that may contain collectors from unrelated dependencies.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError})
}

func (m *Metrics) RecordOperation(area, operation, result string) bool {
	allowedOperations, ok := operationSet[area]
	if !ok {
		return false
	}
	if _, ok := allowedOperations[operation]; !ok {
		return false
	}
	if _, ok := operationResults[result]; !ok {
		return false
	}
	m.operations.WithLabelValues(area, operation, result).Inc()
	return true
}

func (m *Metrics) ObserveHTTP(method string, status int, duration time.Duration) {
	method = boundedMethod(method)
	class := strconv.Itoa(status/100) + "xx"
	if status < 100 || status > 599 {
		class = "other"
	}
	m.httpRequests.WithLabelValues(method, class).Inc()
	m.httpDuration.WithLabelValues(method).Observe(duration.Seconds())
}

func (m *Metrics) ObserveExport(signal string, err error) {
	if signal != "traces" {
		return
	}
	if err != nil {
		m.exportFailures.WithLabelValues(signal).Inc()
		m.SetComponentDegraded("traces", true)
		return
	}
	m.SetComponentDegraded("traces", false)
}

func (m *Metrics) SetDegraded(degraded bool) {
	m.SetComponentDegraded("runtime", degraded)
}

func (m *Metrics) SetComponentDegraded(component string, degraded bool) {
	bit, ok := degradationBits[component]
	if !ok {
		return
	}
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()
	if degraded {
		m.degradedState |= bit
	} else {
		m.degradedState &^= bit
	}
	if m.degradedState == 0 {
		m.degraded.Set(0)
	} else {
		m.degraded.Set(1)
	}
}

func (m *Metrics) Degraded() bool {
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()
	return m.degradedState != 0
}

func boundedMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

func (m *Metrics) ObserveQuery(area string, partial bool, freshness string) bool {
	if m == nil || (area != "graph" && area != "evidence") {
		return false
	}
	if freshness != "fresh" && freshness != "stale" && freshness != "unavailable" {
		return false
	}
	completeness := "complete"
	if partial || freshness == "unavailable" {
		completeness = "partial"
	}
	m.queryCompleteness.WithLabelValues(area, completeness, freshness).Inc()
	m.RecordOperation(area, "query", "ok")
	return true
}
