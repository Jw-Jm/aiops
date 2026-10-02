package httpapi

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"regexp"
)

var sp04KeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
var errSP04KeyReuse = errors.New("IDEMPOTENCY_CONFLICT")

// Only a digest and object identifier are durable; fact slices never enter the
// generic idempotency response cache in PostgreSQL.
func (h *SP04Handlers) bindSP04Request(ctx context.Context, request auth.RequestContext, operation, key, digest string) (uuid.UUID, error) {
	if !sp04KeyPattern.MatchString(key) {
		return uuid.Nil, errors.New("INVALID_ARGUMENT")
	}
	id := uuid.Must(uuid.NewV7())
	err := persistence.WithTenantTx(ctx, h.Pool, request.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,55))`, request.TenantID.String()+"/"+request.Subject+"/"+operation+"/"+key); err != nil {
			return err
		}
		var previous string
		err := tx.QueryRow(ctx, `SELECT request_digest,object_id FROM platform.sp04_request_ledger WHERE tenant_id=$1 AND subject=$2 AND operation=$3 AND idempotency_key=$4`, request.TenantID, request.Subject, operation, key).Scan(&previous, &id)
		if err == nil {
			if previous != digest {
				return errSP04KeyReuse
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO platform.sp04_request_ledger(tenant_id,subject,operation,idempotency_key,request_digest,object_id) VALUES($1,$2,$3,$4,$5,$6)`, request.TenantID, request.Subject, operation, key, digest, id)
		return err
	})
	return id, err
}
