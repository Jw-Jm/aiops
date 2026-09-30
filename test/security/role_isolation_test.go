package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
)

func TestOperatorAndPlatformAdminAreIndependent(t *testing.T) {
	operatorContext := auth.WithRequestContext(t.Context(), auth.RequestContext{
		Subject: "operator-a", TenantID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330"),
		Roles: []auth.Role{auth.Operator},
	})
	adminContext := auth.WithRequestContext(t.Context(), auth.RequestContext{
		Subject: "admin-a", TenantID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987331"),
		Roles: []auth.Role{auth.PlatformAdmin},
	})

	for _, test := range []struct {
		name     string
		ctx      context.Context
		path     string
		required auth.Role
		want     int
	}{
		{name: "operator cannot administer", ctx: operatorContext, path: "/api/v1/admin/role-bindings", required: auth.PlatformAdmin, want: http.StatusForbidden},
		{name: "administrator cannot operate", ctx: adminContext, path: "/api/v1/actions", required: auth.Operator, want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, nil).WithContext(test.ctx)
			recorder := httptest.NewRecorder()
			auth.RequireRole(test.required)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("authorization status = %d, want %d", recorder.Code, test.want)
			}
		})
	}
}

func TestOperatorMustHaveExplicitResourceScope(t *testing.T) {
	clusterID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987332")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/target", nil)
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{
		Subject: "operator-a", TenantID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330"),
		Roles: []auth.Role{auth.Operator},
	}))
	recorder := httptest.NewRecorder()
	auth.RequireOperatorScope(clusterID, "")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("operator without a cluster binding was allowed: status=%d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/clusters/target", nil)
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{
		Subject: "operator-a", TenantID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330"),
		Roles: []auth.Role{auth.Operator}, ClusterScopes: []uuid.UUID{clusterID},
	}))
	recorder = httptest.NewRecorder()
	auth.RequireOperatorScope(uuid.Nil, "")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("operator route with no resolved cluster was allowed: status=%d", recorder.Code)
	}
}
