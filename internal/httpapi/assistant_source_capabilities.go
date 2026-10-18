package httpapi

import (
	"context"
	"encoding/json"
	"ops-platform/internal/analytics"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"time"
)

// capabilityDetailDeadline is asserted below the owning tool deadline by
// TestCapabilityDetailDeadlineFitsContextTool.
const capabilityDetailDeadline = 2 * time.Second

type AssistantCapabilityDetails struct {
	Capabilities []analytics.SourceCapability `json:"sourceCapabilities"`
	Network      []evidence.CapabilitySet     `json:"networkCapabilities"`
	ScopeDigest  string                       `json:"scopeDigest"`
}

func (h *SP04Handlers) assistantSourceCapabilities(ctx context.Context, j investigation.Job) ([]analytics.SourceCapability, error) {
	out, err := h.AssistantCapabilityDetails(ctx, j)
	if out == nil {
		return nil, err
	}
	return out.Capabilities, err
}
func (h *SP04Handlers) AssistantCapabilityDetails(ctx context.Context, j investigation.Job) (*AssistantCapabilityDetails, error) {
	if h == nil || h.Client == nil || h.EndpointAllowed == nil || j.Assistant == nil {
		return nil, investigation.ErrNoJob
	}
	ctx, cancel := context.WithTimeout(ctx, capabilityDetailDeadline)
	defer cancel()
	route, err := (graph.Repository{Pool: h.Pool}).Load(ctx, j.TenantID.String(), j.Scope.Cluster)
	if err != nil || !route.ExpiresAt.After(time.Now()) || !h.EndpointAllowed(route.Endpoint) {
		return nil, graph.ErrNotReady
	}
	// The worker call is auxiliary to the committed context: it must stay
	// strictly below tools.DeadlineFor("get_assistant_context") so a slow or
	// fenced worker degrades the capability status instead of consuming the
	// whole tool deadline and failing the follow-up turn.
	reply, err := h.callWithGrantDigest(ctx, route.Endpoint, "/internal/v1/analysis:query", "platform-analysis-query", j.Subject, j.Scope, rawJSON(map[string]any{"capabilities": true, "ownerEpoch": route.Epoch}), j.Assistant.GrantSnapshotDigest)
	if err != nil {
		return nil, err
	}
	var out AssistantCapabilityDetails
	if json.Unmarshal(reply, &out) != nil || out.ScopeDigest != graph.ScopeDigest(j.Scope) || len(out.Capabilities) > 100 {
		return nil, investigation.ErrInvalid
	}
	if out.Network == nil {
		out.Network = []evidence.CapabilitySet{}
	}
	final, err := (graph.Repository{Pool: h.Pool}).Load(ctx, j.TenantID.String(), j.Scope.Cluster)
	if err != nil || final.Epoch != route.Epoch || final.Instance != route.Instance || final.LeaseUID != route.LeaseUID || !final.ExpiresAt.After(time.Now()) {
		return nil, graph.ErrNotReady
	}
	return &out, nil
}
