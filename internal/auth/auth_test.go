package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRequestContext(t *testing.T) {
	want := RequestContext{
		RequestID: "request-1", Subject: "subject-a",
		TenantID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330"),
		Roles:    []Role{Operator}, ClusterScopes: []uuid.UUID{uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987331")},
		NamespaceScopes: []NamespaceScope{{ClusterID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987331"), Namespace: "production"}}, TraceContext: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	ctx := WithRequestContext(context.Background(), want)
	got, ok := RequestContextFromContext(ctx)
	if !ok || got.Subject != want.Subject || got.TenantID != want.TenantID || got.RequestID != want.RequestID || got.TraceContext != want.TraceContext {
		t.Fatalf("request context round-trip failed: got=%#v ok=%v", got, ok)
	}
}

func TestRolesDoNotInherit(t *testing.T) {
	adminContext := WithRequestContext(context.Background(), RequestContext{Roles: []Role{PlatformAdmin}})
	if HasRole(adminContext, Operator) {
		t.Fatal("platform_admin inherited operator")
	}
	if !HasRole(adminContext, PlatformAdmin) {
		t.Fatal("platform_admin grant was lost")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", nil).WithContext(adminContext)
	recorder := httptest.NewRecorder()
	RequireRole(Operator)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("platform_admin without operator binding reached operator route: status=%d", recorder.Code)
	}
}

func TestStepUpBoundaries(t *testing.T) {
	now := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	valid := StepUpSession{SessionID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987332"), TenantID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330"), Subject: "subject-a", KeycloakSID: "sid-a", ACR: "urn:ops:loa:2", AuthTime: now.Add(-10 * time.Minute), CreatedAt: now.Add(-30 * time.Minute), LastUsedAt: now.Add(-20 * time.Minute)}
	if err := ValidateStepUp(valid, now); err != nil {
		t.Fatalf("valid step-up session rejected: %v", err)
	}
	for name, changed := range map[string]StepUpSession{
		"absolute-expired": func() StepUpSession { value := valid; value.CreatedAt = now.Add(-time.Hour); return value }(),
		"idle-expired":     func() StepUpSession { value := valid; value.LastUsedAt = now.Add(-time.Hour); return value }(),
		"revoked":          func() StepUpSession { value := valid; at := now.Add(-time.Minute); value.RevokedAt = &at; return value }(),
		"future-auth-time": func() StepUpSession { value := valid; value.AuthTime = now.Add(time.Minute); return value }(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateStepUp(changed, now); err == nil {
				t.Fatal("invalid or expired step-up session was accepted")
			}
		})
	}
}

func TestStepUpBindsIdentityAndTenant(t *testing.T) {
	now := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	tenantID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330")
	request := RequestContext{TenantID: tenantID, Subject: "subject-a", KeycloakSID: "sid-a", ACR: "urn:ops:loa:2", AuthTime: now.Add(-time.Minute)}
	session := StepUpSession{SessionID: uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987332"), TenantID: tenantID, Subject: "subject-a", KeycloakSID: "sid-a", ACR: "urn:ops:loa:2", AuthTime: request.AuthTime, CreatedAt: now.Add(-time.Minute), LastUsedAt: now.Add(-time.Minute)}
	if err := ValidateStepUpForContext(session, request, now); err != nil {
		t.Fatalf("matching step-up session rejected: %v", err)
	}
	for name, mutate := range map[string]func(*RequestContext){
		"tenant":    func(value *RequestContext) { value.TenantID = uuid.New() },
		"subject":   func(value *RequestContext) { value.Subject = "subject-b" },
		"sid":       func(value *RequestContext) { value.KeycloakSID = "sid-b" },
		"acr":       func(value *RequestContext) { value.ACR = "urn:ops:loa:1" },
		"auth-time": func(value *RequestContext) { value.AuthTime = now.Add(-2 * time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			mutate(&changed)
			if err := ValidateStepUpForContext(session, changed, now); err == nil {
				t.Fatal("step-up session was accepted for a different identity context")
			}
		})
	}
}
