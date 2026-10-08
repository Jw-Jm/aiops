package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"ops-platform/internal/auth"
)

func TestOperatorRoleStatusRejectsScopeSubstitutionBeforeTransaction(t *testing.T) {
	for _, body := range []string{
		`{"expectedRevision":1,"status":"disabled","subject":"other"}`,
		`{"expectedRevision":1,"status":"active","role":"platform_admin"}`,
		`{"expectedRevision":1,"status":"active","tenantId":"other"}`,
		`{"expectedRevision":1,"status":"active","clusterScopes":["foreign"]}`,
		`{"expectedRevision":1,"status":"active","namespaceScopes":[{"namespace":"foreign"}]}`,
		`{"expectedRevision":1,"status":"active"} {"expectedRevision":2,"status":"disabled"}`,
		`{"expectedRevision":0,"status":"disabled"}`,
		`{"expectedRevision":1.5,"status":"disabled"}`,
		`{"expectedRevision":1,"status":"running"}`,
		`{"expectedRevision":1,"status":"disabled"}` + strings.Repeat(" ", 4096),
	} {
		r := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/role-bindings/current/status", strings.NewReader(body))
		route := chi.NewRouteContext()
		route.URLParams.Add("bindingId", "018f0f2b-91c2-7d42-a8dc-f719c5987302")
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, route)
		ctx = auth.WithRequestContext(ctx, auth.RequestContext{TenantID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987301"), Subject: "verified-admin", Roles: []auth.Role{auth.PlatformAdmin}})
		w := httptest.NewRecorder()
		// No transaction or Service is supplied: these requests must be rejected
		// before touching database authority or status mutation.
		h := TenantAdminHandlers{}
		h.setOperatorRoleBindingStatus(w, r.WithContext(ctx))
		if w.Code != 400 {
			t.Fatalf("status request admitted authority substitution before transaction: %d", w.Code)
		}
	}
}
