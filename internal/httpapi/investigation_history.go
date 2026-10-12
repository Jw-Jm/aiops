package httpapi

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"strconv"
	"strings"
	"time"
)

type investigationHistoryCursor struct {
	Tenant   uuid.UUID `json:"tenant"`
	Subject  string    `json:"subject"`
	Incident uuid.UUID `json:"incident"`
	Before   uuid.UUID `json:"before"`
	Expires  int64     `json:"expires"`
}

func (h *InvestigationHandlers) history(w http.ResponseWriter, r *http.Request, a auth.RequestContext, incident string) {
	id, err := uuid.Parse(incident)
	if err != nil {
		investigationError(w, investigation.ErrInvalid, a.RequestID)
		return
	}
	limit := 20
	for k, v := range r.URL.Query() {
		if (k != "cursor" && k != "limit") || len(v) != 1 {
			investigationError(w, investigation.ErrInvalid, a.RequestID)
			return
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
	}
	if err != nil || limit < 1 || limit > 50 {
		investigationError(w, investigation.ErrInvalid, a.RequestID)
		return
	}
	var before *uuid.UUID
	if token := r.URL.Query().Get("cursor"); token != "" {
		parts := strings.Split(token, ".")
		var c investigationHistoryCursor
		valid := len(parts) == 2 && len(token) <= 4096 && len(h.CursorKey) == ed25519.PrivateKeySize
		if valid {
			b, e := base64.RawURLEncoding.DecodeString(parts[0])
			sig, se := base64.RawURLEncoding.DecodeString(parts[1])
			valid = e == nil && se == nil && ed25519.Verify(h.CursorKey.Public().(ed25519.PublicKey), b, sig) && json.Unmarshal(b, &c) == nil && c.Tenant == a.TenantID && c.Subject == a.Subject && c.Incident == id
		}
		if !valid {
			investigationError(w, investigation.ErrDenied, a.RequestID)
			return
		}
		if c.Expires <= time.Now().Unix() {
			writeSP04Error(w, 410, "CURSOR_EXPIRED", "reload investigation history", false, a.RequestID)
			return
		}
		before = &c.Before
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ids := []uuid.UUID{}
	err = persistence.WithTenantTx(ctx, h.Repository.Pool, a.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT job_id FROM investigation.jobs WHERE tenant_id=$1 AND incident_id=$2 AND requesting_subject=$3 AND schema_version='investigation-job/v2' AND ($4::uuid IS NULL OR job_id<$4) ORDER BY job_id DESC LIMIT $5`, a.TenantID, id, a.Subject, before, limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var job uuid.UUID
			if e = rows.Scan(&job); e != nil {
				return e
			}
			ids = append(ids, job)
		}
		return rows.Err()
	})
	if err != nil {
		investigationError(w, err, a.RequestID)
		return
	}
	out := []investigation.Job{}
	var next any
	if len(ids) > limit {
		ids = ids[:limit]
		if len(h.CursorKey) != ed25519.PrivateKeySize {
			investigationError(w, investigation.ErrInvalid, a.RequestID)
			return
		}
		b, _ := json.Marshal(investigationHistoryCursor{a.TenantID, a.Subject, id, ids[len(ids)-1], time.Now().Add(10 * time.Minute).Unix()})
		next = base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.CursorKey, b))
	}
	for _, jobID := range ids {
		job, e := h.Repository.Get(ctx, a.TenantID, jobID)
		if e != nil || job.Subject != a.Subject {
			investigationError(w, investigation.ErrDenied, a.RequestID)
			return
		}
		out = append(out, job)
	}
	writeJSON(w, 200, map[string]any{"data": out, "meta": map[string]any{"nextCursor": next}, "requestId": a.RequestID})
}
