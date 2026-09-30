package auth

import (
	"context"
	"regexp"
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	Operator      Role = "operator"
	PlatformAdmin Role = "platform_admin"
)

type NamespaceScope struct {
	ClusterID uuid.UUID `json:"clusterId"`
	Namespace string    `json:"namespace"`
}

// RequestContext is the authenticated, tenant-bound identity used by platform handlers.
// TenantID and scopes are populated only after signed OIDC claims are checked against active
// platform role bindings; request headers and bodies cannot grant them.
type RequestContext struct {
	RequestID       string
	Subject         string
	TenantID        uuid.UUID
	TenantIDs       []uuid.UUID
	Roles           []Role
	ClusterScopes   []uuid.UUID
	NamespaceScopes []NamespaceScope
	TraceContext    string
	KeycloakSID     string
	ACR             string
	AuthTime        time.Time
}

type requestContextKey struct{}

var traceParentPattern = regexp.MustCompile(`^[\da-f]{2}-[\da-f]{32}-[\da-f]{16}-[\da-f]{2}$`)

func WithRequestContext(ctx context.Context, value RequestContext) context.Context {
	value.RequestID = nonemptyRequestID(value.RequestID)
	value.TenantIDs = append([]uuid.UUID(nil), value.TenantIDs...)
	value.Roles = append([]Role(nil), value.Roles...)
	value.ClusterScopes = append([]uuid.UUID(nil), value.ClusterScopes...)
	value.NamespaceScopes = append([]NamespaceScope(nil), value.NamespaceScopes...)
	value.TraceContext = ValidTraceParent(value.TraceContext)
	return context.WithValue(ctx, requestContextKey{}, value)
}

func RequestContextFromContext(ctx context.Context) (RequestContext, bool) {
	value, ok := ctx.Value(requestContextKey{}).(RequestContext)
	if !ok {
		return RequestContext{}, false
	}
	value.TenantIDs = append([]uuid.UUID(nil), value.TenantIDs...)
	value.Roles = append([]Role(nil), value.Roles...)
	value.ClusterScopes = append([]uuid.UUID(nil), value.ClusterScopes...)
	value.NamespaceScopes = append([]NamespaceScope(nil), value.NamespaceScopes...)
	return value, true
}

func ValidTraceParent(value string) string {
	if !traceParentPattern.MatchString(value) || value[:2] == "ff" || value[3:35] == "00000000000000000000000000000000" || value[36:52] == "0000000000000000" {
		return ""
	}
	return value
}

func nonemptyRequestID(value string) string {
	if value != "" && len(value) <= 128 {
		return value
	}
	return uuid.Must(uuid.NewV7()).String()
}

func RequestID(ctx context.Context) (string, bool) {
	value, ok := RequestContextFromContext(ctx)
	return value.RequestID, ok
}
