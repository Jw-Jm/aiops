package httpapi

import (
	"net/http"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
)

func (h *AssistantHandlers) resolveResource(w http.ResponseWriter, r *http.Request, actor auth.RequestContext) {
	fail := func(err error) { assistantError(w, err, actor.RequestID) }
	if r.Method != "POST" || !auth.HasRole(r.Context(), auth.Operator) || h.SP04 == nil {
		fail(investigation.ErrDenied)
		return
	}
	var request struct {
		ClusterUID           string                     `json:"clusterUid"`
		NamespaceSelection   []string                   `json:"namespaceSelection"`
		IncludeClusterScoped bool                       `json:"includeClusterScoped"`
		Selector             assistant.ResourceSelector `json:"selector"`
	}
	if decodeInvestigation(r, &request) != nil || request.ClusterUID == "" {
		fail(investigation.ErrInvalid)
		return
	}
	grant, err := (graph.Authorization{Pool: h.Repository.Pool}).Effective(r.Context(), actor.TenantID.String(), actor.Subject, request.ClusterUID)
	if err != nil {
		fail(err)
		return
	}
	selection := analytics.Selection{Namespaces: request.NamespaceSelection, IncludeClusterScoped: request.IncludeClusterScoped}
	scope, err := analytics.RestrictScope(grant, selection)
	if err != nil {
		fail(err)
		return
	}
	route, err := (graph.Repository{Pool: h.Repository.Pool}).Load(r.Context(), actor.TenantID.String(), request.ClusterUID)
	if err != nil {
		fail(err)
		return
	}
	// This read has no persistent Job/Turn or Incident. The reader carries only
	// the trusted current grant and narrowed execution scope into signed transport.
	binding := investigation.Job{TenantID: actor.TenantID, Subject: actor.Subject, Scope: scope, Assistant: &investigation.AssistantTask{GrantSnapshotDigest: graph.ScopeDigest(grant)}}
	result, err := (assistant.ResourceResolver{Authorization: graph.Authorization{Pool: h.Repository.Pool}, Graph: assistantGraphReader{h.SP04, binding}}).Resolve(r.Context(), analytics.Binding{Subject: actor.Subject, Grant: grant, Selection: selection, OwnerEpoch: route.Epoch}, request.Selector)
	if err != nil {
		fail(err)
		return
	}
	writeJSON(w, 200, map[string]any{"requestId": actor.RequestID, "data": result})
}
