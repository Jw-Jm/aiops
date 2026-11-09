package httpapi

import (
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"strings"
	"testing"
)

func TestDisabledLegacyInvestigationRejectsBeforeCreatingDoomedJob(t *testing.T) {
	h := InvestigationHandlers{LegacyDisabled: true}
	for _, authenticated := range []bool{true, false} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+uuid.NewString()+"/investigations", strings.NewReader(`{}`))
		if authenticated {
			r = r.WithContext(auth.WithRequestContext(r.Context(), auth.RequestContext{TenantID: uuid.New(), Subject: "operator", Roles: []auth.Role{auth.Operator}}))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 403
		if authenticated {
			want = 503
		}
		if w.Code != want || authenticated && !strings.Contains(w.Body.String(), "CAPABILITY_DISABLED") {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
