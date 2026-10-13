package httpapi

import (
	"github.com/google/uuid"
	"net/http"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
)

type assistantRebindRequest struct {
	ExpectedRevision     int64                `json:"expectedConversationRevision"`
	Namespaces           *[]string            `json:"namespaceSelection,omitempty"`
	IncludeClusterScoped *bool                `json:"includeClusterScoped,omitempty"`
	ResourceCanonicalID  string               `json:"resourceCanonicalId,omitempty"`
	TimeRange            *assistant.TimeRange `json:"timeRange,omitempty"`
}

func (h *AssistantHandlers) bindResource(r *http.Request, actor auth.RequestContext, c assistant.Conversation, id, symptom string, window assistant.TimeRange) (*assistant.ResourceContext, error) {
	if h.SP04 == nil {
		return nil, investigation.ErrNoJob
	}
	grant, err := (graph.Authorization{Pool: h.Repository.Pool}).Effective(r.Context(), actor.TenantID.String(), actor.Subject, c.ClusterUID)
	if err != nil {
		return nil, err
	}
	route, err := (graph.Repository{Pool: h.Repository.Pool}).Load(r.Context(), actor.TenantID.String(), c.ClusterUID)
	if err != nil {
		return nil, err
	}
	selection := c.Binding.Selection
	selection.CanonicalIDs = nil
	scope, err := analytics.RestrictScope(grant, selection)
	if err != nil {
		return nil, err
	}
	j := investigation.Job{TenantID: actor.TenantID, Subject: actor.Subject, Scope: scope, Assistant: &investigation.AssistantTask{GrantSnapshotDigest: graph.ScopeDigest(grant)}}
	resolution, err := (assistant.ResourceResolver{Authorization: graph.Authorization{Pool: h.Repository.Pool}, Graph: assistantGraphReader{h.SP04, j}}).Resolve(r.Context(), analytics.Binding{Subject: actor.Subject, Grant: grant, Selection: selection, OwnerEpoch: route.Epoch}, assistant.ResourceSelector{CanonicalID: id})
	if err != nil {
		return nil, err
	}
	rc, err := assistant.NewContext(resolution, symptom, window)
	if err != nil {
		return nil, err
	}
	return &rc, nil
}

func (h *AssistantHandlers) rebind(w http.ResponseWriter, r *http.Request, actor auth.RequestContext, id uuid.UUID) {
	fail := func(err error) { assistantError(w, err, actor.RequestID) }
	if r.Method != "POST" || !auth.HasRole(r.Context(), auth.Operator) {
		fail(investigation.ErrDenied)
		return
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 || !validIdempotencyKey(r.Header.Get("Idempotency-Key")) {
		fail(investigation.ErrInvalid)
		return
	}
	var q assistantRebindRequest
	if decodeInvestigation(r, &q) != nil || (q.Namespaces == nil && q.IncludeClusterScoped == nil && q.ResourceCanonicalID == "" && q.TimeRange == nil) {
		fail(investigation.ErrInvalid)
		return
	}
	c, err := h.Repository.Get(r.Context(), actor.TenantID, id, actor.Subject)
	if err != nil {
		fail(err)
		return
	}
	selection := c.Binding.Selection
	if q.Namespaces != nil {
		selection.Namespaces = *q.Namespaces
	}
	if q.IncludeClusterScoped != nil {
		selection.IncludeClusterScoped = *q.IncludeClusterScoped
	}
	var rc *assistant.ResourceContext
	if q.ResourceCanonicalID != "" {
		if c.Binding.ResourceContext == nil {
			fail(investigation.ErrInvalid)
			return
		}
		window := c.Binding.ResourceContext.TimeRange
		if q.TimeRange != nil {
			window = *q.TimeRange
		}
		selected := c
		selected.Binding.Selection = selection
		rc, err = h.bindResource(r, actor, selected, q.ResourceCanonicalID, c.Binding.ResourceContext.Symptom, window)
		if err != nil {
			fail(err)
			return
		}
	}
	out, err := h.Repository.Rebind(r.Context(), actor.TenantID, id, actor.Subject, r.Header.Get("Idempotency-Key"), q.ExpectedRevision, selection, rc, q.TimeRange, graph.ScopeDigest(q))
	if err != nil {
		fail(err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out, "requestId": actor.RequestID})
}
