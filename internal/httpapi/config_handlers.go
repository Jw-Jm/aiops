package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/gen/api"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/persistence"
)

const (
	registryDraftsPath      = "/api/v1/admin/registry-drafts"
	registryActivationsPath = "/api/v1/admin/registry-activations"
	registryVersionsPath    = "/api/v1/admin/registry-versions"
)

type ConfigAdminHandlers struct {
	service *configregistry.Service
}

func NewConfigAdminRouter(service *configregistry.Service, pool persistence.TxBeginner) (chi.Router, error) {
	if service == nil || pool == nil {
		return nil, errors.New("configuration registry service and idempotency pool are required")
	}
	handlers := ConfigAdminHandlers{service: service}
	router := chi.NewRouter()
	router.With(auth.RequireRole(auth.PlatformAdmin)).Get("/api/v1/admin/policy-bundles", handlers.getPolicyBundles)
	router.With(auth.RequireRole(auth.PlatformAdmin)).Get("/api/v1/admin/recipes", handlers.getRecipes)
	router.With(auth.RequireRole(auth.PlatformAdmin)).Get("/api/v1/admin/tools", handlers.getTools)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "create-registry-draft"), requireTenantAdminStepUp).Post(registryDraftsPath, handlers.createDraft)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "update-registry-draft"), requireTenantAdminStepUp).Patch(registryDraftsPath+"/{draftId}", handlers.updateDraft)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "publish-registry-draft"), requireTenantAdminStepUp).Post(registryDraftsPath+"/{draftId}:publish", handlers.publishDraft)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "publish-policy-bundle"), requireTenantAdminStepUp).Post("/api/v1/admin/policy-bundles:publish", handlers.publishPolicyBundle)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "activate-registry-version"), requireTenantAdminStepUp).Post(registryActivationsPath, handlers.activateVersion)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "retire-registry-version"), requireTenantAdminStepUp).Post(registryVersionsPath+"/{versionId}:retire", handlers.retireVersion)
	return router, nil
}

func (h ConfigAdminHandlers) getPolicyBundles(w http.ResponseWriter, r *http.Request) {
	h.listVersions(w, r, configregistry.KindPolicy)
}

func (h ConfigAdminHandlers) getRecipes(w http.ResponseWriter, r *http.Request) {
	h.listVersions(w, r, configregistry.KindRecipe)
}

func (h ConfigAdminHandlers) getTools(w http.ResponseWriter, r *http.Request) {
	h.listVersions(w, r, configregistry.KindTool)
}

func (h ConfigAdminHandlers) listVersions(w http.ResponseWriter, r *http.Request, kind configregistry.Kind) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeConfigError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return
	}
	versions, err := h.service.ListVersions(r.Context(), request, kind)
	if err != nil {
		writeConfigServiceError(w, err, request.RequestID)
		return
	}
	page, next, err := paginateRegistryVersions(versions, r.URL.Query().Get("cursor"), registryQueryLimit(r))
	if err != nil {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cursor or limit is invalid", false, request.RequestID)
		return
	}
	data := make([]interface{}, 0, len(page))
	for _, value := range page {
		data = append(data, value)
	}
	writeJSON(w, http.StatusOK, api.PageEnvelope{Data: data, Meta: api.PageEnvelope_Meta{NextCursor: next}, RequestId: request.RequestID})
}

func (h ConfigAdminHandlers) createDraft(w http.ResponseWriter, r *http.Request) {
	request, tx, ok := configWriteContext(w, r)
	if !ok {
		return
	}
	var body api.RegistryDraftCreateRequest
	if err := decodeSourceJSON(r, &body); err != nil {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry draft is invalid", false, request.RequestID)
		return
	}
	content, err := json.Marshal(body.Content)
	if err != nil {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry draft content is invalid", false, request.RequestID)
		return
	}
	created, err := h.service.CreateDraft(r.Context(), tx, request, configregistry.DraftCommand{
		Kind: configregistry.Kind(body.Kind), LogicalName: body.LogicalName, Content: content,
	})
	if err != nil {
		writeConfigServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusCreated, api.SuccessEnvelope{Data: created, RequestId: request.RequestID})
}

