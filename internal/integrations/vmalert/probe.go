package vmalert

import (
	"context"
	"encoding/json"
	"fmt"

	"ops-platform/internal/integrations"
)

func Probe(ctx context.Context, client integrations.HTTPDoer, endpoint, authRef, logicalID string) (integrations.ProbeResult, error) {
	if err := integrations.Healthy(ctx, client, endpoint, authRef); err != nil {
		return integrations.ProbeResult{}, err
	}
	version, err := integrations.Version(ctx, client, endpoint, authRef)
	if err != nil {
		metrics, metricsErr := integrations.Get(ctx, client, endpoint, "/metrics", authRef)
		if metricsErr != nil {
			return integrations.ProbeResult{}, err
		}
		version, err = integrations.VersionFromVictoriaMetricsMetrics(metrics)
		if err != nil {
			return integrations.ProbeResult{}, err
		}
	}
	body, err := integrations.Get(ctx, client, endpoint, "/api/v1/rules", authRef)
	if err != nil {
		return integrations.ProbeResult{}, fmt.Errorf("SOURCE_CAPABILITY_UNAVAILABLE: read-only vmalert rule listing failed: %w", err)
	}
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		return integrations.ProbeResult{}, fmt.Errorf("SOURCE_CAPABILITY_UNAVAILABLE: vmalert rule listing returned invalid JSON")
	}
	return integrations.ProbeResult{Endpoint: endpoint, Version: version, AuthRef: authRef, LogicalID: logicalID, Capabilities: map[string]string{"alertRules": "available"}}, nil
}
