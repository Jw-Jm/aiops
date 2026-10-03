package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"ops-platform/internal/rca"
	"ops-platform/internal/source"
	"strconv"
	"strings"
)

type IngestionBinding struct {
	Tenant, Subject, SourceID                string
	RegistrationRevision, CredentialRevision int64
}
type SP05Handlers struct {
	GraphAPI *SP04Handlers
	Pool     persistence.TxBeginner
	Enabled  bool
	Bindings []IngestionBinding
}

func (h *SP05Handlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")
	request, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeSP04Error(w, 401, "UNAUTHENTICATED", "verified identity required", false, "")
		return
	}
	fail := func(err error) {
		code, status := "SOURCE_DEGRADED", 503
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			code, status = "NOT_FOUND", 404
		case errors.Is(err, graph.ErrScope), errors.Is(err, finding.ErrUnauthorized):
			code, status = "FORBIDDEN", 403
		case errors.Is(err, finding.ErrConflict):
			code, status = "IDEMPOTENCY_CONFLICT", 409
		case errors.Is(err, incident.ErrRevision), errors.Is(err, rca.ErrStale):
			code, status = "STALE_CONTEXT", 409
		case errors.Is(err, incident.ErrTransition), errors.Is(err, finding.ErrInvalid):
			code, status = "INVALID_ARGUMENT", 400
		}
		writeSP04Error(w, status, code, "SP05 request failed", status == 503, request.RequestID)
	}
	if !h.Enabled {
		writeSP04Error(w, 409, "CAPABILITY_DISABLED", "SP05 runtime is disabled", false, request.RequestID)
		return
	}
	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/v1/incidents") {
		if _, ok := TransactionFromContext(r.Context()); !ok {
			h.serveMutation(w, r)
			return
		}
	}
	if r.URL.Path == "/api/v1/findings:ingest" && r.Method == "POST" {
		writeSP04Error(w, 409, "CAPABILITY_DISABLED", "historical ingestion contract is unverified; explicitly bound publishers require finding-envelope/v2 and /api/v2/findings:ingest", false, request.RequestID)
		return
	}
	if r.URL.Path == "/api/v2/findings:ingest" && r.Method == "POST" {
		var b *IngestionBinding
		for index := range h.Bindings {
			v := &h.Bindings[index]
			if v.Tenant == request.TenantID.String() && v.Subject == request.Subject {
				if b != nil {
					fail(finding.ErrUnauthorized)
					return
				}
				b = v
			}
		}
		if b == nil {
			fail(finding.ErrUnauthorized)
			return
		}
		var e finding.Envelope
		if decodeSP05(r, &e) != nil || r.Header.Get("Idempotency-Key") == "" || r.Header.Get("Idempotency-Key") != e.IdempotencyKey {
			fail(finding.ErrInvalid)
			return
		}
		bound := source.BoundSourceContext{TenantID: request.TenantID, SourceID: uuid.MustParse(b.SourceID), RegistrationRevision: b.RegistrationRevision, CredentialRevision: b.CredentialRevision}
		err := persistence.WithTenantTx(r.Context(), h.Pool, request.TenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT s.cluster_id,c.cluster_uid FROM platform.source_registrations s JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE s.tenant_id=$1 AND s.source_id=$2`, request.TenantID, b.SourceID).Scan(&bound.ClusterID, &bound.ClusterUID)
		})
		if err != nil {
			fail(err)
			return
		}
		scope, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), request.TenantID.String(), request.Subject, bound.ClusterUID)
		if err != nil || !scope.Allows(e.ResourceCanonicalID, e.Namespace) {
			fail(graph.ErrScope)
			return
		}
		f, d, err := (finding.Service{Pool: h.Pool}).Ingest(r.Context(), bound, e)
		if err != nil {
			fail(err)
			return
		}
		current, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), scope.Tenant, request.Subject, scope.Cluster)
		if err != nil || graph.ScopeDigest(scope) != graph.ScopeDigest(current) {
			fail(graph.ErrScope)
			return
		}
		writeJSON(w, 200, map[string]any{"data": map[string]any{"finding": f, "disposition": d}, "requestId": request.RequestID})
		return
	}
	if r.Method == "GET" {
		data, next, err := h.read(r.Context(), request, r)
		if err != nil {
			fail(err)
			return
		}
		if next != "" {
			writeJSON(w, 200, map[string]any{"data": data, "meta": map[string]any{"nextCursor": next}, "requestId": request.RequestID})
		} else {
			writeJSON(w, 200, map[string]any{"data": data, "meta": map[string]any{}, "requestId": request.RequestID})
		}
		return
	}
	// Mutation replay must still execute current authorization. The service CAS
	// and timeline are business semantics; the common ledger stores transport replay.
	path := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/incidents/"), "/")
	if len(path) != 2 || path[0] == "" || r.Method != "POST" {
		fail(pgx.ErrNoRows)
		return
	}
	var i incident.Incident
	err := persistence.WithTenantTx(r.Context(), h.Pool, request.TenantID, func(tx pgx.Tx) error {
		var err error
		i, err = incident.Load(r.Context(), tx, request.TenantID, path[0], false)
		return err
	})
	if err != nil {
		fail(err)
		return
	}
	scope, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), request.TenantID.String(), request.Subject, i.ClusterUID)
	if err != nil || !scope.Allows(i.ResourceCanonicalID, i.Namespace) {
		fail(graph.ErrScope)
		return
	}
	mutationPool := h.Pool
	if tx, ok := TransactionFromContext(r.Context()); ok {
		mutationPool = tx
	}
	service := incident.Service{Pool: mutationPool}
	var out incident.Incident
	switch path[1] {
	case "transition":
		var c incident.Change
		if decodeSP05(r, &c) != nil {
			fail(finding.ErrInvalid)
			return
		}
		out, err = service.Change(r.Context(), scope, request.Subject, i.IncidentID, c)
	case "merge":
		var c struct {
			SourceID         string `json:"sourceIncidentId"`
			TargetID         string `json:"targetIncidentId"`
			TargetRevision   int64  `json:"targetRevision"`
			ExpectedRevision int64  `json:"expectedRevision"`
			Reason           string `json:"reason"`
		}
		if decodeSP05(r, &c) != nil {
			fail(finding.ErrInvalid)
			return
		}
		out, err = service.Merge(r.Context(), scope, request.Subject, c.TargetID, i.IncidentID, c.Reason, c.TargetRevision, c.ExpectedRevision)
	case "split":
		var c struct {
			ExpectedRevision int64    `json:"expectedRevision"`
			FindingIDs       []string `json:"findingIds"`
			Reason           string   `json:"reason"`
		}
		if decodeSP05(r, &c) != nil {
			fail(finding.ErrInvalid)
			return
		}
		out, err = service.Split(r.Context(), scope, request.Subject, i.IncidentID, c.Reason, c.ExpectedRevision, c.FindingIDs)
	default:
		err = pgx.ErrNoRows
	}
	if err != nil {
		fail(err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out, "requestId": request.RequestID})
}
func decodeSP05(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, (64<<10)+1))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return finding.ErrInvalid
	}
	return nil
}

type sp05Cursor struct {
	After       string `json:"after"`
	ScopeDigest string `json:"scopeDigest"`
	Query       string `json:"query"`
}

func (h *SP05Handlers) read(ctx context.Context, actor auth.RequestContext, r *http.Request) (data any, next string, readErr error) {
	path := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")
	if len(path) == 0 || (path[0] != "findings" && path[0] != "incidents") {
		return nil, "", pgx.ErrNoRows
	}
	cluster := r.URL.Query().Get("clusterUid")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return nil, "", finding.ErrInvalid
		}
		limit = n
	}
	var incidentItem incident.Incident
	var f finding.Finding
	if len(path) > 1 {
		if _, err := uuid.Parse(path[1]); err != nil {
			return nil, "", finding.ErrInvalid
		}
		err := persistence.WithTenantTx(ctx, h.Pool, actor.TenantID, func(tx pgx.Tx) error {
			var err error
			if path[0] == "findings" {
				f, err = finding.Load(ctx, tx, actor.TenantID, path[1])
				cluster = f.ClusterUID
			} else {
				incidentItem, err = incident.Load(ctx, tx, actor.TenantID, path[1], false)
				cluster = incidentItem.ClusterUID
			}
			return err
		})
		if err != nil {
			return nil, "", err
		}
	}
	scope, err := (graph.Authorization{Pool: h.Pool}).Effective(ctx, actor.TenantID.String(), actor.Subject, cluster)
	if err != nil {
		return nil, "", graph.ErrScope
	}
	defer func() {
		if readErr == nil {
			current, err := (graph.Authorization{Pool: h.Pool}).Effective(ctx, scope.Tenant, actor.Subject, scope.Cluster)
			if err != nil || graph.ScopeDigest(current) != graph.ScopeDigest(scope) {
				data = nil
				next = ""
				readErr = graph.ErrScope
			}
		}
	}()
	if len(path) > 1 {
		if path[0] == "findings" {
			if len(path) != 2 {
				return nil, "", pgx.ErrNoRows
			}
			if !scope.Allows(f.ResourceCanonicalID, f.Namespace) {
				return nil, "", graph.ErrScope
			}
			return f, "", nil
		}
		if !scope.Allows(incidentItem.ResourceCanonicalID, incidentItem.Namespace) {
			return nil, "", graph.ErrScope
		}
		if err := h.authorizeIncidentRead(ctx, actor, scope, incidentItem); err != nil {
			return nil, "", err
		}
		if len(path) == 2 {
			return incidentItem, "", nil
		}
		if len(path) == 3 && (path[2] == "timeline" || path[2] == "evidence") {
			return h.incidentPage(ctx, actor, scope, incidentItem, r, path[2], limit)
		}
		if len(path) == 4 && path[2] == "rca" && path[3] == "revisions" {
			return h.incidentPage(ctx, actor, scope, incidentItem, r, "rca/revisions", limit)
		}
		if path[2] == "rca" {
			if len(path) != 3 && !(len(path) == 5 && path[3] == "revisions") {
				return nil, "", pgx.ErrNoRows
			}
			revision := incidentItem.CurrentRCARevision
			if len(path) == 5 && path[3] == "revisions" {
				revision, err = strconv.ParseInt(path[4], 10, 64)
				if err != nil || revision < 1 {
					return nil, "", finding.ErrInvalid
				}
			}
			if revision == 0 {
				return nil, "", pgx.ErrNoRows
			}
			rev, err := (rca.Repository{Pool: h.Pool}).Read(ctx, actor.TenantID, incidentItem.IncidentID, revision)
			if err != nil {
				return nil, "", err
			}
			if err := h.authorizeRCARead(ctx, actor, scope, rev); err != nil {
				return nil, "", err
			}
			if len(path) == 3 {
				view, err := h.currentRCAView(ctx, actor, scope, incidentItem, rev)
				return view, "", err
			}
			return rev, "", nil
		}

		return nil, "", pgx.ErrNoRows
	}
	query := r.URL.Query()
	query.Del("cursor")
	digest := graph.ScopeDigest(scope)
	queryHash := finding.Hash([]string{path[0], query.Encode()})
	after := ""
	if token := r.URL.Query().Get("cursor"); token != "" {
		raw, err := base64.RawURLEncoding.DecodeString(token)
		var cursor sp05Cursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.ScopeDigest != digest || cursor.Query != queryHash {
			return nil, "", rca.ErrStale
		}
		after = cursor.After
	}
	items := []json.RawMessage{}
	ids := []string{}
	err = persistence.WithTenantTx(ctx, h.Pool, actor.TenantID, func(tx pgx.Tx) error {
		sql, args, err := sp05ListSQL(path[0], actor.TenantID, cluster, after, scope, limit, r)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, sql, args...)

		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				return err
			}
			items = append(items, json.RawMessage(raw))
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	current, err := (graph.Authorization{Pool: h.Pool}).Effective(ctx, scope.Tenant, actor.Subject, scope.Cluster)
	if err != nil || graph.ScopeDigest(current) != digest {
		return nil, "", rca.ErrStale
	}
	next = ""
	if len(items) > limit {
		raw, _ := json.Marshal(sp05Cursor{After: ids[limit-1], ScopeDigest: digest, Query: queryHash})
		next = base64.RawURLEncoding.EncodeToString(raw)
		items = items[:limit]
	}
	return items, next, nil
}

func evidenceNamespace(scope graph.Scope) string {
	if len(scope.Namespaces) == 1 {
		return scope.Namespaces[0]
	}
	return ""
}