func (h ConfigAdminHandlers) updateDraft(w http.ResponseWriter, r *http.Request) {
	request, tx, draftID, ok := configDraftPathContext(w, r)
	if !ok {
		return
	}
	var body api.RegistryDraftUpdateRequest
	if err := decodeSourceJSON(r, &body); err != nil || body.ExpectedRevision < 1 {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry draft update is invalid", false, request.RequestID)
		return
	}
	content, err := json.Marshal(body.Content)
	if err != nil {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry draft content is invalid", false, request.RequestID)
		return
	}
	updated, err := h.service.UpdateDraft(r.Context(), tx, request, configregistry.DraftRef{TenantID: request.TenantID, DraftID: draftID}, configregistry.DraftUpdateCommand{
		ExpectedRevision: body.ExpectedRevision, Content: content,
	})
	if err != nil {
		writeConfigServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusOK, api.SuccessEnvelope{Data: updated, RequestId: request.RequestID})
}

func (h ConfigAdminHandlers) publishDraft(w http.ResponseWriter, r *http.Request) {
	request, tx, draftID, ok := configDraftPathContext(w, r)
	if !ok {
		return
	}
	var body api.RegistryPublishRequest
	if err := decodeSourceJSON(r, &body); err != nil || body.ExpectedRevision < 1 || body.SignerKeyId == "" || len(body.Signature) == 0 {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry publication signature is invalid", false, request.RequestID)
		return
	}
	published, err := h.service.Publish(r.Context(), tx, request, configregistry.DraftRef{TenantID: request.TenantID, DraftID: draftID}, body.ExpectedRevision, configregistry.PublicationSignature{
		KeyID: body.SignerKeyId, Signature: body.Signature,
	})
	if err != nil {
		writeConfigServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusCreated, api.SuccessEnvelope{Data: published, RequestId: request.RequestID})
}

func (h ConfigAdminHandlers) publishPolicyBundle(w http.ResponseWriter, r *http.Request) {
	request, tx, ok := configWriteContext(w, r)
	if !ok {
		return
	}
	var body api.PolicyBundlePublishRequest
	if err := decodeSourceJSON(r, &body); err != nil || body.ExpectedRevision < 1 || body.DraftId == "" || body.SignerKeyId == "" || len(body.Signature) == 0 {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "policy publication signature is invalid", false, request.RequestID)
		return
	}
	draftID, err := uuid.Parse(body.DraftId)
	if err != nil {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "policy draft identifier is invalid", false, request.RequestID)
		return
	}
	published, err := h.service.PublishPolicyBundle(r.Context(), tx, request, configregistry.DraftRef{TenantID: request.TenantID, DraftID: draftID}, body.ExpectedRevision, configregistry.PublicationSignature{
		KeyID: body.SignerKeyId, Signature: body.Signature,
	})
	if err != nil {
		writeConfigServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusOK, api.SuccessEnvelope{Data: published, RequestId: request.RequestID})
}

func (h ConfigAdminHandlers) activateVersion(w http.ResponseWriter, r *http.Request) {
	request, tx, ok := configWriteContext(w, r)
	if !ok {
		return
	}
	var body api.RegistryActivationRequest
	if err := decodeSourceJSON(r, &body); err != nil || body.ExpectedRevision < 0 {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry activation is invalid", false, request.RequestID)
		return
	}
	versionID, err := uuid.Parse(body.VersionId)
	if err != nil {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry version identifier is invalid", false, request.RequestID)
		return
	}
	scope := configregistry.Scope{Type: configregistry.ScopeType(body.ScopeType)}
	if body.ClusterId != nil {
		scope.ClusterID, err = uuid.Parse(*body.ClusterId)
		if err != nil {
			writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry activation scope is invalid", false, request.RequestID)
			return
		}
	}
	if body.Namespace != nil {
		scope.Namespace = *body.Namespace
	}
	activation, err := h.service.Activate(r.Context(), tx, request, configregistry.VersionRef{TenantID: request.TenantID, VersionID: versionID}, configregistry.Kind(body.Kind), body.LogicalName, scope, body.ExpectedRevision)
	if err != nil {
		writeConfigServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusOK, api.SuccessEnvelope{Data: activation, RequestId: request.RequestID})
}

