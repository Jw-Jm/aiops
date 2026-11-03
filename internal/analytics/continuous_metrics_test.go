package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
)

func TestContinuousMetricsRequiresStoredTimesAndEveryFamily(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	invalid := ""
	denied := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows := []any{}
		for _, metric := range podMetricNames {
			if invalid == "missing" && metric == "container_cpu_usage_seconds_total" {
				continue
			}
			labels := map[string]string{"__name__": metric, "tenant": "tenant", "cluster": "cluster", "collector": evidence.KubernetesMetricsCollector, "namespace": "apps", "uid": "11111111-1111-4111-8111-111111111111"}
			if invalid == "scope" {
				labels["namespace"] = "private"
			}
			// The evaluation time remains now even when the stored sample time goes stale.
			rows = append(rows, map[string]any{"metric": labels, "value": []any{time.Now().Unix(), strconv.FormatFloat(float64(at.UnixMilli())/1000, 'f', 3, 64)}})
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}})
	}))
	defer server.Close()
	b := evidence.Binding{SourceType: "victoriametrics", Tenant: "tenant", Revision: 1, Endpoint: server.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": "tenant", "cluster": "cluster", "collector": evidence.KubernetesMetricsCollector}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
	proof := Qualification{Continuous: true, DatasetID: "metrics.pod-cpu", Operation: "trend", CoverageFrom: at.Add(-time.Hour), SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(b), EvidenceDigest: graph.ScopeDigest("native metrics proof"), CollectorVersion: "test/v1", MaxWindowSeconds: 1800}
	v, err := NewVictoria(b, server.Client(), func(context.Context, evidence.Binding) error {
		if denied {
			return ErrDenied
		}
		return nil
	}, []Qualification{proof})
	if err != nil {
		t.Fatal(err)
	}
	if err = v.RefreshContinuousMetrics(t.Context()); err != nil {
		t.Fatal(err)
	}
	if v.currentQualifications()[0].Digest() != proof.Digest() {
		t.Fatal("watermark changed immutable proof")
	}
	for _, tc := range []string{"stale", "future", "missing", "scope", "revoked"} {
		t.Run(tc, func(t *testing.T) {
			invalid = tc
			at = time.Now().Add(-time.Second)
			denied = tc == "revoked"
			if tc == "stale" {
				at = time.Now().Add(-time.Minute)
			}
			if tc == "future" {
				at = time.Now().Add(time.Minute)
			}
			if v.RefreshContinuousMetrics(t.Context()) == nil || !v.currentQualifications()[0].VerifiedUntil.IsZero() {
				t.Fatal("invalid native series retained qualification")
			}
		})
	}
	invalid = ""
	denied = false
	at = time.Now().Add(-time.Second)
	if v.RefreshContinuousMetrics(t.Context()) != nil {
		t.Fatal("fresh native source failed to recover")
	}
}
