package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/gen/api"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"ops-platform/internal/source"
)

const adminSourcesPath = "/api/v1/admin/source-registrations"

type SourceAdminHandlers struct {
	service *source.Service
}

func NewSourceAdminRouter(service *source.Service, pool persistence.TxBeginner) (chi.Router, error) {
	if service == nil || pool == nil {
		return nil, errors.New("source service and idempotency pool are required")
	}
	handlers := SourceAdminHandlers{service: service}
	router := chi.NewRouter()
	router.With(auth.RequireRole(auth.PlatformAdmin)).Get(adminSourcesPath, handlers.getSources)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "create-source-registration"), requireTenantAdminStepUp).Post(adminSourcesPath, handlers.createSource)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "update-source-registration"), requireTenantAdminStepUp).Patch(adminSourcesPath+"/{sourceId}", handlers.updateSource)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "rotate-source-credential"), requireTenantAdminStepUp).Post(adminSourcesPath+"/{sourceId}/rotate-credential", handlers.rotateSourceCredential)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "rollback-source-registration"), requireTenantAdminStepUp).Post(adminSourcesPath+"/{sourceId}/rollback", handlers.rollbackSourceRegistration)
	mountClusterAdminRoutes(router, service, pool)
	return router, nil
}

func (h SourceAdminHandlers) getSources(w http.ResponseWriter, r *http.Request) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeSourceError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return
	}
	sources, err := h.service.ListSources(r.Context(), request)
	if err != nil {
		writeSourceServiceError(w, err, request.RequestID)
		return
	}
	page, next, err := paginateSources(sources, r.URL.Query().Get("cursor"), queryLimit(r))
	if err != nil {
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cursor or limit is invalid", false, request.RequestID)
		return
	}
	data := make([]interface{}, 0, len(page))
	for _, item := range page {
		data = append(data, sourceRegistrationJSON(item))
	}
	writeJSON(w, http.StatusOK, api.PageEnvelope{Data: data, Meta: api.PageEnvelope_Meta{NextCursor: next}, RequestId: request.RequestID})
}

func (h SourceAdminHandlers) createSource(w http.ResponseWriter, r *http.Request) {
	request, tx, ok := sourceWriteContext(w, r)
	if !ok {
		return
	}
	var body api.SourceRegistrationRequest
	if err := decodeSourceJSON(r, &body); err != nil {
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "source registration is invalid", false, request.RequestID)
		return
	}
	var clusterID *uuid.UUID
	if body.ClusterId != nil {
		parsed, err := uuid.Parse(*body.ClusterId)
		if err != nil {
			writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "source registration is invalid", false, request.RequestID)
			return
		}
		clusterID = &parsed
	}
	created, err := h.service.Register(r.Context(), tx, request, source.RegisterCommand{
		SourceType: string(body.SourceType), InstanceKey: body.InstanceKey, ClusterID: clusterID, AuthRef: body.AuthRef,
	})
	if err != nil {
		writeSourceServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusCreated, api.SuccessEnvelope{Data: sourceRegistrationJSON(created), RequestId: request.RequestID})
}

func (h SourceAdminHandlers) updateSource(w http.ResponseWriter, r *http.Request) {
	request, tx, sourceID, ok := sourceWritePathContext(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64           `json:"expectedRevision"`
		ClusterID        json.RawMessage `json:"clusterId"`
		Status           *string         `json:"status"`
	}
	present, err := decodeSourceObject(r, &body)
	if err != nil || body.ExpectedRevision < 1 {
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "source registration update is invalid", false, request.RequestID)
		return
	}
	command := source.SourceUpdateCommand{ExpectedRevision: body.ExpectedRevision}
	if raw, exists := present["clusterId"]; exists {
		command.ClusterIDSet = true
		if string(raw) != "null" {
			var value string
			if json.Unmarshal(raw, &value) != nil {
				writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "source registration update is invalid", false, request.RequestID)
				return
			}
			parsed, parseErr := uuid.Parse(value)
			if parseErr != nil {
				writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "source registration update is invalid", false, request.RequestID)
				return
			}
			command.ClusterID = &parsed
		}
	}
	if raw, exists := present["status"]; exists && string(raw) == "null" {
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "source registration update is invalid", false, request.RequestID)
		return
	}
	if body.Status != nil {
		command.Status = *body.Status
	}
	updated, err := h.service.UpdateRegistration(r.Context(), tx, request, sourceID, command)
	if err != nil {
		writeSourceServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusOK, api.SuccessEnvelope{Data: sourceRegistrationJSON(updated), RequestId: request.RequestID})
}

func (h SourceAdminHandlers) rotateSourceCredential(w http.ResponseWriter, r *http.Request) {
	request, tx, sourceID, ok := sourceWritePathContext(w, r)
	if !ok {
		return
	}
	var body api.SourceCredentialRotationRequest
	if err := decodeSourceJSON(r, &body); err != nil || body.ExpectedRevision < 1 {
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "credential rotation is invalid", false, request.RequestID)
		return
	}
	updated, err := h.service.RotateCredential(r.Context(), tx, request, sourceID, source.CredentialRotationCommand{
		ExpectedRevision: body.ExpectedRevision, AuthRef: body.AuthRef,
	})
	if err != nil {
		writeSourceServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusOK, api.SuccessEnvelope{Data: sourceRegistrationJSON(updated), RequestId: request.RequestID})
}

