package httpapi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
)

func (h *SP05Handlers) caseDraftScope(r *http.Request, pool persistence.TxBeginner, origin string) (graph.Scope, error) {
	actor, ok := auth.RequestContextFromContext(r.Context())
	if !ok || !auth.HasRole(r.Context(), auth.Operator) {
		return graph.Scope{}, graph.ErrScope
	}
	var scope graph.Scope
	err := persistence.WithTenantTx(r.Context(), pool, actor.TenantID, func(tx pgx.Tx) error {
		i, err := incident.Load(r.Context(), tx, actor.TenantID, origin, false)
		if err != nil {
			return err
		}
		scope, err = (graph.Authorization{Pool: tx}).Effective(r.Context(), actor.TenantID.String(), actor.Subject, i.ClusterUID)
		if err != nil {
			return graph.ErrScope
		}
		return incident.AuthorizeAll(r.Context(), tx, scope, actor.Subject, i)
	})
	return scope, err
}

func (h *SP05Handlers) serveCaseDrafts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")
	actor, ok := auth.RequestContextFromContext(r.Context())
	if !ok || !auth.HasRole(r.Context(), auth.Operator) {
		writeSP04Error(w, 403, "FORBIDDEN", "operator access required", false, actor.RequestID)
		return
	}
	fail := func(err error) {
		status, code := 503, "SOURCE_DEGRADED"
		switch {
		case errors.Is(err, graph.ErrScope), errors.Is(err, finding.ErrUnauthorized):
			status, code = 403, "FORBIDDEN"
		case errors.Is(err, pgx.ErrNoRows):
			status, code = 404, "NOT_FOUND"
		case errors.Is(err, incident.ErrRevision):
			status, code = 409, "STALE_CONTEXT"
		case errors.Is(err, finding.ErrInvalid):
			status, code = 400, "INVALID_ARGUMENT"
		}
		writeSP04Error(w, status, code, "case draft request failed", status == 503, actor.RequestID)
	}
	if !h.Enabled {
		writeSP04Error(w, 409, "CAPABILITY_DISABLED", "incident runtime disabled", false, actor.RequestID)
		return
	}
	const collection = "/api/v1/knowledge/documents"
	if r.Method == http.MethodPost && r.URL.Path == collection {
		raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
		var input incident.CaseDraftInput
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err != nil || len(raw) > 64<<10 || decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.Validate() != nil {
			fail(finding.ErrInvalid)
			return
		}
		if _, inside := TransactionFromContext(r.Context()); !inside {
			request := r.Clone(r.Context())
			request.Body = io.NopCloser(bytes.NewReader(raw))
			authorize := func(r *http.Request, pool persistence.TxBeginner) error {
				_, err := h.caseDraftScope(r, pool, input.IncidentID)
				return err
			}
			middleware := IdempotencyMiddleware{Pool: h.Pool, Resolve: func(r *http.Request) (persistence.Scope, error) {
				return persistence.Scope{TenantID: actor.TenantID, Subject: actor.Subject, Operation: collection}, nil
			}, Authorize: func(r *http.Request, _ persistence.Scope) error { return authorize(r, h.Pool) }, AuthorizeTx: func(r *http.Request, tx pgx.Tx, _ persistence.Scope) error { return authorize(r, tx) }}
			middleware.Wrap(http.HandlerFunc(h.serveCaseDrafts)).ServeHTTP(w, request)
			return
		}
		tx, _ := TransactionFromContext(r.Context())
		scope, err := h.caseDraftScope(r, tx, input.IncidentID)
		if err != nil {
			fail(err)
			return
		}
		draft, err := (incident.Service{Pool: tx}).CreateCaseDraft(r.Context(), scope, actor.Subject, input)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 201, map[string]any{"data": draft, "requestId": actor.RequestID})
		return
	}
	if r.Method != http.MethodGet {
		fail(pgx.ErrNoRows)
		return
	}
	origin := r.URL.Query().Get("incidentId")
	var id, after uuid.UUID
	var err error
	if r.URL.Path != collection {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, collection+"/"), "/")
		if len(parts) != 3 || parts[1] != "versions" || parts[2] != "1" {
			fail(pgx.ErrNoRows)
			return
		}
		id, err = uuid.Parse(parts[0])
		if err != nil {
			fail(finding.ErrInvalid)
			return
		}
		err = persistence.WithTenantTx(r.Context(), h.Pool, actor.TenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT incident_id FROM incident.case_drafts WHERE tenant_id=$1 AND document_id=$2`, actor.TenantID, id).Scan(&origin)
		})
		if err != nil {
			fail(err)
			return
		}
	}
	if _, err = uuid.Parse(origin); err != nil {
		fail(finding.ErrInvalid)
		return
	}
	scope, err := h.caseDraftScope(r, h.Pool, origin)
	if err != nil {
		fail(err)
		return
	}
	queryDigest := finding.Hash([]string{r.URL.Path, origin})
	cursorScope := graph.ScopeDigest([]any{actor.TenantID.String(), actor.Subject, scope})
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		var bound graphCursor
		if id != uuid.Nil || h.GraphAPI == nil || len(h.GraphAPI.SigningKey) != ed25519.PrivateKeySize ||
			decodeCursor(h.GraphAPI.SigningKey.Public().(ed25519.PublicKey), cursor, &bound) != nil || bound.ScopeDigest != cursorScope || bound.QueryDigest != queryDigest {
			fail(finding.ErrInvalid)
			return
		}
		after, err = uuid.Parse(bound.After)
		if err != nil {
			fail(finding.ErrInvalid)
			return
		}
	}
	items, err := (incident.Service{Pool: h.Pool}).ReadCaseDrafts(r.Context(), scope, actor.Subject, origin, id, after)
	if err != nil {
		fail(err)
		return
	}
	if id != uuid.Nil {
		if len(items) != 1 {
			fail(pgx.ErrNoRows)
			return
		}
		writeJSON(w, 200, map[string]any{"data": items[0], "requestId": actor.RequestID})
		return
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		if h.GraphAPI == nil || len(h.GraphAPI.SigningKey) != ed25519.PrivateKeySize {
			fail(graph.ErrNotReady)
			return
		}
		next, err = encodeCursor(h.GraphAPI.SigningKey, graphCursor{ScopeDigest: cursorScope, QueryDigest: queryDigest, After: items[49].DocumentID.String()})
		if err != nil {
			fail(err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"data": items, "meta": map[string]any{"nextCursor": next}, "requestId": actor.RequestID})
}
