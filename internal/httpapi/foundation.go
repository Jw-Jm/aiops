package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"ops-platform/gen/api"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
	"ops-platform/internal/policy"
	"ops-platform/internal/source"
	"ops-platform/internal/tenant"
)

// NewFoundationHandler composes the SP-03 management routes. Later domain APIs
// include the authorized SP-04 resource/evidence routes.
func NewFoundationHandler(pool persistence.TxBeginner, authenticator *auth.Authenticator, trust configregistry.SignatureVerifier, runtime *observability.Runtime) (http.Handler, error) {
	return NewFoundationHandlerWithSP04(pool, authenticator, trust, runtime, &SP04Handlers{Pool: pool})
}

func NewFoundationHandlerWithSP04(pool persistence.TxBeginner, authenticator *auth.Authenticator, trust configregistry.SignatureVerifier, runtime *observability.Runtime, sp04 *SP04Handlers) (http.Handler, error) {
	if pool == nil || authenticator == nil || trust == nil || runtime == nil {
		return nil, errors.New("foundation persistence, authentication, trust and observability are required")
	}
	if sp04 == nil {
		sp04 = &SP04Handlers{Pool: pool}
	}
	tenantService, err := tenant.NewService(pool)
	if err != nil {
		return nil, err
	}
	tenantRouter, err := NewTenantAdminRouter(tenantService, pool)
	if err != nil {
		return nil, err
	}
	sourceService, err := source.NewService(pool, nil)
	if err != nil {
		return nil, err
	}
	sourceRouter, err := NewSourceAdminRouter(sourceService, pool)
	if err != nil {
		return nil, err
	}
	compiler, err := policy.NewBundleCompiler(trust)
	if err != nil {
		return nil, err
	}
	registryService, err := configregistry.NewService(pool, trust, compiler)
	if err != nil {
		return nil, err
	}
	registryRouter, err := NewConfigAdminRouter(registryService, pool)
	if err != nil {
		return nil, err
	}
	stepUp := IdempotencyMiddleware{
		Pool: pool,
		Resolve: func(r *http.Request) (persistence.Scope, error) {
			request, ok := auth.RequestContextFromContext(r.Context())
			if !ok {
				return persistence.Scope{}, auth.ErrUnauthenticated
			}
			return persistence.Scope{TenantID: request.TenantID, Subject: request.Subject, Operation: "create-step-up-session"}, nil
		},
		Authorize: func(r *http.Request, _ persistence.Scope) error {
			if !auth.HasRole(r.Context(), auth.Operator) && !auth.HasRole(r.Context(), auth.PlatformAdmin) {
				return auth.ErrForbidden
			}
			return nil
		},
	}.Wrap(http.HandlerFunc(createStepUpSession))
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/resources"), strings.HasPrefix(r.URL.Path, "/api/v1/evidence"), r.URL.Path == "/api/v1/diagnostic-graphs:build", r.URL.Path == "/api/v1/admin/legal-holds":
			sp04.ServeHTTP(w, r)
		case r.URL.Path == "/api/v1/auth/step-up-sessions" && r.Method == http.MethodPost:
			stepUp.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, adminTenantsPath), strings.HasPrefix(r.URL.Path, adminRoleBindingsPath):
			tenantRouter.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, adminSourcesPath), strings.HasPrefix(r.URL.Path, adminClustersPath):
			sourceRouter.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/v1/admin/"):
			registryRouter.ServeHTTP(w, r)
		default:
			request, _ := auth.RequestContextFromContext(r.Context())
			writeTenantError(w, http.StatusNotFound, "NOT_FOUND", "endpoint is unavailable", false, request.RequestID)
		}
	})
	return ObservabilityMiddleware(runtime.Logger, runtime.Metrics, runtime.Tracing)(authenticator.Middleware(dispatch)), nil
}

func createStepUpSession(w http.ResponseWriter, r *http.Request) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity is required", false, "")
		return
	}
	body, err := decodeTenantBody(r)
	if err != nil || len(body) != 0 {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "step-up identity must come from verified claims", false, request.RequestID)
		return
	}
	tx, ok := TransactionFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "tenant transaction is unavailable", true, request.RequestID)
		return
	}
	session, err := auth.RecordStepUpSession(r.Context(), tx, request, uuid.Must(uuid.NewV7()), []string{auth.StepUpACRLevel2})
	if err != nil {
		writeTenantError(w, http.StatusForbidden, "STEP_UP_REQUIRED", "fresh Keycloak step-up is required", false, request.RequestID)
		return
	}
	writeJSON(w, http.StatusCreated, api.SuccessEnvelope{Data: api.StepUpSession{
		SessionId: session.SessionID.String(), ExpiresAt: session.CreatedAt.Add(auth.StepUpLifetime), LastUsedAt: session.LastUsedAt,
	}, RequestId: request.RequestID})
}