func (h ConfigAdminHandlers) retireVersion(w http.ResponseWriter, r *http.Request) {
	request, tx, ok := configWriteContext(w, r)
	if !ok {
		return
	}
	versionID, err := uuid.Parse(chi.URLParam(r, "versionId"))
	if err != nil {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry version identifier is invalid", false, request.RequestID)
		return
	}
	var body api.RegistryVersionRetireRequest
	if err := decodeSourceJSON(r, &body); err != nil {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry version digest is invalid", false, request.RequestID)
		return
	}
	retired, err := h.service.Retire(r.Context(), tx, request, configregistry.VersionRef{TenantID: request.TenantID, VersionID: versionID}, body.ExpectedDigest)
	if err != nil {
		writeConfigServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusOK, api.SuccessEnvelope{Data: retired, RequestId: request.RequestID})
}

func configWriteContext(w http.ResponseWriter, r *http.Request) (auth.RequestContext, pgx.Tx, bool) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeConfigError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return auth.RequestContext{}, nil, false
	}
	tx, ok := TransactionFromContext(r.Context())
	if !ok {
		writeConfigError(w, http.StatusInternalServerError, "INTERNAL", "registry transaction is unavailable", true, request.RequestID)
		return auth.RequestContext{}, nil, false
	}
	return request, tx, true
}

func configDraftPathContext(w http.ResponseWriter, r *http.Request) (auth.RequestContext, pgx.Tx, uuid.UUID, bool) {
	request, tx, ok := configWriteContext(w, r)
	if !ok {
		return auth.RequestContext{}, nil, uuid.Nil, false
	}
	draftID, err := uuid.Parse(chi.URLParam(r, "draftId"))
	if err != nil {
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry draft identifier is invalid", false, request.RequestID)
		return auth.RequestContext{}, nil, uuid.Nil, false
	}
	return request, tx, draftID, true
}

func registryQueryLimit(r *http.Request) int {
	if r.URL.Query().Get("limit") == "" {
		return 100
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 || limit > 200 {
		return 0
	}
	return limit
}

func paginateRegistryVersions(items []configregistry.PublishedVersion, cursor string, limit int) ([]configregistry.PublishedVersion, *string, error) {
	if limit < 1 {
		return nil, nil, errors.New("invalid limit")
	}
	start := 0
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, nil, err
		}
		id, err := uuid.Parse(string(decoded))
		if err != nil {
			return nil, nil, err
		}
		found := false
		for index, item := range items {
			if item.VersionID == id {
				start, found = index+1, true
				break
			}
		}
		if !found {
			return nil, nil, errors.New("cursor is not in this tenant result")
		}
	}
	end := min(start+limit, len(items))
	var next *string
	if end < len(items) && end > start {
		value := base64.RawURLEncoding.EncodeToString([]byte(items[end-1].VersionID.String()))
		next = &value
	}
	return items[start:end], next, nil
}

func writeConfigServiceError(w http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeConfigError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, requestID)
	case errors.Is(err, configregistry.ErrUnauthorized), errors.Is(err, auth.ErrForbidden):
		writeConfigError(w, http.StatusForbidden, "FORBIDDEN", "platform_admin authorization is required", false, requestID)
	case errors.Is(err, configregistry.ErrInvalidInput), errors.Is(err, configregistry.ErrInvalidSignature):
		writeConfigError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "registry input or signature is invalid", false, requestID)
	case errors.Is(err, configregistry.ErrRevisionConflict), errors.Is(err, configregistry.ErrScopeConflict), errors.Is(err, configregistry.ErrStateConflict), errors.Is(err, configregistry.ErrVersionActive):
		writeConfigError(w, http.StatusConflict, "CONFLICT", "registry state or revision conflicts with the request", false, requestID)
	case errors.Is(err, configregistry.ErrImmutable):
		writeConfigError(w, http.StatusConflict, "IMMUTABLE", "published registry content cannot be changed", false, requestID)
	case errors.Is(err, configregistry.ErrNotFound):
		writeConfigError(w, http.StatusNotFound, "NOT_FOUND", "registry resource was not found", false, requestID)
	default:
		writeConfigError(w, http.StatusInternalServerError, "INTERNAL", "registry request failed", true, requestID)
	}
}

func writeConfigError(w http.ResponseWriter, status int, code, message string, retryable bool, requestID string) {
	if requestID == "" {
		requestID = uuid.Must(uuid.NewV7()).String()
	}
	writeJSON(w, status, api.ErrorEnvelope{Code: api.ErrorEnvelopeCode(code), Message: message, RequestId: requestID, Retryable: retryable})
}
