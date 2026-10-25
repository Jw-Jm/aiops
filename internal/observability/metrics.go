package observability

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry          *prometheus.Registry
	workDuration      *prometheus.HistogramVec
	queueDepth        *prometheus.GaugeVec
	recentResults     *prometheus.GaugeVec
	jobDuration       prometheus.Gauge
	databaseUp        prometheus.Gauge
	businessAvailable prometheus.Gauge
	businessSampledAt prometheus.Gauge
	httpRequests      *prometheus.CounterVec
	httpDuration      *prometheus.HistogramVec
	operations        *prometheus.CounterVec
	queryCompleteness *prometheus.CounterVec
	exportFailures    *prometheus.CounterVec
	degraded          prometheus.Gauge
	businessDegraded  prometheus.Gauge
	processUp         prometheus.Gauge
	auditDelay        prometheus.Gauge
	degradedMu        sync.Mutex
	degradedState     uint32
	// businessComponents is the registered business health state. Only the
	// components a Worker registered are kept, so the key space stays finite
	// and no runtime label can grow the map.
	businessComponents map[string]bool
}

// degradationBits are the fixed observability export components. Their health
// is the responsibility of the export itself and keeps its own semantics.
var degradationBits = map[string]uint32{"traces": 1, "metrics_listener": 2, "runtime": 4, "audit_signing": 8}

// maxBusinessComponents bounds the registered business health keys. A Worker
// registers the sources it owns; the space is finite by construction. The bound
// is far above any legitimate configuration: it exists to stop a runaway
// registration, not to cap a real deployment.
const maxBusinessComponents = 4096

// businessComponentPattern is the accepted shape of a business health key. A
// key is a stable identifier of a configured source, never a free label.
var businessComponentPattern = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)

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

// RegisterBusinessComponent registers one business health key. It returns false
// when the key is not a stable identifier or the bounded key space is full. A
// key that is already registered keeps its current state: a repeated
// registration must never clear an existing failure.
func (m *Metrics) RegisterBusinessComponent(key string) bool {
	if !businessComponentPattern.MatchString(key) {
		return false
	}
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()
	if m.businessComponents == nil {
		m.businessComponents = map[string]bool{}
	}
	if _, ok := m.businessComponents[key]; ok {
		return true
	}
	if len(m.businessComponents) >= maxBusinessComponents {
		return false
	}
	m.businessComponents[key] = false
	m.publishBusinessDegraded()
	return true
}

// RegisterBusinessComponents registers a whole set of business health keys. The
// identity and the capacity of every key are checked before the first key is
// registered, so a startup either owns the complete set or none of it.
func (m *Metrics) RegisterBusinessComponents(keys []string) error {
	for _, key := range keys {
		if !businessComponentPattern.MatchString(key) {
			return errors.New("business health key " + key + " is not a stable source identity")
		}
	}
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()
	if m.businessComponents == nil {
		m.businessComponents = map[string]bool{}
	}
	added := 0
	for _, key := range keys {
		if _, ok := m.businessComponents[key]; ok {
			continue
		}
		added++
	}
	if len(m.businessComponents)+added > maxBusinessComponents {
		return errors.New("the business health key space is exhausted")
	}
	for _, key := range keys {
		if _, ok := m.businessComponents[key]; !ok {
			m.businessComponents[key] = false
		}
	}
	m.publishBusinessDegraded()
	return nil
}

// UnregisterBusinessComponents removes a set of registered keys together with
// their state. A removed source never keeps a failure alive.
func (m *Metrics) UnregisterBusinessComponents(keys []string) {
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()
	changed := false
	for _, key := range keys {
		if _, ok := m.businessComponents[key]; !ok {
			continue
		}
		delete(m.businessComponents, key)
		changed = true
	}
	if changed {
		m.publishBusinessDegraded()
	}
}

// UnregisterBusinessComponent removes a registered key together with its state.
// A removed source never keeps a failure alive.
func (m *Metrics) UnregisterBusinessComponent(key string) {
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()
	if _, ok := m.businessComponents[key]; !ok {
		return
	}
	delete(m.businessComponents, key)
	m.publishBusinessDegraded()
}

// SetBusinessDegraded records the health of one registered business component.
// An unregistered key is refused: a caller may not invent components at runtime.
func (m *Metrics) SetBusinessDegraded(key string, degraded bool) {
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()
	if _, ok := m.businessComponents[key]; !ok {
		return
	}
	m.businessComponents[key] = degraded
	m.publishBusinessDegraded()
}

// BusinessComponentRegistered reports whether a business health key is
// registered. It never registers anything, so a caller can check the ownership
// of a source without taking it.
func (m *Metrics) BusinessComponentRegistered(key string) bool {
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()
	_, ok := m.businessComponents[key]
	return ok
}

// BusinessDegraded reports whether any registered business component is
// degraded. One source recovering never clears the failure of another source.
func (m *Metrics) BusinessDegraded() bool {
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()
	for _, degraded := range m.businessComponents {
		if degraded {
			return true
		}
	}
	return false
}

// publishBusinessDegraded publishes the aggregated business health. The caller
// holds the lock.
func (m *Metrics) publishBusinessDegraded() {
	if m.businessDegraded == nil {
		return
	}
	for _, degraded := range m.businessComponents {
		if degraded {
			m.businessDegraded.Set(1)
			return
		}
	}
	m.businessDegraded.Set(0)
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry:          registry,
		workDuration:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "platform", Name: "work_duration_seconds", Help: "Elapsed work time in the named boundary; incident aggregation excludes queue wait and outer transaction commit.", Buckets: prometheus.DefBuckets}, []string{"area"}),
		queueDepth:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "platform", Name: "queue_depth", Help: "Current database queue counts across tenants; replicated workers observe the same population, use max rather than sum."}, []string{"queue"}),
		recentResults:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "platform", Name: "recent_results", Help: "Current counts of tool Steps/admissions started and command executions updated within five minutes, by fixed area/state; replicated observations must not be summed."}, []string{"area", "state"}),
		jobDuration:       prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "platform", Name: "investigation_terminal_mean_seconds", Help: "Mean updated_at minus created_at for jobs currently terminal and updated within five minutes; NaN if no observations."}),
		databaseUp:        prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "platform", Name: "database_up", Help: "Whether the worker's last bounded database ping succeeded."}),
		businessAvailable: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "platform", Name: "business_snapshot_available", Help: "Whether all business queue measurements completed in the last bounded pass."}),
		businessSampledAt: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "platform", Name: "business_snapshot_timestamp_seconds", Help: "Unix time of the last complete business snapshot."}),
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
		businessDegraded: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "platform", Name: "business_degraded",
			Help: "Whether any registered business collection component is degraded.",
		}),
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
	m.InvalidateBusinessSnapshot()
	m.databaseUp.Set(math.NaN())
	registry.MustRegister(m.workDuration, m.queueDepth, m.recentResults, m.jobDuration, m.databaseUp, m.businessAvailable, m.businessSampledAt, m.httpRequests, m.httpDuration, m.operations, m.exportFailures, m.degraded, m.businessDegraded, m.processUp, m.auditDelay, m.queryCompleteness)
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
