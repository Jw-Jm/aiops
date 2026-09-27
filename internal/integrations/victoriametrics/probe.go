package victoriametrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"ops-platform/internal/integrations"
)

func Probe(ctx context.Context, client integrations.HTTPDoer, endpoint, authRef, logicalID string) (integrations.ProbeResult, error) {
	if err := integrations.Healthy(ctx, client, endpoint, authRef); err != nil {
		return integrations.ProbeResult{}, err
	}
	version, err := integrations.Version(ctx, client, endpoint, authRef)
	if err != nil {
		metricsBody, metricsErr := integrations.Get(ctx, client, endpoint, "/metrics", authRef)
		if metricsErr != nil {
			return integrations.ProbeResult{}, err
		}
		version, err = integrations.VersionFromVictoriaMetricsMetrics(metricsBody)
		if err != nil {
			return integrations.ProbeResult{}, err
		}
	}
	query := url.Values{"query": []string{"vector(1)"}}
	body, err := integrations.Get(ctx, client, endpoint, "/api/v1/query?"+query.Encode(), authRef)
	if err != nil {
		return integrations.ProbeResult{}, err
	}
	var response struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []any  `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Status != "success" || response.Data.ResultType != "vector" {
		return integrations.ProbeResult{}, fmt.Errorf("SOURCE_CAPABILITY_UNAVAILABLE: read-only Prometheus query did not return a successful vector")
	}
	return integrations.ProbeResult{Endpoint: endpoint, Version: version, AuthRef: authRef, LogicalID: logicalID, Capabilities: map[string]string{"metrics": "available"}}, nil
}
