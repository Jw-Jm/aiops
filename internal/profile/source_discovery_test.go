package profile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ops-platform/internal/integrations"
)

func TestSourceDiscovery(t *testing.T) {
	t.Run("upstream version formats remain exact", func(t *testing.T) {
		logsVersion, err := integrations.VersionFromRoot([]byte("Version victoria-logs-20260716-022232-tags-v1.52.0-0-g46a54c976f<br>"))
		if err != nil || logsVersion != "v1.52.0" {
			t.Fatalf("VictoriaLogs version = %q, %v", logsVersion, err)
		}
		metricsVersion, err := integrations.VersionFromVictoriaMetricsMetrics([]byte(`vm_log_messages_total{app_version="victoria-metrics-20250425-131441-tags-v1.116.0-0-g27d3fb2105",level="info"} 1`))
		if err != nil || metricsVersion != "v1.116.0" {
			t.Fatalf("VictoriaMetrics version = %q, %v", metricsVersion, err)
		}
		vmalertVersion, err := integrations.VersionFromVictoriaMetricsMetrics([]byte(`vm_app_version{version="vmalert-20250425-131706-tags-v1.116.0-0-g27d3fb2105",short_version="v1.116.0"} 1`))
		if err != nil || vmalertVersion != "v1.116.0" {
			t.Fatalf("vmalert version = %q, %v", vmalertVersion, err)
		}
	})
	t.Run("healthy Victoria sources expose exact versions and capabilities", func(t *testing.T) {
		metricsServer := victoriaProbeServer(t, false, true)
		defer metricsServer.Close()
		logsServer := victoriaProbeServer(t, false, true)
		defer logsServer.Close()
		results, err := DiscoverSources(context.Background(), []SourceCandidate{{
			Component: "victoriaMetrics", Endpoint: metricsServer.URL, LogicalID: "monitoring/vmsingle-vm",
		}, {Component: "victoriaLogs", Endpoint: logsServer.URL, LogicalID: "monitoring/vlogs"}}, metricsServer.Client())
		if err != nil {
			t.Fatal(err)
		}
		for _, component := range []string{"victoriaMetrics", "victoriaLogs"} {
			result, ok := results[component]
			if !ok || result.Version == "" || result.LogicalID == "" {
				t.Fatalf("%s result is incomplete: %#v", component, result)
			}
		}
		if results["victoriaMetrics"].Capabilities["metrics"] != "available" {
			t.Fatalf("metrics capability = %#v", results["victoriaMetrics"].Capabilities)
		}
		if results["victoriaLogs"].Capabilities["logs"] != "available" {
			t.Fatalf("logs capability = %#v", results["victoriaLogs"].Capabilities)
		}
	})

	t.Run("healthy vmalert exposes the rule listing capability", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/health":
				_, _ = w.Write([]byte("OK"))
			case "/":
				_, _ = w.Write([]byte("<html>vmalert UI</html>"))
			case "/metrics":
				_, _ = w.Write([]byte(`vm_app_version{version="vmalert-20250425-131706-tags-v1.116.0-0-g27d3fb2105",short_version="v1.116.0"} 1`))
			case "/api/v1/rules":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"success","data":{"groups":[]}}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		results, err := DiscoverSources(context.Background(), []SourceCandidate{{
			Component: "vmalert", Endpoint: server.URL, LogicalID: "monitoring/vmalert",
		}}, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		if got := results["vmalert"].Capabilities["alertRules"]; got != "available" {
			t.Fatalf("alertRules capability = %q", got)
		}
		if got := results["vmalert"].Version; got != "v1.116.0" {
			t.Fatalf("vmalert version = %q", got)
		}
	})

	t.Run("unauthenticated source is not accepted", func(t *testing.T) {
		server := victoriaProbeServer(t, true, true)
		defer server.Close()
		_, err := DiscoverSources(context.Background(), []SourceCandidate{{Component: "victoriaMetrics", Endpoint: server.URL}}, server.Client())
		if err == nil || !strings.Contains(err.Error(), "SOURCE_UNAUTHORIZED") {
			t.Fatalf("error = %v, want SOURCE_UNAUTHORIZED", err)
		}
	})

	t.Run("unknown version fails closed", func(t *testing.T) {
		server := victoriaProbeServer(t, false, false)
		defer server.Close()
		_, err := DiscoverSources(context.Background(), []SourceCandidate{{Component: "victoriaMetrics", Endpoint: server.URL}}, server.Client())
		if err == nil || !strings.Contains(err.Error(), "SOURCE_VERSION_UNKNOWN") {
			t.Fatalf("error = %v, want SOURCE_VERSION_UNKNOWN", err)
		}
	})

	t.Run("unreachable endpoint is not treated as absent", func(t *testing.T) {
		server := victoriaProbeServer(t, false, true)
		endpoint := server.URL
		server.Close()
		_, err := DiscoverSources(context.Background(), []SourceCandidate{{Component: "victoriaMetrics", Endpoint: endpoint}}, http.DefaultClient)
		if err == nil || !strings.Contains(err.Error(), "SOURCE_UNAVAILABLE") {
			t.Fatalf("error = %v, want SOURCE_UNAVAILABLE", err)
		}
	})

	t.Run("duplicate service endpoints are a conflict", func(t *testing.T) {
		server := victoriaProbeServer(t, false, true)
		defer server.Close()
		_, err := DiscoverSources(context.Background(), []SourceCandidate{
			{Component: "victoriaMetrics", Endpoint: server.URL, LogicalID: "monitoring/a"},
			{Component: "victoriaMetrics", Endpoint: server.URL, LogicalID: "monitoring/b"},
		}, server.Client())
		if err == nil || !strings.Contains(err.Error(), "SOURCE_CONFLICT") {
			t.Fatalf("error = %v, want SOURCE_CONFLICT", err)
		}
	})
}

func victoriaProbeServer(t *testing.T, unauthorized, knownVersion bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unauthorized {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		case "/":
			if knownVersion {
				_, _ = w.Write([]byte("<h2>Single-node VictoriaMetrics</h2>Version v1.116.0<br>"))
			}
		case "/api/v1/query":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		case "/select/logsql/query":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/rules":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"groups":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
}
