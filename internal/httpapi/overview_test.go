package httpapi

import (
	"context"
	"github.com/google/uuid"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"testing"
)

func TestOverviewRequiresIdentityAndConfiguredPersistence(t *testing.T) {
	for _, verified := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/v1/overview?clusterUid=cluster", nil)
		want := 401
		if verified {
			r = r.WithContext(auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: uuid.New(), Subject: "operator", Roles: []auth.Role{auth.Operator}}))
			want = 503
		}
		w := httptest.NewRecorder()
		(&SP04Handlers{}).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("verified=%t status=%d", verified, w.Code)
		}
	}
}
