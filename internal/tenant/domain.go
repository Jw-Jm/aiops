package tenant

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
)

var (
	ErrUnauthorized      = errors.New("tenant administration requires an active platform_admin binding")
	ErrRevisionConflict  = errors.New("tenant or role binding revision changed")
	ErrResourceNotFound  = errors.New("tenant or role binding was not found")
	ErrInvalidInput      = errors.New("invalid tenant administration input")
	slugPattern          = regexp.MustCompile("^[a-z0-9][a-z0-9-]{1,62}$")
	namespaceNamePattern = regexp.MustCompile("^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$")
)

type Tenant struct {
	ID          uuid.UUID
	Slug        string
	DisplayName string
	Status      string
	Revision    int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type RoleBinding struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	Subject         string
	Role            auth.Role
	ClusterScopes   []uuid.UUID
	NamespaceScopes []auth.NamespaceScope
	Status          string
	Revision        int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreateTenantInput struct {
	Slug        string
	DisplayName string
}

type CreateRoleBindingInput struct {
	Subject         string
	Role            auth.Role
	ClusterScopes   []uuid.UUID
	NamespaceScopes []auth.NamespaceScope
}

func (input CreateTenantInput) Validate() error {
	if !slugPattern.MatchString(input.Slug) || strings.TrimSpace(input.DisplayName) != input.DisplayName ||
		len(input.DisplayName) < 1 || len(input.DisplayName) > 200 {
		return ErrInvalidInput
	}
	return nil
}

func (input CreateRoleBindingInput) Validate() error {
	if strings.TrimSpace(input.Subject) == "" || len(input.Subject) > 512 ||
		(input.Role != auth.Operator && input.Role != auth.PlatformAdmin) {
		return ErrInvalidInput
	}
	clusterScopes := make(map[uuid.UUID]struct{}, len(input.ClusterScopes))
	for _, clusterID := range input.ClusterScopes {
		if clusterID == uuid.Nil {
			return ErrInvalidInput
		}
		if _, found := clusterScopes[clusterID]; found {
			return ErrInvalidInput
		}
		clusterScopes[clusterID] = struct{}{}
	}
	if input.Role == auth.PlatformAdmin && (len(input.ClusterScopes) > 0 || len(input.NamespaceScopes) > 0) {
		return ErrInvalidInput
	}
	seenNamespaces := map[auth.NamespaceScope]struct{}{}
	for _, scope := range input.NamespaceScopes {
		if scope.ClusterID == uuid.Nil || !namespaceNamePattern.MatchString(scope.Namespace) {
			return ErrInvalidInput
		}
		if _, found := clusterScopes[scope.ClusterID]; !found {
			return fmt.Errorf("%w: namespace scope requires an explicit cluster scope", ErrInvalidInput)
		}
		if _, found := seenNamespaces[scope]; found {
			return ErrInvalidInput
		}
		seenNamespaces[scope] = struct{}{}
	}
	return nil
}
