package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/identity"
)

const (
	// NativeSessionCookieName is fixed by the reviewed identity contract. The
	// __Host- prefix also requires Secure, Path=/ and no Domain attribute.
	NativeSessionCookieName = "__Host-ops-session"
	NativeCSRFHeader        = "X-CSRF-Token"
	// NativeACRLevel1 is the only ACR currently proven by a native password
	// login. P2-03 must add a real second factor before this value changes.
	NativeACRLevel1 = "urn:ops:loa:1"
)

var ErrNativeSessionInvalid = errors.New("native session is invalid")

// SessionStore is the server-side, digest-only session source used by the
// native middleware. It never receives or returns bearer token plaintext.
type SessionStore interface {
	FindActiveSession(ctx context.Context, tokenDigest []byte) (identity.ActiveSession, error)
	TouchSession(ctx context.Context, tokenDigest []byte, idleTTL time.Duration) (time.Time, error)
}

type nativeSessionKey struct{}

// NativeSessionContext is server-derived request state. It is never populated
// from a cookie signature, browser role or client tenant header.
type NativeSessionContext struct {
	StepUpChallengeID      uuid.UUID
	Session                identity.ActiveSession
	IdleExpiresAt          time.Time
	CSRFToken              string
	CSRFVerified           bool
	FirstPasswordOnly      bool
	PasswordChangeOnlyPath bool
}

func WithNativeSession(ctx context.Context, value NativeSessionContext) context.Context {
	value.Session.Grants = append([]identity.SessionGrant(nil), value.Session.Grants...)
	return context.WithValue(ctx, nativeSessionKey{}, value)
}

func NativeSessionFromContext(ctx context.Context) (NativeSessionContext, bool) {
	value, ok := ctx.Value(nativeSessionKey{}).(NativeSessionContext)
	if !ok {
		return NativeSessionContext{}, false
	}
	value.Session.Grants = append([]identity.SessionGrant(nil), value.Session.Grants...)
	return value, true
}

// AuthMiddleware lets HTTP composition accept either the reviewed OIDC bearer
// authenticator or the native cookie authenticator without changing handlers.
type AuthMiddleware interface {
	Middleware(http.Handler) http.Handler
}

type NativeAuthenticator struct {
	store        SessionStore
	tenantID     uuid.UUID
	idleTTL      time.Duration
	now          func() time.Time
	publicOrigin string
}

func NewNativeAuthenticator(store SessionStore, tenantID uuid.UUID, idleTTL time.Duration) (*NativeAuthenticator, error) {
	if store == nil {
		return nil, errors.New("native session store is required")
	}
	if tenantID == uuid.Nil {
		return nil, errors.New("fixed native deployment tenant is required")
	}
	if idleTTL <= 0 || idleTTL > 24*time.Hour {
		return nil, errors.New("native session idle TTL must be between one second and 24 hours")
	}
	return &NativeAuthenticator{
		store: store, tenantID: tenantID, idleTTL: idleTTL, now: time.Now,
	}, nil
}

func (a *NativeAuthenticator) SetPublicOrigin(origin string) {
	if a != nil {
		a.publicOrigin = strings.TrimRight(strings.TrimSpace(origin), "/")
	}
}

