package configregistry

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"testing"
)

func TestSP08ReadDraftAuthorizationBoundaries(t *testing.T) {
	service := &Service{}
	tenant := uuid.New()
	actor := auth.RequestContext{TenantID: tenant, Subject: "operator-only", Roles: []auth.Role{auth.Operator}}
	_, err := service.ReadDraft(context.Background(), actor, DraftRef{TenantID: tenant, DraftID: uuid.New()})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("operator inherited admin: %v", err)
	}
	actor.Roles = []auth.Role{auth.PlatformAdmin}
	_, err = service.ReadDraft(context.Background(), actor, DraftRef{TenantID: uuid.New(), DraftID: uuid.New()})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant draft visible: %v", err)
	}
	actor.Subject = ""
	_, err = service.ReadDraft(context.Background(), actor, DraftRef{TenantID: tenant, DraftID: uuid.New()})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("missing verified subject accepted: %v", err)
	}
}
