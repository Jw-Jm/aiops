package httpapi

import (
	"net/http"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/investigation"
)

func (h *AssistantHandlers) interpret(w http.ResponseWriter, r *http.Request, actor auth.RequestContext) {
	if r.Method != "POST" || !auth.HasRole(r.Context(), auth.Operator) {
		assistantError(w, investigation.ErrDenied, actor.RequestID)
		return
	}
	var request struct {
		Symptom string `json:"symptom"`
	}
	if decodeInvestigation(r, &request) != nil {
		assistantError(w, investigation.ErrInvalid, actor.RequestID)
		return
	}
	intent, err := assistant.ParseSymptom(request.Symptom)
	if err != nil {
		assistantError(w, err, actor.RequestID)
		return
	}
	writeJSON(w, 200, map[string]any{"data": intent, "requestId": actor.RequestID})
}
