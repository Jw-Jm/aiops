package httpapi

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"testing"
	"time"
)

func TestSessionUsesActiveGrantsWithoutInheritance(t *testing.T) {
	for _, role := range []auth.Role{auth.Operator, auth.PlatformAdmin} {
		req := httptest.NewRequest("GET", "/api/v1/auth/session", nil)
		req = req.WithContext(auth.WithRequestContext(req.Context(), auth.RequestContext{Subject: "subject", TenantID: uuid.New(), Roles: []auth.Role{role}, TokenExpiresAt: time.Now().Add(time.Minute)}))
		w := httptest.NewRecorder()
		getAuthSession(w, req)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status/cache: %d %v", w.Code, w.Header())
		}
		var body struct {
			Data struct {
				Roles   []string `json:"roles"`
				Subject string   `json:"subject"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Data.Roles) != 1 || body.Data.Roles[0] != string(role) || body.Data.Subject != "subject" {
			t.Fatalf("invalid session: %s", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	getAuthSession(w, httptest.NewRequest("GET", "/api/v1/auth/session", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
