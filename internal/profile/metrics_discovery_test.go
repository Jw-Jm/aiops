package profile

import (
	"strings"
	"testing"
)

func TestMetricsDiscoveryRequiresMatchingTrustedAvailableAPIService(t *testing.T) {
	candidate := ComponentCandidate{Name: "ops-metrics-server-metrics", Namespace: "metrics"}
	makeAPI := func() map[string]any {
		return map[string]any{
			"metadata": map[string]any{"name": "v1beta1.metrics.k8s.io", "uid": "live-api-uid"},
			"spec":     map[string]any{"group": "metrics.k8s.io", "version": "v1beta1", "caBundle": "actual-ca", "service": map[string]any{"name": candidate.Name, "namespace": candidate.Namespace}},
			"status":   map[string]any{"conditions": []any{map[string]any{"type": "Available", "status": "True"}}},
		}
	}
	if !metricsAPIServiceReady(candidate, []map[string]any{makeAPI()}) {
		t.Fatal("matching available TLS Metrics rejected")
	}
	for _, change := range []func(map[string]any){
		func(a map[string]any) { objectMap(a["metadata"])["uid"] = nil },
		func(a map[string]any) { objectMap(a["spec"])["caBundle"] = "" },
		func(a map[string]any) { objectMap(a["spec"])["insecureSkipTLSVerify"] = true },
		func(a map[string]any) { objectMap(objectMap(a["spec"])["service"])["namespace"] = "foreign" },
		func(a map[string]any) { a["status"] = map[string]any{} },
	} {
		api := makeAPI()
		change(api)
		if metricsAPIServiceReady(candidate, []map[string]any{api}) {
			t.Fatal("untrusted, unavailable or foreign Metrics accepted")
		}
	}
}

func TestMetricsDigestCompletionCannotOverrideFailedCapability(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	image := "registry.k8s.io/metrics-server/metrics-server@" + digest
	locks := map[string]ComponentLock{"metrics-server": {Version: "v0.8.0", Digest: digest, Image: image}}
	components := map[string][]ComponentCandidate{"metrics-server": {{Image: image, Digest: digest, Endpoint: "https://metrics.metrics.svc:443", Evidence: []string{"image version or runtime digest is unavailable"}}}}
	completeDigestOnlyCandidateVersions(components, locks)
	if components["metrics-server"][0].Compatible {
		t.Fatal("image identity promoted without live Metrics capability")
	}
	candidate := components["metrics-server"][0]
	candidate.Evidence = append(candidate.Evidence, "image version or runtime digest is unavailable", "read-only capability resourceMetrics=available")
	components["metrics-server"][0] = candidate
	completeDigestOnlyCandidateVersions(components, locks)
	if !components["metrics-server"][0].Compatible || components["metrics-server"][0].Version != "v0.8.0" {
		t.Fatal("qualified exact digest plus live capability not resolved")
	}
}
