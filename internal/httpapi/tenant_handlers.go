package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/gen/api"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"ops-platform/internal/tenant"
)

const (
	adminTenantsPath      = "/api/v1/admin/tenants"
	adminRoleBindingsPath = "/api/v1/admin/role-bindings"
)

type TenantAdminHandlers struct {
	service *tenant.Service
}

func NewTenantAdminRouter(service *tenant.Service, pool persistence.TxBeginner) (chi.Router, error) {
	if service == nil || pool == nil {
		return nil, errors.New("tenant service and idempotency pool are required")
	}
	handlers := TenantAdminHandlers{service: service}
	router := chi.NewRouter()
	router.With(auth.RequireRole(auth.PlatformAdmin)).Get(adminTenantsPath, handlers.getTenants)
	router.With(auth.RequireRole(auth.PlatformAdmin)).Get(adminRoleBindingsPath, handlers.getRoleBindings)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "create-tenant"), requireTenantAdminStepUp).Post(adminTenantsPath, handlers.createTenant)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "create-role-binding"), requireTenantAdminStepUp).Post(adminRoleBindingsPath, handlers.createRoleBinding)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantAdminStepUpIdempotency(pool, "set-operator-role-binding-status")).Patch(adminRoleBindingsPath+"/{bindingId}/status", handlers.setOperatorRoleBindingStatus)
	return router, nil
}

func newTenantIdempotency(pool persistence.TxBeginner, operation string) func(http.Handler) http.Handler {
	return tenantIdempotency(pool, operation, nil)
}

func newTenantAdminStepUpIdempotency(pool persistence.TxBeginner, operation string) func(http.Handler) http.Handler {
	return tenantIdempotency(pool, operation, func(r *http.Request, tx pgx.Tx, _ persistence.Scope) error {
		request, ok := auth.RequestContextFromContext(r.Context())
		if !ok {
			return auth.ErrUnauthenticated
		}
		// AuthorizeTx runs inside the tenant transaction before checking the
		// idempotency ledger, including successful-response replay.
		_, err := auth.TouchCurrentStepUpSession(r.Context(), tx, request, []string{auth.StepUpACRLevel2})
		return err
	})
}

func tenantIdempotency(pool persistence.TxBeginner, operation string, authorizeTx func(*http.Request, pgx.Tx, persistence.Scope) error) func(http.Handler) http.Handler {
	middleware := IdempotencyMiddleware{
		Pool:        pool,
		AuthorizeTx: authorizeTx,
		Resolve: func(r *http.Request) (persistence.Scope, error) {
			request, ok := auth.RequestContextFromContext(r.Context())
			if !ok {
				return persistence.Scope{}, errors.New("authenticated request context is missing")
			}
			return persistence.Scope{TenantID: request.TenantID, Subject: request.Subject, Operation: operation}, nil
		},
		Authorize: func(r *http.Request, _ persistence.Scope) error {
			if _, ok := auth.RequestContextFromContext(r.Context()); !ok {
				return auth.ErrUnauthenticated
			}
			if !auth.HasRole(r.Context(), auth.PlatformAdmin) {
				return auth.ErrForbidden
			}
			return nil
		},
	}
	return func(next http.Handler) http.Handler { return middleware.Wrap(next) }
}

func (h TenantAdminHandlers) getTenants(w http.ResponseWriter, r *http.Request) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return
	}
	current, err := h.service.GetCurrentTenant(r.Context(), request)
	if err != nil {
		writeTenantServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusOK, api.PageEnvelope{
		Data: []interface{}{tenantJSON(current)}, Meta: api.PageEnvelope_Meta{}, RequestId: request.RequestID,
	})
}

func (h TenantAdminHandlers) getRoleBindings(w http.ResponseWriter, r *http.Request) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return
	}
	bindings, err := h.service.ListCurrentRoleBindings(r.Context(), request)
	if err != nil {
		writeTenantServiceError(w, err, request.RequestID)
		return
	}
	data := make([]interface{}, 0, len(bindings))
	for _, binding := range bindings {
		data = append(data, roleBindingJSON(binding))
	}
	writeJSON(w, http.StatusOK, api.PageEnvelope{Data: data, Meta: api.PageEnvelope_Meta{}, RequestId: request.RequestID})
}

func (h TenantAdminHandlers) createTenant(w http.ResponseWriter, r *http.Request) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return
	}
	body, err := decodeTenantBody(r)
	if err != nil {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid", false, request.RequestID)
		return
	}
	input := tenant.CreateTenantInput{Slug: stringValue(body["slug"]), DisplayName: stringValue(body["displayName"])}
	tx, ok := TransactionFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "tenant transaction is unavailable", true, request.RequestID)
		return
	}
	created, err := h.service.CreateTenant(r.Context(), tx, request, input)
	if err != nil {
		writeTenantServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusCreated, api.SuccessEnvelope{Data: tenantJSON(created), RequestId: request.RequestID})
}

func (h TenantAdminHandlers) createRoleBinding(w http.ResponseWriter, r *http.Request) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return
	}
	body, err := decodeTenantBody(r)
	if err != nil {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid", false, request.RequestID)
		return
	}
	input, err := parseRoleBindingInput(body)
	if err != nil {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "role binding scope is invalid", false, request.RequestID)
		return
	}
	tx, ok := TransactionFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "tenant transaction is unavailable", true, request.RequestID)
		return
	}
	created, err := h.service.CreateRoleBinding(r.Context(), tx, request, input)
	if err != nil {
		writeTenantServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusCreated, api.SuccessEnvelope{Data: roleBindingJSON(created), RequestId: request.RequestID})
}

