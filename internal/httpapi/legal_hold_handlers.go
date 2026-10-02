package httpapi

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
	"ops-platform/internal/persistence"
)

func loadEvidenceForScope(ctx context.Context, pool persistence.TxBeginner, tenant, id uuid.UUID) (evidence.Evidence, error) {
	var e evidence.Evidence
	err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT metadata FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2 AND NOT deleting`, tenant, id).Scan(&raw)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &e)
	})
	return e, err
}
func (h *SP04Handlers) legalHold(w http.ResponseWriter, r *http.Request, request auth.RequestContext) {
	if !auth.HasRole(r.Context(), auth.PlatformAdmin) {
		writeSP04Error(w, 403, "FORBIDDEN", "platform administrator required", false, request.RequestID)
		return
	}
	if r.Method == "GET" {
		h.listLegalHolds(w, r, request)
		return
	}
	if r.Method != "POST" {
		writeSP04Error(w, 405, "INVALID_ARGUMENT", "method unavailable", false, request.RequestID)
		return
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			EvidenceID string `json:"evidenceId"`
			Hold       *bool  `json:"hold"`
		}
		if decodeSP04(r, &body) != nil || body.Hold == nil {
			writeSP04Error(w, 400, "INVALID_ARGUMENT", "invalid hold request", false, request.RequestID)
			return
		}
		id, err := uuid.Parse(body.EvidenceID)
		if err != nil {
			writeSP04Error(w, 400, "INVALID_ARGUMENT", "invalid evidence ID", false, request.RequestID)
			return
		}
		tx, ok := TransactionFromContext(r.Context())
		if !ok {
			writeSP04Error(w, 503, "INTERNAL", "transaction unavailable", true, request.RequestID)
			return
		}
		if err := evidence.SetLegalHold(r.Context(), tx, request.TenantID, id, *body.Hold, request.Subject); err != nil {
			writeSP04Error(w, 409, "CONFLICT", "evidence hold unavailable", false, request.RequestID)
			return
		}
		writeJSON(w, 201, map[string]any{"schemaVersion": "legal-hold-result/v1", "requestId": request.RequestID, "data": body, "protectionSync": "pending", "meta": legalHoldMeta("")})
	})
	newTenantIdempotency(h.Pool, "set-evidence-legal-hold")(requireTenantAdminStepUp(handler)).ServeHTTP(w, r)
}

func legalHoldMeta(next string) map[string]any {
	return map[string]any{"nextCursor": next, "freshness": "fresh", "partial": false, "warnings": []string{}, "degradedSources": []string{}}
}
