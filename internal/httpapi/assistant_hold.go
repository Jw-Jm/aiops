package httpapi

import (
	"github.com/google/uuid"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/investigation"
)

func (h *AssistantHandlers) legalHold(w http.ResponseWriter, r *http.Request, actor auth.RequestContext) {
	if r.Method != "POST" || !auth.HasRole(r.Context(), auth.PlatformAdmin) {
		assistantError(w, investigation.ErrDenied, actor.RequestID)
		return
	}
	var q struct {
		TurnID uuid.UUID `json:"turnId"`
		Held   *bool     `json:"held"`
		Reason string    `json:"reason"`
	}
	if decodeInvestigation(r, &q) != nil || q.Held == nil {
		assistantError(w, investigation.ErrInvalid, actor.RequestID)
		return
	}
	if err := h.Repository.SetLegalHold(r.Context(), actor.TenantID, q.TurnID, actor.Subject, *q.Held, q.Reason); err != nil {
		assistantError(w, err, actor.RequestID)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]bool{"held": *q.Held}, "requestId": actor.RequestID})
}