func (a *NativeAuthenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := RequestIDFromHeader(r.Header.Get("X-Request-ID"))
		r.Header.Set("X-Request-ID", requestID)
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("Cache-Control", "no-store")
		if a == nil || a.store == nil || next == nil {
			writeAuthorizationError(w, http.StatusInternalServerError, "INTERNAL", "native authentication is not configured", false, requestID)
			return
		}
		if r.URL.Path == "/api/v1/auth/login" {
			if err := VerifyNativeOrigin(r, a.publicOrigin); err != nil {
				writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "login requires a same-origin browser request", false, requestID)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(NativeSessionCookieName)
		if err != nil || cookie.Value == "" {
			writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a native session cookie is required", false, requestID)
			return
		}
		session, err := a.store.FindActiveSession(r.Context(), identity.DigestToken(cookie.Value))
		if err != nil {
			writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "the native session is unavailable", false, requestID)
			return
		}
		csrfToken := identity.DeriveCSRFToken(cookie.Value)
		if !identity.ConstantTimeDigest(identity.DigestToken(csrfToken), session.CSRFDigest) {
			writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "the native session is unavailable", false, requestID)
			return
		}
		csrfVerified := false
		if isUnsafeHTTPMethod(r.Method) {
			if err := VerifyNativeOrigin(r, a.publicOrigin); err != nil {
				writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "same-origin browser request is required", false, requestID)
				return
			}
			supplied := r.Header.Get(NativeCSRFHeader)
			if supplied == "" ||
				!identity.ConstantTimeDigest(identity.DigestToken(supplied), session.CSRFDigest) {
				writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "a valid native CSRF token is required", false, requestID)
				return
			}
			csrfVerified = true
		}
		if session.TenantID != a.tenantID {
			writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "session is outside the fixed deployment organization", false, requestID)
			return
		}
		context, requestErr := a.requestContext(r.Context(), session, requestID, r.Header.Get("traceparent"))
		if requestErr != nil {
			writeAuthorizationError(w, http.StatusInternalServerError, "INTERNAL", "native authorization projection is invalid", true, requestID)
			return
		}
		if session.Mode == "first_password_only" && !nativePasswordChangePath(r.URL.Path) {
			writeAuthorizationError(w, http.StatusForbidden, "FORBIDDEN", "password setup session can only change its password or sign out", false, requestID)
			return
		}
		if session.MustReenrollMFA && !nativeMFASetupPath(r.URL.Path) {
			writeAuthorizationError(w, http.StatusForbidden, "MFA_SETUP_REQUIRED", "second factor setup is required", false, requestID)
			return
		}
		idleExpiresAt, err := a.store.TouchSession(r.Context(), identity.DigestToken(cookie.Value), a.idleTTL)
		if err != nil {
			writeAuthorizationError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "the native session is unavailable", false, requestID)
			return
		}
		var stepUpChallengeID uuid.UUID
		if values := r.Header.Values("X-Ops-Step-Up-Challenge"); len(values) > 0 {
			if len(values) != 1 {
				writeAuthorizationError(w, 400, "INVALID_ARGUMENT", "one step-up challenge reference is required", false, requestID)
				return
			}
			var parseErr error
			stepUpChallengeID, parseErr = uuid.Parse(values[0])
			if parseErr != nil {
				writeAuthorizationError(w, 400, "INVALID_ARGUMENT", "step-up challenge reference is invalid", false, requestID)
				return
			}
		}
		context = WithNativeSession(context, NativeSessionContext{
			StepUpChallengeID: stepUpChallengeID,
			Session:           session, IdleExpiresAt: idleExpiresAt,
			CSRFToken:              csrfToken,
			CSRFVerified:           csrfVerified,
			FirstPasswordOnly:      session.Mode == "first_password_only",
			PasswordChangeOnlyPath: nativePasswordChangePath(r.URL.Path),
		})
		next.ServeHTTP(w, r.WithContext(context))
	})
}

