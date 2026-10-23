package tenant

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"testing"
)

func TestNativeSessionCannotMutateLegacyAuthority(t *testing.T) {
	ctx := auth.WithNativeSession(context.Background(), auth.NativeSessionContext{})
	service := &Service{}
	actor := auth.RequestContext{}
	// These calls must fail before touching a database or creating a legacy grant.
	_, err := service.CreateTenant(ctx, nil, actor, CreateTenantInput{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("native legacy tenant creation: %v", err)
	}
	_, err = service.CreateRoleBinding(ctx, nil, actor, CreateRoleBindingInput{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("native legacy grant creation: %v", err)
	}
	_, err = service.UpdateRoleBinding(ctx, nil, actor, uuid.Nil, 1, CreateRoleBindingInput{}, "active")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("native legacy grant update: %v", err)
	}
	_, err = service.SetOperatorRoleBindingStatus(ctx, nil, actor, uuid.Nil, 1, "disabled")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("native legacy grant status: %v", err)
	}
}
