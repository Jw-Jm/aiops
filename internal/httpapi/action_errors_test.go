package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"ops-platform/internal/action"
	"testing"
)

func TestRiskAcknowledgementErrorsStayDistinct(t *testing.T) {
	for _, entry := range []struct {
		err    error
		status int
		code   string
	}{{action.ErrAckRequired, 412, "RISK_ACK_REQUIRED"}, {action.ErrAckExpired, 412, "RISK_ACK_EXPIRED"}, {action.ErrAckScopeMismatch, 409, "RISK_ACK_SCOPE_MISMATCH"}, {action.ErrAckCommandMismatch, 409, "RISK_ACK_COMMAND_MISMATCH"}} {
		w := httptest.NewRecorder()
		actionError(w, entry.err, "request")
		var body map[string]any
		if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != entry.status || body["code"] != entry.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