func (a *NativeAuthenticator) requestContext(ctx context.Context, session identity.ActiveSession, requestID, traceParent string) (context.Context, error) {
	request := RequestContext{
		TokenExpiresAt: session.AbsoluteExpiresAt,
		RequestID:      nonemptyRequestID(requestID),
		Subject:        session.Subject,
		TenantID:       session.TenantID,
		TenantIDs:      []uuid.UUID{session.TenantID},
		TraceContext:   traceParent,
		ACR:            NativeACRLevel1,
		AuthTime:       session.IssuedAt,
	}
	if session.Mode != "first_password_only" && !session.MustReenrollMFA {
		request.Roles = make([]Role, 0, len(session.Grants))
		request.ClusterScopes = make([]uuid.UUID, 0)
		request.NamespaceScopes = make([]NamespaceScope, 0)
		seenRoles := make(map[Role]struct{}, len(session.Grants))
		for _, grant := range session.Grants {
			if grant.AuthorizationVersion != session.AuthorizationVersion {
				return nil, ErrNativeSessionInvalid
			}
			role := Role(grant.Role)
			if role != Operator && role != PlatformAdmin {
				return nil, ErrNativeSessionInvalid
			}
			if _, duplicate := seenRoles[role]; !duplicate {
				request.Roles = append(request.Roles, role)
				seenRoles[role] = struct{}{}
			}
			if role != Operator {
				continue
			}
			resolved := map[string]uuid.UUID{}
			if len(grant.ClusterUIDs) > 0 {
				resolver, ok := a.store.(interface {
					ResolveClusterIDs(context.Context, uuid.UUID, []string) (map[string]uuid.UUID, error)
				})
				if !ok {
					return nil, ErrNativeSessionInvalid
				}
				var err error
				resolved, err = resolver.ResolveClusterIDs(ctx, session.TenantID, grant.ClusterUIDs)
				if err != nil {
					return nil, ErrNativeSessionInvalid
				}
				for _, clusterUID := range grant.ClusterUIDs {
					clusterID, ok := resolved[clusterUID]
					if !ok || clusterID == uuid.Nil {
						return nil, ErrNativeSessionInvalid
					}
					if len(grant.Namespaces) == 0 {
						request.ClusterScopes = append(request.ClusterScopes, clusterID)
					}
				}
			}
			// Existing grant storage keeps cluster UIDs and namespaces in
			// separate arrays. Namespaces are projected only for a grant that
			// names exactly one registered cluster; otherwise the ambiguous
			// grant fails closed instead of widening scope.
			if len(grant.Namespaces) > 0 {
				if len(grant.ClusterUIDs) != 1 {
					return nil, ErrNativeSessionInvalid
				}
				clusterID, ok := resolved[grant.ClusterUIDs[0]]
				if !ok || clusterID == uuid.Nil {
					return nil, ErrNativeSessionInvalid
				}
				for _, namespace := range grant.Namespaces {
					if strings.TrimSpace(namespace) == "" {
						return nil, ErrNativeSessionInvalid
					}
					request.NamespaceScopes = append(request.NamespaceScopes, NamespaceScope{
						ClusterID: clusterID, Namespace: namespace,
					})
				}
			}
		}
	}
	return WithRequestContext(ctx, request), nil
}

// VerifyNativeOrigin accepts exact same-origin browser evidence only. When a
// deployment behind TLS termination provides its public origin explicitly, that
// value is authoritative; arbitrary Forwarded/X-Forwarded-* headers are never
// trusted. With no configured origin, only the direct request scheme/host is
// accepted.
func VerifyNativeOrigin(r *http.Request, trustedOrigin string) error {
	if r == nil {
		return errors.New("request is required")
	}
	fetchSite := r.Header.Get("Sec-Fetch-Site")
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" && fetchSite == "" {
		return errors.New("same-origin evidence is required")
	}
	if fetchSite != "" && fetchSite != "same-origin" {
		return errors.New("cross-site browser request")
	}
	if origin == "" {
		return nil
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("origin is invalid")
	}
	trustedOrigin = strings.TrimRight(strings.TrimSpace(trustedOrigin), "/")
	if trustedOrigin != "" {
		expected, err := url.Parse(trustedOrigin)
		if err != nil || expected.Scheme == "" || expected.Host == "" ||
			parsed.Scheme != expected.Scheme || parsed.Host != expected.Host {
			return errors.New("origin does not match configured deployment origin")
		}
		return nil
	}
	if parsed.Host != r.Host {
		return errors.New("origin does not match deployment")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if parsed.Scheme != scheme {
		return errors.New("origin scheme does not match deployment")
	}
	return nil
}

func isUnsafeHTTPMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func nativePasswordChangePath(path string) bool {
	return path == "/api/v1/auth/session" ||
		path == "/api/v1/auth/password:change" ||
		path == "/api/v1/auth/logout"
}

func nativeMFASetupPath(path string) bool {
	return path == "/api/v1/auth/session" || path == "/api/v1/auth/logout" ||
		path == "/api/v1/auth/mfa/status" || path == "/api/v1/auth/mfa/enrollments" ||
		strings.HasPrefix(path, "/api/v1/auth/mfa/enrollments/") ||
		path == "/api/v1/auth/step-up-challenges" || strings.HasPrefix(path, "/api/v1/auth/step-up-challenges/")
}
