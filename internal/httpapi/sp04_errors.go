package httpapi

import (
	"github.com/google/uuid"
	"net/http"
)

// SP-04 has a distinct error payload major so the legacy closed ErrorEnvelope
// enum and foundation consumers remain compatible. Authentication middleware
// may still emit v1, explicitly represented in the endpoint's response union.
func writeSP04Error(w http.ResponseWriter, status int, code, message string, retryable bool, requestID string) {
	if requestID == "" {
		requestID = uuid.Must(uuid.NewV7()).String()
	}
	writeJSON(w, status, map[string]any{"schemaVersion": "error-envelope/v2", "code": code, "message": message, "retryable": retryable, "requestId": requestID})
}
