package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"strings"
)

func (h *SP05Handlers) serveMutation(w http.ResponseWriter, r *http.Request) {
	originalPath := r.URL.Path
	raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || !json.Valid(raw) {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "invalid incident mutation", false, "")
		return
	}
	request := r.Clone(r.Context())
	urlCopy := *r.URL
	request.URL = &urlCopy
	request.Body = io.NopCloser(bytes.NewReader(raw))
	ids := []string{}
	if originalPath == "/api/v1/incidents:merge" {
		var body struct {
			Source string `json:"sourceIncidentId"`
			Target string `json:"targetIncidentId"`
		}
		if json.Unmarshal(raw, &body) != nil {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "invalid merge", false, "")
			return
		}
		ids = []string{body.Source, body.Target}
		request.URL.Path = "/api/v1/incidents/" + body.Source + "/merge"
	} else {
		for _, action := range []string{"transition", "split"} {
			suffix := ":" + action
			if strings.HasSuffix(originalPath, suffix) {
				request.URL.Path = strings.TrimSuffix(originalPath, suffix) + "/" + action
			}
		}
		parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/v1/incidents/"), "/")
		if len(parts) != 2 || (parts[1] != "transition" && parts[1] != "split") {
			writeTenantError(w, 404, "NOT_FOUND", "incident action unavailable", false, "")
			return
		}
		ids = []string{parts[0]}
	}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "invalid incident identity", false, "")
			return
		}
	}
	authorize := func(r *http.Request, pool persistence.TxBeginner) error {
		actor, ok := auth.RequestContextFromContext(r.Context())
		if !ok {
			return auth.ErrUnauthenticated
		}
		return persistence.WithTenantTx(r.Context(), pool, actor.TenantID, func(tx pgx.Tx) error {
			for _, id := range ids {
				item, err := incident.Load(r.Context(), tx, actor.TenantID, id, false)
				if err != nil {
					return err
				}
				scope, err := (graph.Authorization{Pool: tx}).Effective(r.Context(), actor.TenantID.String(), actor.Subject, item.ClusterUID)
				if err != nil {
					return graph.ErrScope
				}
				if err := incident.AuthorizeAll(r.Context(), tx, scope, actor.Subject, item); err != nil {
					return err
				}
			}
			return nil
		})
	}
	middleware := IdempotencyMiddleware{Pool: h.Pool, Resolve: func(r *http.Request) (persistence.Scope, error) {
		a, ok := auth.RequestContextFromContext(r.Context())
		if !ok {
			return persistence.Scope{}, auth.ErrUnauthenticated
		}
		return persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: originalPath}, nil
	}, Authorize: func(r *http.Request, _ persistence.Scope) error { return authorize(r, h.Pool) }, AuthorizeTx: func(r *http.Request, tx pgx.Tx, _ persistence.Scope) error { return authorize(r, tx) }}
	middleware.Wrap(http.HandlerFunc(h.ServeHTTP)).ServeHTTP(w, request)
}
