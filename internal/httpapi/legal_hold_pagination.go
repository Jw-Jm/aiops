package httpapi

import (
	"crypto/ed25519"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"strconv"
)

func (h *SP04Handlers) listLegalHolds(w http.ResponseWriter, r *http.Request, request auth.RequestContext) {
	limit := 200
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
	}
	if err != nil || limit < 1 || limit > 200 {
		writeSP04Error(w, 400, "INVALID_ARGUMENT", "invalid page limit", false, request.RequestID)
		return
	}
	scopeDigest := graph.ScopeDigest(struct{ Tenant, Subject string }{request.TenantID.String(), request.Subject})
	after := ""
	var previous graphCursor
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		if len(h.SigningKey) != ed25519.PrivateKeySize || decodeCursor(h.SigningKey.Public().(ed25519.PublicKey), cursor, &previous) != nil || previous.ScopeDigest != scopeDigest || previous.QueryDigest != "legal-holds" {
			writeSP04Error(w, 409, "CONFLICT", "cursor scope or deadline changed", false, request.RequestID)
			return
		}
		if _, err := uuid.Parse(previous.After); err != nil {
			writeSP04Error(w, 400, "INVALID_ARGUMENT", "invalid cursor", false, request.RequestID)
			return
		}
		after = previous.After
	}
	items := []map[string]any{}
	ids := []uuid.UUID{}
	revision := ""
	err = persistence.WithTenantTx(r.Context(), h.Pool, request.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,180))`, request.TenantID.String()); err != nil {
			return err
		}
		// The append-only Legal Hold audit chain is the list generation. A mutation
		// invalidates a previously issued page instead of silently mixing snapshots.
		if err := tx.QueryRow(r.Context(), `SELECT COALESCE(max(audit_seq),0)::text FROM audit.records WHERE tenant_id=$1 AND event_type='evidence.legal_hold'`, request.TenantID).Scan(&revision); err != nil {
			return err
		}
		if after != "" && previous.Revision.OwnerEpoch != hashRevision(revision) {
			return errors.New("stale hold cursor")
		}
		rows, err := tx.Query(r.Context(), `SELECT evidence_id FROM platform.evidence_metadata WHERE tenant_id=$1 AND legal_hold AND ($2='' OR evidence_id>NULLIF($2,'')::uuid) ORDER BY evidence_id LIMIT $3`, request.TenantID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		writeSP04Error(w, 409, "CONFLICT", "hold metadata or cursor unavailable", false, request.RequestID)
		return
	}
	next := ""
	if len(ids) > limit {
		ids = ids[:limit]
		if len(h.SigningKey) != ed25519.PrivateKeySize {
			writeSP04Error(w, 503, "SOURCE_DEGRADED", "pagination signing unavailable", true, request.RequestID)
			return
		}
		next, err = encodeCursor(h.SigningKey, graphCursor{ScopeDigest: scopeDigest, QueryDigest: "legal-holds", After: ids[len(ids)-1].String(), Revision: graph.Revision{OwnerEpoch: hashRevision(revision)}})
		if err != nil {
			writeSP04Error(w, 503, "SOURCE_DEGRADED", "cursor unavailable", true, request.RequestID)
			return
		}
	}
	for _, id := range ids {
		items = append(items, map[string]any{"evidenceId": id, "hold": true})
	}
	writeJSON(w, 200, map[string]any{"schemaVersion": "legal-hold-page/v1", "requestId": request.RequestID, "data": items, "meta": legalHoldMeta(next)})
}
func hashRevision(raw string) int64 { n, _ := strconv.ParseInt(raw, 10, 64); return n + 1 }