func (h SourceAdminHandlers) rollbackSourceRegistration(w http.ResponseWriter, r *http.Request) {
	request, tx, sourceID, ok := sourceWritePathContext(w, r)
	if !ok {
		return
	}
	var body api.SourceRegistrationRollbackRequest
	if err := decodeSourceJSON(r, &body); err != nil || body.ExpectedRevision < 1 || body.TargetRevision < 1 {
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "source registration rollback is invalid", false, request.RequestID)
		return
	}
	rolledBack, err := h.service.RollbackRegistration(r.Context(), tx, request, sourceID, source.SourceRegistrationRollbackCommand{
		ExpectedRevision: body.ExpectedRevision, TargetRevision: body.TargetRevision,
	})
	if err != nil {
		writeSourceServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusOK, api.SuccessEnvelope{Data: sourceRegistrationJSON(rolledBack), RequestId: request.RequestID})
}

func sourceWriteContext(w http.ResponseWriter, r *http.Request) (auth.RequestContext, pgx.Tx, bool) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeSourceError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return auth.RequestContext{}, nil, false
	}
	tx, ok := TransactionFromContext(r.Context())
	if !ok {
		writeSourceError(w, http.StatusInternalServerError, "INTERNAL", "source transaction is unavailable", true, request.RequestID)
		return auth.RequestContext{}, nil, false
	}
	return request, tx, true
}

func sourceWritePathContext(w http.ResponseWriter, r *http.Request) (auth.RequestContext, pgx.Tx, uuid.UUID, bool) {
	request, tx, ok := sourceWriteContext(w, r)
	if !ok {
		return auth.RequestContext{}, nil, uuid.Nil, false
	}
	sourceID, err := uuid.Parse(chi.URLParam(r, "sourceId"))
	if err != nil {
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "source identifier is invalid", false, request.RequestID)
		return auth.RequestContext{}, nil, uuid.Nil, false
	}
	return request, tx, sourceID, true
}

func decodeSourceJSON(r *http.Request, target any) error {
	_, err := decodeSourceObject(r, target)
	return err
}

func decodeSourceObject(r *http.Request, target any) (map[string]json.RawMessage, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return nil, errors.New("request body must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("request body contains trailing JSON")
	}
	return object, nil
}

func sourceRegistrationJSON(value source.SourceRegistration) map[string]any {
	var clusterID any
	if value.ClusterID != uuid.Nil {
		clusterID = value.ClusterID
	}
	return map[string]any{
		"tenantId": value.TenantID, "sourceId": value.SourceID, "sourceType": value.SourceType,
		"instanceKey": value.InstanceKey, "clusterId": clusterID, "clusterUid": value.ClusterUID,
		"authRef": value.AuthRef, "credentialRevision": value.CredentialRevision,
		"status": value.Status, "revision": value.Revision, "createdAt": value.CreatedAt, "updatedAt": value.UpdatedAt,
	}
}

func queryLimit(r *http.Request) int {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return 100
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 200 {
		return 0
	}
	return limit
}

func paginateSources(items []source.SourceRegistration, cursor string, limit int) ([]source.SourceRegistration, *string, error) {
	if limit < 1 {
		return nil, nil, errors.New("invalid limit")
	}
	start := 0
	if cursor != "" {
		id, err := decodePageCursor(cursor)
		if err != nil {
			return nil, nil, err
		}
		found := false
		for index, item := range items {
			if item.SourceID == id {
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
		value := encodePageCursor(items[end-1].SourceID)
		next = &value
	}
	return items[start:end], next, nil
}

func encodePageCursor(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id.String()))
}

func decodePageCursor(value string) (uuid.UUID, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(string(decoded))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("invalid cursor")
	}
	return id, nil
}

func writeSourceServiceError(w http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeSourceError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, requestID)
	case errors.Is(err, source.ErrUnauthorized), errors.Is(err, auth.ErrForbidden):
		writeSourceError(w, http.StatusForbidden, "FORBIDDEN", "platform_admin authorization is required", false, requestID)
	case errors.Is(err, source.ErrInvalidInput):
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "source or cluster registration input is invalid", false, requestID)
	case errors.Is(err, source.ErrRevisionConflict), errors.Is(err, source.ErrIdentityConflict):
		writeSourceError(w, http.StatusConflict, "CONFLICT", "the requested identity or revision conflicts with current state", false, requestID)
	case errors.Is(err, source.ErrResourceNotFound), errors.Is(err, pgx.ErrNoRows):
		writeSourceError(w, http.StatusNotFound, "NOT_FOUND", "source or cluster registration was not found", false, requestID)
	default:
		writeSourceError(w, http.StatusInternalServerError, "INTERNAL", "source registration request failed", true, requestID)
	}
}

func writeSourceError(w http.ResponseWriter, status int, code, message string, retryable bool, requestID string) {
	if requestID == "" {
		requestID = uuid.Must(uuid.NewV7()).String()
	}
	writeJSON(w, status, api.ErrorEnvelope{Code: api.ErrorEnvelopeCode(code), Message: message, RequestId: requestID, Retryable: retryable})
}
