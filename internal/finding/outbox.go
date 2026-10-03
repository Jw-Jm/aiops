package finding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
	"time"
)

type Delivery struct {
	TenantID  uuid.UUID
	EventID   uuid.UUID
	FindingID string
	Revision  int64
	Payload   json.RawMessage
	Token     uuid.UUID
	Attempts  int
}

var errHistoricalSchema = errors.New("UNSUPPORTED_HISTORICAL_SCHEMA")

type Consumer func(context.Context, pgx.Tx, Delivery) error

func (s Service) Claim(ctx context.Context, tenant uuid.UUID) (Delivery, error) {
	d := Delivery{TenantID: tenant, Token: uuid.Must(uuid.NewV7())}
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE finding.outbox SET state='claimed',claim_token=$2,claimed_at=clock_timestamp(),attempts=attempts+1 WHERE (tenant_id,outbox_id)=(SELECT tenant_id,outbox_id FROM finding.outbox WHERE tenant_id=$1 AND ((state='pending' AND next_attempt_at<=clock_timestamp()) OR (state='claimed' AND claimed_at<clock_timestamp()-interval '30 seconds')) ORDER BY created_at,outbox_id LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING outbox_id,finding_id,aggregate_revision,payload,attempts`, tenant, d.Token).Scan(&d.EventID, &d.FindingID, &d.Revision, &d.Payload, &d.Attempts)
	})
	return d, err
}

// Delivery and consumer inbox/state commit together. If the process dies after
// claim, another Worker reclaims the unchanged event ID after its bounded lease.
func (s Service) Deliver(ctx context.Context, d Delivery, consume Consumer) error {
	err := persistence.WithTenantTx(ctx, s.Pool, d.TenantID, func(tx pgx.Tx) error {
		var valid bool
		var schema string
		if err := tx.QueryRow(ctx, `SELECT schema_version,state='claimed' AND claim_token=$3 AND claimed_at>clock_timestamp()-interval '30 seconds' FROM finding.outbox WHERE tenant_id=$1 AND outbox_id=$2 FOR UPDATE`, d.TenantID, d.EventID, d.Token).Scan(&schema, &valid); err != nil {
			return err
		}
		if !valid {
			return errors.New("STALE_CONTEXT")
		}
		if schema != "finding/v2" {
			return fmt.Errorf("%w: %w", errHistoricalSchema, ErrInvalid)
		}
		if err := consume(ctx, tx, d); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE finding.outbox SET state='delivered',delivered_at=clock_timestamp(),claim_token=NULL WHERE tenant_id=$1 AND outbox_id=$2 AND claim_token=$3`, d.TenantID, d.EventID, d.Token)
		return err
	})
	if err == nil {
		return nil
	}
	retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	retryErr := persistence.WithTenantTx(retryCtx, s.Pool, d.TenantID, func(tx pgx.Tx) error {
		// Never dead-letter a transient outage, revocation or transaction failure.
		// Only an invalid persisted payload qualifies as poison.
		state := "pending"
		code := "CONSUMER_UNAVAILABLE"
		if errors.Is(err, ErrInvalid) {
			state = "deadletter"
			code = "INVALID_PAYLOAD"
			if errors.Is(err, errHistoricalSchema) {
				code = "UNSUPPORTED_HISTORICAL_SCHEMA"
			}
		}
		_, updateErr := tx.Exec(retryCtx, `UPDATE finding.outbox SET state=$4,next_attempt_at=clock_timestamp()+$5::interval,claim_token=NULL,last_error_code=$6 WHERE tenant_id=$1 AND outbox_id=$2 AND claim_token=$3`, d.TenantID, d.EventID, d.Token, state, (time.Duration(min(d.Attempts, 30)) * time.Second).String(), code)
		return updateErr
	})
	if retryErr != nil {
		return errors.Join(err, retryErr)
	}
	return err
}
func (s Service) RelayPass(ctx context.Context, tenant uuid.UUID, consumer Consumer, limit int) error {
	for i := 0; i < min(limit, 100); i++ {
		d, err := s.Claim(ctx, tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = s.Deliver(ctx, d, consumer); err != nil {
			return err
		}
	}
	return nil
}
