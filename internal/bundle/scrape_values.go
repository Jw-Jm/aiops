package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"ops-platform/internal/profile"
)

// Freeze the monitoring choice in the values shared by local preflight and
// installation. Helm template has no server capabilities; leaving this at
// auto would let the later server-backed install create an unchecked resource.
func platformScrapeValues(ctx context.Context, p profile.ResolvedProfile, run CommandRunner) (map[string]any, error) {
	for _, choice := range []struct{ crd, kind, version, value string }{
		{"vmservicescrapes.operator.victoriametrics.com", "VMServiceScrape", "v1beta1", "vmservicescrape"},
		{"servicemonitors.monitoring.coreos.com", "ServiceMonitor", "v1", "servicemonitor"},
	} {
		if !slices.Contains(p.Kubernetes.CRDs, choice.crd) {
			continue
		}
		raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "customresourcedefinition", choice.crd, "-o", "json")
		if err != nil {
			return nil, errors.New("PROFILE_COMPONENT_DRIFT: resolved monitoring CRD is unavailable")
		}
		var crd struct {
			Spec struct {
				Names    struct{ Kind string } `json:"names"`
				Versions []struct {
					Name   string
					Served bool
				} `json:"versions"`
			} `json:"spec"`
		}
		if len(raw) > 4<<20 || json.Unmarshal(raw, &crd) != nil || crd.Spec.Names.Kind != choice.kind {
			return nil, errors.New("PROFILE_COMPONENT_DRIFT: resolved monitoring CRD identity is invalid")
		}
		for _, version := range crd.Spec.Versions {
			if version.Name == choice.version && version.Served {
				return map[string]any{"scrapeKind": choice.value}, nil
			}
		}
		return nil, errors.New("PROFILE_COMPONENT_DRIFT: required monitoring CRD version is not served")
	}
	return map[string]any{"scrapeKind": "none"}, nil
}
