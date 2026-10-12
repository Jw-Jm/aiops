package profile

import (
	"context"
	"encoding/json"
)

// An external Metrics instance must serve the actual aggregated API through
// its declared TLS-verified Service, not merely have a matching image name.
func metricsAPIServiceReady(candidate ComponentCandidate, services []map[string]any) bool {
	for _, api := range services {
		meta, spec := objectMap(api["metadata"]), objectMap(api["spec"])
		service := objectMap(spec["service"])
		uid, _ := meta["uid"].(string)
		ca, _ := spec["caBundle"].(string)
		if meta["name"] != "v1beta1.metrics.k8s.io" || uid == "" ||
			spec["group"] != "metrics.k8s.io" || spec["version"] != "v1beta1" ||
			service["name"] != candidate.Name || service["namespace"] != candidate.Namespace ||
			spec["insecureSkipTLSVerify"] == true || ca == "" {
			continue
		}
		conditions, _ := objectMap(api["status"])["conditions"].([]any)
		for _, raw := range conditions {
			condition := objectMap(raw)
			if condition["type"] == "Available" && condition["status"] == "True" {
				return true
			}
		}
	}
	return false
}

func enrichMetricsCandidates(ctx context.Context, reader kubectlReader, components map[string][]ComponentCandidate) {
	candidates := components["metrics-server"]
	if len(candidates) == 0 {
		return
	}
	apis, err := reader.getJSON(ctx, "apiservices", false)
	for i := range candidates {
		candidate := &candidates[i]
		available := err == nil && metricsAPIServiceReady(*candidate, apis)
		if available {
			for _, path := range []string{"/apis/metrics.k8s.io/v1beta1/nodes", "/apis/metrics.k8s.io/v1beta1/pods"} {
				raw, readErr := reader.raw(ctx, path)
				var list struct {
					Items []json.RawMessage `json:"items"`
				}
				if readErr != nil || json.Unmarshal(raw, &list) != nil || len(list.Items) == 0 {
					available = false
					break
				}
			}
		}
		if available {
			candidate.Evidence = append(candidate.Evidence, "read-only capability resourceMetrics=available")
		} else {
			candidate.Compatible = false
			candidate.Evidence = append(candidate.Evidence, "TLS-verified matching Metrics APIService and positive Node/Pod Metrics are required")
		}
	}
	components["metrics-server"] = candidates
}
