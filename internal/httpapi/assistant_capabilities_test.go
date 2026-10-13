package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/investigation"
)

func TestAssistantCapabilitiesRespectModelTasksAndIndependentRoles(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	for _, test := range []struct {
		name        string
		role        auth.Role
		enabled     bool
		model, want []string
	}{
		{"operator", auth.Operator, true, []string{"data_query", "resource_troubleshooting", "incident_followup"}, []string{"data_query", "resource_troubleshooting", "incident_followup"}},
		{"admin has no diagnostic inheritance", auth.PlatformAdmin, true, []string{"data_query", "resource_troubleshooting", "incident_followup"}, []string{"data_query"}},
		{"query-only profile", auth.Operator, true, []string{"data_query"}, []string{"data_query"}},
		{"disabled", auth.Operator, false, []string{"data_query"}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Capabilities read frozen configuration only; this pool is never used.
			h := AssistantHandlers{Repository: assistant.Repository{Pool: &pgxpool.Pool{}, Jobs: investigation.Repository{AssistantEnabled: test.enabled}}, CursorKey: key, ModelTaskKinds: test.model}
			r := httptest.NewRequest("GET", "/api/v1/assistant/capabilities", nil).WithContext(auth.WithRequestContext(context.Background(), auth.RequestContext{Subject: "owner", Roles: []auth.Role{test.role}}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var envelope struct {
				Data struct {
					TaskKinds []string `json:"taskKinds"`
				} `json:"data"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || !reflect.DeepEqual(envelope.Data.TaskKinds, test.want) {
				t.Fatalf("unexpected authorized tasks: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
