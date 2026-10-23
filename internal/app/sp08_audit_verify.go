package app

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/authz"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"strconv"
	"time"
)

var errAuditRangeBudget = errors.New("audit verification range exceeds budget")

type AuditRangeVerifier interface {
	VerifyRange(context.Context, uuid.UUID, int64, int64) error
}

func serveWorkerAudit(w http.ResponseWriter, r *http.Request, trust auth.WorkloadTrust, key ed25519.PublicKey, pool *pgxpool.Pool, namespace string, verifier AuditRangeVerifier) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, code string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"code": code})
	}
	if r.Method != "POST" || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		fail(403, "FORBIDDEN")
		return
	}
	identity, err := auth.VerifyWorkload(auth.WithWorkloadTrust(r.Context(), trust), r.TLS.PeerCertificates[0])
	if err != nil || identity.Namespace != namespace || identity.ServiceAccount != "ops-api" {
		fail(403, "FORBIDDEN")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(raw) > 4096 {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	claims, err := graph.VerifyContext(key, r.Header.Get("X-Graph-Context"), "platform-audit-verify", raw)
	if err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	var request struct {
		TenantID uuid.UUID `json:"tenantId"`
		First    string    `json:"first"`
		Last     string    `json:"last"`
	}
	if json.Unmarshal(raw, &request) != nil || request.TenantID == uuid.Nil || claims.Scope.Tenant != request.TenantID.String() {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	first, e1 := strconv.ParseInt(request.First, 10, 64)
	last, e2 := strconv.ParseInt(request.Last, 10, 64)
	if e1 != nil || e2 != nil || first < 1 || last < first {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	deadline := claims.Deadline
	if max := time.Now().Add(10 * time.Second); deadline.After(max) {
		deadline = max
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	authorize := func() error {
		return persistence.WithTenantTx(ctx, pool, request.TenantID, func(tx pgx.Tx) error {
			var count int64
			// This machine-to-machine path carries a signed graph context, not a
			// browser session, so the shared layer resolves the legacy binding
			// for the signed subject. It never accepts a browser-supplied role.
			if err := authz.RequireAdmin(ctx, tx, auth.RequestContext{
				TenantID: request.TenantID,
				Subject:  claims.Subject,
				Roles:    []auth.Role{auth.PlatformAdmin},
			}); err != nil {
				if errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrIdentityMismatch) {
					return auth.ErrForbidden
				}
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM audit.records WHERE tenant_id=$1 AND audit_seq BETWEEN $2 AND $3 LIMIT 201) budget`, request.TenantID, first, last).Scan(&count); err != nil {
				return err
			}
			if count == 0 || count > 200 {
				return errAuditRangeBudget
			}
			return nil
		})
	}
	if err := authorize(); err != nil {
		if err == errAuditRangeBudget {
			fail(422, "AUDIT_RANGE_BUDGET")
		} else if errors.Is(err, auth.ErrForbidden) {
			fail(403, "FORBIDDEN")
		} else {
			fail(503, "AUDIT_AUTHORIZATION_UNAVAILABLE")
		}
		return
	}
	if verifier == nil {
		fail(503, "AUDIT_VERIFIER_UNAVAILABLE")
		return
	}
	verifyErr := verifier.VerifyRange(ctx, request.TenantID, first, last)
	if err := authorize(); err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			fail(403, "FORBIDDEN")
		} else {
			fail(503, "AUDIT_AUTHORIZATION_UNAVAILABLE")
		}
		return
	}
	status, reason := "verified", "signed archive bytes, record digests, Merkle roots, Transit signatures and predecessor coverage verified"
	if verifyErr != nil {
		status = "failed"
		reason = "signed archive verification did not pass; coverage, archive, signature or predecessor proof unavailable or invalid"
	}
	json.NewEncoder(w).Encode(map[string]string{"status": status, "reason": reason, "first": request.First, "last": request.Last})
}
