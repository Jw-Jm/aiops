package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
)

type TokenVerifier interface {
	Verify(context.Context, string) (*oidc.IDToken, error)
}

type RoleBinding struct {
	Role            Role
	ClusterScopes   []uuid.UUID
	NamespaceScopes []NamespaceScope
}

type RoleBindingSource interface {
	LoadRoleBindings(context.Context, uuid.UUID, string) ([]RoleBinding, error)
}

type Authenticator struct {
	verifier     TokenVerifier
	roleBindings RoleBindingSource
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func NewOIDCAuthenticator(ctx context.Context, issuer, clientID string, roleBindings RoleBindingSource) (*Authenticator, error) {
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback(parsed.Hostname()))) {
		return nil, errors.New("OIDC issuer must use HTTPS except for a loopback test issuer")
	}
	if clientID == "" || roleBindings == nil {
		return nil, errors.New("OIDC client id and role binding source are required")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer: %w", err)
	}
	verifier := provider.VerifierContext(ctx, &oidc.Config{
		ClientID:             clientID,
		SupportedSigningAlgs: []string{"RS256"},
	})
	return &Authenticator{verifier: verifier, roleBindings: roleBindings}, nil
}

func NewAuthenticator(verifier TokenVerifier, roleBindings RoleBindingSource) (*Authenticator, error) {
	if verifier == nil || roleBindings == nil {
		return nil, errors.New("OIDC token verifier and role binding source are required")
	}
	return &Authenticator{verifier: verifier, roleBindings: roleBindings}, nil
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := RequestIDFromHeader(r.Header.Get("X-Request-ID"))
		w.Header().Set("X-Request-ID", requestID)
		if a == nil || a.verifier == nil || a.roleBindings == nil || next == nil {
			writeAuthorizationError(w, http.StatusInternalServerError, "INTERNAL", "authentication is not configured", false, requestID)
			return
		}
		authorization := strings.Fields(r.Header.Get("Authorization"))
		if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") || authorization[1] == "" {
			writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a bearer token is required", false, requestID)
			return
		}
		token, err := a.verifier.Verify(r.Context(), authorization[1])
		if err != nil || token == nil || token.Subject == "" {
			writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "the bearer token is invalid", false, requestID)
			return
		}
		claims := struct {
			TenantID  string   `json:"tenant_id"`
			TenantIDs []string `json:"tenant_ids"`
			SID       string   `json:"sid"`
			ACR       string   `json:"acr"`
			AuthTime  int64    `json:"auth_time"`
		}{}
		if err := token.Claims(&claims); err != nil {
			writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "the bearer token is missing required claims", false, requestID)
			return
		}
		tenantIDs, err := parseTenantIDs(claims.TenantID, claims.TenantIDs)
		if err != nil || len(tenantIDs) == 0 {
			writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "the bearer token has no valid tenant membership claim", false, requestID)
			return
		}
		tenantID := tenantIDs[0]
		if selected := r.Header.Get("X-Tenant-ID"); selected != "" {
			selectedID, parseErr := uuid.Parse(selected)
			if parseErr != nil || !containsTenantID(tenantIDs, selectedID) {
				writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "the selected tenant is not in the verified tenant membership claim", false, requestID)
				return
			}
			tenantID = selectedID
		}
		bindings, err := a.roleBindings.LoadRoleBindings(r.Context(), tenantID, token.Subject)
		if err != nil {
			writeAuthorizationError(w, http.StatusInternalServerError, "INTERNAL", "tenant authorization could not be loaded", true, requestID)
			return
		}
		if len(bindings) == 0 {
			writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "the subject has no active role binding in this tenant", false, requestID)
			return
		}
		requestContext, err := requestContextFromToken(token, tenantID, tenantIDs, bindings, requestID, r.Header.Get("traceparent"))
		if err != nil {
			writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "the tenant role binding is invalid", false, requestID)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithRequestContext(r.Context(), requestContext)))
	})
}

