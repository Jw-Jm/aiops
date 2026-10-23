package httpapi

import (
	"context"
	"github.com/google/uuid"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"ops-platform/internal/identity"
	"strings"
	"testing"
)

func TestNativeUserBoundaryRejectsAuthorityClaimsBeforeDatabase(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: tenant, Subject: "native-fixture", Roles: []auth.Role{auth.PlatformAdmin}})
	ctx = auth.WithNativeSession(ctx, auth.NativeSessionContext{Session: identity.ActiveSession{TenantID: tenant, UserID: user, Mode: "full"}})
	for _, tc := range []struct{ path, method, body string }{
		{"/api/v1/admin/users", "POST", `{"loginName":"new.user","displayName":"New","initialPassword":"fixture-password","role":"platform_admin"}`},
		{"/api/v1/admin/users", "POST", `{"loginName":"new.user","displayName":"New","initialPassword":"fixture-password","tenantId":"` + uuid.NewString() + `"}`},
		{"/api/v1/admin/role-bindings", "POST", `{"subject":"same-name","role":"platform_admin","scope":{},"expectedAuthorizationVersion":1,"reason":"fixture reason"}`},
		{"/api/v1/admin/role-bindings/" + uuid.NewString() + "/status", "PATCH", `{"userId":"` + user.String() + `","expectedRevision":1,"status":"active","reason":"fixture reason"}`},
	} {
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)).WithContext(ctx)
		response := httptest.NewRecorder()
		NativeUserHandlers{}.ServeHTTP(response, request)
		if response.Code != 400 {
			t.Fatalf("%s %s=%d, want strict validation before DB", tc.method, tc.path, response.Code)
		}
	}
}

func TestNativeMappingBoundaryRejectsUnreviewedClaimsAndBadQueries(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: tenant, Subject: "native-fixture", Roles: []auth.Role{auth.PlatformAdmin}})
	ctx = auth.WithNativeSession(ctx, auth.NativeSessionContext{Session: identity.ActiveSession{TenantID: tenant, UserID: user, Mode: "full"}})
	for _, path := range []string{"?userId=not-a-uuid", "?cursor=not-a-uuid", "?limit=101", "?limit=-1"} {
		r := httptest.NewRequest("GET", "/api/v1/admin/subject-mappings"+path, nil).WithContext(ctx)
		w := httptest.NewRecorder()
		NativeUserHandlers{}.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("%s = %d", path, w.Code)
		}
	}
	for _, body := range []string{
		`{"legacySubject":"same-name","userId":"` + user.String() + `","basis":"login_name","evidenceDigest":"sha256:` + strings.Repeat("a", 64) + `","expectedUserRevision":1}`,
		`{"legacySubject":"old","userId":"` + user.String() + `","basis":"verified_inventory","verified":true,"evidenceDigest":"sha256:` + strings.Repeat("a", 64) + `","expectedUserRevision":1}`,
	} {
		r := httptest.NewRequest("POST", "/api/v1/admin/subject-mappings", strings.NewReader(body)).WithContext(ctx)
		w := httptest.NewRecorder()
		NativeUserHandlers{}.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("mapping authority claim = %d", w.Code)
		}
	}
	ctx = auth.WithRequestContext(ctx, auth.RequestContext{TenantID: tenant, Subject: "native-fixture", Roles: []auth.Role{auth.Operator}})
	w := httptest.NewRecorder()
	NativeUserHandlers{}.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/subject-mappings", nil).WithContext(ctx))
	if w.Code != 403 {
		t.Fatalf("operator read mappings = %d", w.Code)
	}
}
