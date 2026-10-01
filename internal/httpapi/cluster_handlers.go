package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"ops-platform/gen/api"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"ops-platform/internal/source"
)

const adminClustersPath = "/api/v1/admin/clusters"

type ClusterAdminHandlers struct {
	service *source.Service
}

func mountClusterAdminRoutes(router chi.Router, service *source.Service, pool persistence.TxBeginner) {
	handlers := ClusterAdminHandlers{service: service}
	router.With(auth.RequireRole(auth.PlatformAdmin)).Get(adminClustersPath, handlers.getClusters)
	router.With(auth.RequireRole(auth.PlatformAdmin), newTenantIdempotency(pool, "create-cluster"), requireTenantAdminStepUp).Post(adminClustersPath, handlers.createCluster)
}

func (h ClusterAdminHandlers) getClusters(w http.ResponseWriter, r *http.Request) {
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeSourceError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
		return
	}
	clusters, err := h.service.ListClusters(r.Context(), request)
	if err != nil {
		writeSourceServiceError(w, err, request.RequestID)
		return
	}
	page, next, err := paginateClusters(clusters, r.URL.Query().Get("cursor"), queryLimit(r))
	if err != nil {
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cursor or limit is invalid", false, request.RequestID)
		return
	}
	data := make([]interface{}, 0, len(page))
	for _, item := range page {
		data = append(data, clusterRegistrationJSON(item))
	}
	writeJSON(w, http.StatusOK, api.PageEnvelope{Data: data, Meta: api.PageEnvelope_Meta{NextCursor: next}, RequestId: request.RequestID})
}

func (h ClusterAdminHandlers) createCluster(w http.ResponseWriter, r *http.Request) {
	request, tx, ok := sourceWriteContext(w, r)
	if !ok {
		return
	}
	var body api.ClusterRegistrationRequest
	if err := decodeSourceJSON(r, &body); err != nil {
		writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cluster registration is invalid", false, request.RequestID)
		return
	}
	var expectedRevision int64
	if body.ExpectedRevision != nil {
		expectedRevision = *body.ExpectedRevision
		if expectedRevision < 1 {
			writeSourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cluster revision is invalid", false, request.RequestID)
			return
		}
	}
	created, err := h.service.RegisterCluster(r.Context(), tx, request, source.ClusterCommand{ExpectedRevision: expectedRevision, ClusterUID: body.ClusterUid, DisplayName: body.DisplayName, APIEndpointRef: body.ApiEndpointRef, Distribution: body.Distribution, ActualVersions: body.ActualVersions, Capabilities: body.Capabilities})
	if err != nil {
		writeSourceServiceError(w, err, request.RequestID)
		return
	}
	writeJSON(w, http.StatusCreated, api.SuccessEnvelope{Data: clusterRegistrationJSON(created), RequestId: request.RequestID})
}

func clusterRegistrationJSON(value source.ClusterRegistration) map[string]any {
	return map[string]any{
		"tenantId": value.TenantID, "clusterId": value.ClusterID, "clusterUid": value.ClusterUID,
		"displayName": value.DisplayName, "status": value.Status, "revision": value.Revision,
		"apiEndpointRef": value.APIEndpointRef, "distribution": value.Distribution, "actualVersions": value.ActualVersions, "capabilities": value.Capabilities,
		"createdAt": value.CreatedAt, "updatedAt": value.UpdatedAt,
	}
}

func paginateClusters(items []source.ClusterRegistration, cursor string, limit int) ([]source.ClusterRegistration, *string, error) {
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
			if item.ClusterID == id {
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
		value := encodePageCursor(items[end-1].ClusterID)
		next = &value
	}
	return items[start:end], next, nil
}
