package httpapi

import (
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"strconv"
	"strings"
	"time"
)

// Manual queries use the existing mTLS/owner routing and native session proof;
// no synthetic conversation, Job or legacy role is created.
func (h *SP04Handlers) serveObservation(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.RequestContextFromContext(r.Context())
	fail := func(status int, code string) {
		writeSP04Error(w, status, code, "观测查询不可用", status >= 500, actor.RequestID)
	}
	proof := nativeProofFromContext(r.Context())
	if proof == nil {
		fail(403, "FORBIDDEN")
		return
	}
	cluster := r.URL.Query().Get("clusterUid")
	scope, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), actor.TenantID.String(), actor.Subject, cluster)
	if err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	body := map[string]any{}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/observability/")
	switch {
	case path == "capabilities" && r.Method == "GET":
		body["action"] = "capabilities"
		body["canonicalId"] = r.URL.Query().Get("canonicalId")
	case path == "queries" && r.Method == "GET":
		body["action"] = "list"
		limit := 20
		if text := r.URL.Query().Get("limit"); text != "" {
			value, e := strconv.Atoi(text)
			if e != nil || value < 1 || value > 50 {
				fail(400, "INVALID_ARGUMENT")
				return
			}
			limit = value
		}
		body["limit"] = limit
		if text := r.URL.Query().Get("cursor"); text != "" {
			cursor, e := uuid.Parse(text)
			if e != nil || cursor == uuid.Nil {
				fail(400, "INVALID_ARGUMENT")
				return
			}
			body["cursor"] = cursor
		}
	case path == "queries" && r.Method == "POST":
		raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
		if err != nil || len(raw) > 64<<10 || !json.Valid(raw) {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		key, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
		if err != nil {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		body["requestKey"] = key
		body["action"] = "create"
		body["request"] = json.RawMessage(raw)
	default:
		rest := strings.TrimPrefix(path, "queries/")
		idText := strings.TrimSuffix(strings.TrimSuffix(rest, "/results"), ":cancel")
		id, err := uuid.Parse(idText)
		if err != nil {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		body["queryId"] = id
		if rest == idText && r.Method == "GET" {
			body["action"] = "get"
		} else if rest == idText+"/results" && r.Method == "GET" {
			body["action"] = "results"
		} else if rest == idText+":cancel" && r.Method == "POST" {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
			if err != nil || len(raw) > 4096 || !json.Valid(raw) {
				fail(400, "INVALID_ARGUMENT")
				return
			}
			body["action"] = "cancel"
			body["cancel"] = json.RawMessage(raw)
		} else {
			fail(405, "INVALID_ARGUMENT")
			return
		}
	}
	owner, err := (graph.Repository{Pool: h.Pool}).Load(r.Context(), scope.Tenant, cluster)
	if err != nil || !owner.ExpiresAt.After(time.Now()) || h.EndpointAllowed == nil || !h.EndpointAllowed(owner.Endpoint) || h.Client == nil {
		fail(503, "GRAPH_NOT_READY")
		return
	}
	body["ownerEpoch"] = owner.Epoch
	raw, _ := json.Marshal(body)
	result, err := h.callWithGrantDigest(r.Context(), owner.Endpoint, "/internal/v1/observability:request", "platform-observability-query", actor.Subject, scope, raw, "", proof)
	if err != nil {
		h.writeCallError(w, err, actor.RequestID)
		return
	}
	var data any
	if json.Unmarshal(result, &data) != nil {
		fail(503, "GRAPH_NOT_READY")
		return
	}
	status := 200
	if body["action"] == "create" {
		status = 202
	}
	envelope := map[string]any{"requestId": actor.RequestID, "data": data}
	if body["action"] == "list" {
		var page struct {
			Items      json.RawMessage `json:"items"`
			NextCursor *uuid.UUID      `json:"nextCursor"`
		}
		if json.Unmarshal(result, &page) != nil || page.Items == nil {
			fail(503, "GRAPH_NOT_READY")
			return
		}
		envelope["data"], envelope["nextCursor"] = page.Items, page.NextCursor
	}
	writeJSON(w, status, envelope)
}