func (h TenantAdminHandlers) setOperatorRoleBindingStatus(w http.ResponseWriter, r *http.Request) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeTenantError(w, 401, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return
	}
	bindingID, err := uuid.Parse(chi.URLParam(r, "bindingId"))
	var body api.OperatorRoleBindingStatusUpdateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	var trailing any
	if err != nil || bindingID == uuid.Nil || decoder.Decode(&body) != nil || decoder.Decode(&trailing) != io.EOF ||
		body.ExpectedRevision < 1 || (body.Status != "active" && body.Status != "disabled") {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "revision and operator binding status are required", false, request.RequestID)
		return
	}
	tx, ok := TransactionFromContext(r.Context())
	if !ok {
		writeTenantError(w, 500, "INTERNAL", "tenant transaction is unavailable", true, request.RequestID)
		return
	}
	updated, err := h.service.SetOperatorRoleBindingStatus(r.Context(), tx, request, bindingID, int64(body.ExpectedRevision), string(body.Status))
	if err != nil {
		writeTenantServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, 200, api.SuccessEnvelope{Data: roleBindingJSON(updated), RequestId: request.RequestID})
}

func decodeTenantBody(r *http.Request) (map[string]any, error) {
	defer r.Body.Close()
	var body map[string]any
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := decoder.Decode(&body); err != nil || body == nil {
		return nil, errors.New("request body must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("request body contains trailing JSON")
	}
	return body, nil
}

func parseRoleBindingInput(body map[string]any) (tenant.CreateRoleBindingInput, error) {
	input := tenant.CreateRoleBindingInput{Subject: stringValue(body["subject"]), Role: auth.Role(stringValue(body["role"]))}
	if value, present := body["clusterScopes"]; present {
		raw, ok := value.([]any)
		if !ok {
			return tenant.CreateRoleBindingInput{}, tenant.ErrInvalidInput
		}
		for _, item := range raw {
			clusterID, err := uuid.Parse(stringValue(item))
			if err != nil {
				return tenant.CreateRoleBindingInput{}, tenant.ErrInvalidInput
			}
			input.ClusterScopes = append(input.ClusterScopes, clusterID)
		}
	}
	if value, present := body["namespaceScopes"]; present {
		raw, ok := value.([]any)
		if !ok {
			return tenant.CreateRoleBindingInput{}, tenant.ErrInvalidInput
		}
		for _, item := range raw {
			record, ok := item.(map[string]any)
			if !ok {
				return tenant.CreateRoleBindingInput{}, tenant.ErrInvalidInput
			}
			clusterID, err := uuid.Parse(stringValue(record["clusterId"]))
			if err != nil {
				return tenant.CreateRoleBindingInput{}, tenant.ErrInvalidInput
			}
			input.NamespaceScopes = append(input.NamespaceScopes, auth.NamespaceScope{
				ClusterID: clusterID, Namespace: stringValue(record["namespace"]),
			})
		}
	}
	return input, input.Validate()
}

func tenantJSON(value tenant.Tenant) map[string]any {
	return map[string]any{
		"tenantId": value.ID, "slug": value.Slug, "displayName": value.DisplayName,
		"status": value.Status, "revision": value.Revision, "createdAt": value.CreatedAt, "updatedAt": value.UpdatedAt,
	}
}

func roleBindingJSON(value tenant.RoleBinding) map[string]any {
	return map[string]any{
		"bindingId": value.ID, "tenantId": value.TenantID, "subject": value.Subject, "role": value.Role,
		"clusterScopes": value.ClusterScopes, "namespaceScopes": value.NamespaceScopes, "status": value.Status,
		"revision": value.Revision, "createdAt": value.CreatedAt, "updatedAt": value.UpdatedAt,
	}
}

func writeTenantServiceError(w http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, requestID)
	case errors.Is(err, tenant.ErrUnauthorized), errors.Is(err, auth.ErrForbidden):
		writeTenantError(w, http.StatusForbidden, "FORBIDDEN", "platform_admin authorization is required", false, requestID)
	case errors.Is(err, tenant.ErrInvalidInput):
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "tenant or role binding input is invalid", false, requestID)
	case errors.Is(err, tenant.ErrRevisionConflict):
		writeTenantError(w, http.StatusConflict, "CONFLICT", "the requested revision is stale", false, requestID)
	case errors.Is(err, tenant.ErrResourceNotFound), errors.Is(err, pgx.ErrNoRows):
		writeTenantError(w, http.StatusNotFound, "NOT_FOUND", "tenant or role binding was not found", false, requestID)
	default:
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "tenant request failed", true, requestID)
	}
}

func writeTenantError(w http.ResponseWriter, status int, code, message string, retryable bool, requestID string) {
	if requestID == "" {
		requestID = uuid.Must(uuid.NewV7()).String()
	}
	writeJSON(w, status, api.ErrorEnvelope{
		Code: api.ErrorEnvelopeCode(code), Message: message, RequestId: requestID, Retryable: retryable,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func stringValue(value any) string {
	result, _ := value.(string)
	return strings.TrimSpace(result)
}
