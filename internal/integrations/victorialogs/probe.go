package victorialogs

import (
	"context"
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
		return integrations.ProbeResult{}, err
	}
	query := url.Values{"query": []string{"*"}, "limit": []string{"1"}}
	if _, err := integrations.Get(ctx, client, endpoint, "/select/logsql/query?"+query.Encode(), authRef); err != nil {
		return integrations.ProbeResult{}, fmt.Errorf("SOURCE_CAPABILITY_UNAVAILABLE: bounded read-only LogsQL query failed: %w", err)
	}
	return integrations.ProbeResult{Endpoint: endpoint, Version: version, AuthRef: authRef, LogicalID: logicalID, Capabilities: map[string]string{"logs": "available"}}, nil
}
