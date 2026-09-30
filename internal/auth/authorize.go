package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

var (
	ErrUnauthenticated = errors.New("verified request context is required")
	ErrForbidden       = errors.New("required role is not assigned")
)

func HasRole(ctx context.Context, role Role) bool {
	request, ok := RequestContextFromContext(ctx)
	if !ok {
		return false
	}
	for _, granted := range request.Roles {
		if granted == role {
			return true
		}
	}
	return false
}

func RequireRole(role Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request, ok := RequestContextFromContext(r.Context())
			requestID := RequestIDFromHeader(r.Header.Get("X-Request-ID"))
			if ok {
				requestID = request.RequestID
			}
			w.Header().Set("X-Request-ID", requestID)
			if !ok {
				writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, requestID)
				return
			}
			if !HasRole(r.Context(), role) {
				writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "the required role is not assigned", false, requestID)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RequireOperatorScope(clusterID uuid.UUID, namespace string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request, ok := RequestContextFromContext(r.Context())
			requestID := RequestIDFromHeader(r.Header.Get("X-Request-ID"))
			if ok {
				requestID = request.RequestID
			}
			w.Header().Set("X-Request-ID", requestID)
			if !ok {
				writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, requestID)
				return
			}
			if !containsRole(request.Roles, Operator) || !hasScope(request, clusterID, namespace) {
				writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "the operator grant does not cover this resource scope", false, requestID)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func containsRole(roles []Role, role Role) bool {
	for _, granted := range roles {
		if granted == role {
			return true
		}
	}
	return false
}

func hasScope(request RequestContext, clusterID uuid.UUID, namespace string) bool {
	if clusterID == uuid.Nil {
		return false
	}
	clusterGranted := false
	for _, granted := range request.ClusterScopes {
		if granted == clusterID {
			clusterGranted = true
			break
		}
	}
	if !clusterGranted {
		return false
	}
	if namespace == "" {
		return true
	}
	for _, granted := range request.NamespaceScopes {
		if granted.ClusterID == clusterID && granted.Namespace == namespace {
			return true
		}
	}
	return false
}

func writeAuthorizationError(w http.ResponseWriter, status int, code, message string, retryable bool, requestID string) {
	if requestID == "" {
		requestID = nonemptyRequestID("")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": code, "message": message, "requestId": requestID, "retryable": retryable,
	})
}