func requestContextFromToken(token *oidc.IDToken, tenantID uuid.UUID, tenantIDs []uuid.UUID, bindings []RoleBinding, requestID, traceParent string) (RequestContext, error) {
	claims := struct {
		SID      string `json:"sid"`
		ACR      string `json:"acr"`
		AuthTime int64  `json:"auth_time"`
	}{}
	if err := token.Claims(&claims); err != nil {
		return RequestContext{}, err
	}
	result := RequestContext{
		RequestID: nonemptyRequestID(requestID), Subject: token.Subject, TenantID: tenantID,
		TenantIDs: tenantIDs, TraceContext: traceParent, KeycloakSID: claims.SID,
		ACR: claims.ACR, AuthTime: time.Unix(claims.AuthTime, 0).UTC(),
	}
	if claims.AuthTime == 0 {
		result.AuthTime = time.Time{}
	}
	seenRoles, seenClusters, seenNamespaces := map[Role]bool{}, map[uuid.UUID]bool{}, map[NamespaceScope]bool{}
	for _, binding := range bindings {
		if binding.Role != Operator && binding.Role != PlatformAdmin {
			return RequestContext{}, fmt.Errorf("unsupported role %q", binding.Role)
		}
		if !seenRoles[binding.Role] {
			result.Roles = append(result.Roles, binding.Role)
			seenRoles[binding.Role] = true
		}
		if binding.Role != Operator {
			continue
		}
		for _, clusterID := range binding.ClusterScopes {
			if clusterID == uuid.Nil {
				return RequestContext{}, errors.New("empty cluster scope")
			}
			if !seenClusters[clusterID] {
				result.ClusterScopes = append(result.ClusterScopes, clusterID)
				seenClusters[clusterID] = true
			}
		}
		for _, namespace := range binding.NamespaceScopes {
			if namespace.ClusterID == uuid.Nil || strings.TrimSpace(namespace.Namespace) == "" {
				return RequestContext{}, errors.New("invalid namespace scope")
			}
			if !seenNamespaces[namespace] {
				result.NamespaceScopes = append(result.NamespaceScopes, namespace)
				seenNamespaces[namespace] = true
			}
		}
	}
	return result, nil
}

func parseTenantIDs(primary string, membership []string) ([]uuid.UUID, error) {
	if len(membership) == 0 {
		if primary == "" {
			return nil, errors.New("tenant claims are missing")
		}
		membership = []string{primary}
	}
	result := make([]uuid.UUID, 0, len(membership))
	seen := make(map[uuid.UUID]struct{}, len(membership))
	for _, raw := range membership {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return nil, errors.New("tenant membership claim contains an invalid identifier")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errors.New("tenant membership claim contains a duplicate identifier")
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	if primary != "" {
		primaryID, err := uuid.Parse(primary)
		if err != nil || !containsTenantID(result, primaryID) {
			return nil, errors.New("primary tenant is not in tenant membership claim")
		}
	}
	return result, nil
}

func containsTenantID(values []uuid.UUID, selected uuid.UUID) bool {
	for _, value := range values {
		if value == selected {
			return true
		}
	}
	return false
}

type PostgreSQLRoleBindingSource struct {
	pool persistence.TxBeginner
}

func NewPostgreSQLRoleBindingSource(pool persistence.TxBeginner) (*PostgreSQLRoleBindingSource, error) {
	if pool == nil {
		return nil, errors.New("role binding database pool is required")
	}
	return &PostgreSQLRoleBindingSource{pool: pool}, nil
}

func (s *PostgreSQLRoleBindingSource) LoadRoleBindings(ctx context.Context, tenantID uuid.UUID, subject string) ([]RoleBinding, error) {
	if tenantID == uuid.Nil || subject == "" {
		return nil, errors.New("tenant and subject are required")
	}
	var result []RoleBinding
	err := persistence.WithTenantTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT rb.role_name, rb.cluster_scopes, rb.namespace_scopes
			FROM platform.role_bindings AS rb
			JOIN platform.tenants AS tenant ON tenant.tenant_id = rb.tenant_id
			WHERE rb.tenant_id = $1 AND rb.subject = $2 AND rb.status = 'active' AND tenant.status = 'active'
			ORDER BY rb.role_name, rb.binding_id`, tenantID, subject)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var binding RoleBinding
			var clustersJSON, namespacesJSON []byte
			if err := rows.Scan(&binding.Role, &clustersJSON, &namespacesJSON); err != nil {
				return err
			}
			if err := json.Unmarshal(clustersJSON, &binding.ClusterScopes); err != nil {
				return fmt.Errorf("decode cluster role scope: %w", err)
			}
			if err := json.Unmarshal(namespacesJSON, &binding.NamespaceScopes); err != nil {
				return fmt.Errorf("decode namespace role scope: %w", err)
			}
			result = append(result, binding)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("load tenant role bindings: %w", err)
	}
	return result, nil
}
