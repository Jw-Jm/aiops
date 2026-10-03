package finding

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
	"ops-platform/internal/source"
	"time"
)

// PollIdentity durably chooses an occurrence before ingestion. A crash between
// choosing and ingesting cannot create a second occurrence. Only explicit healthy
// complete observations close it; absent/partial/timeout snapshots never call it.
func (s Service) PollIdentity(ctx context.Context, b source.BoundSourceContext, key, state, ns string, observation ...time.Time) (string, time.Time, error) {
	var id string
	var start time.Time
	noMutation := false
	err := persistence.WithTenantTx(ctx, s.Pool, b.TenantID, func(tx pgx.Tx) error {
		if err := CheckBound(ctx, tx, b, ns, "finding-envelope/v2"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,5))`, b.TenantID.String()+"|"+b.SourceID.String()+"|"+key); err != nil {
			return err
		}
		var active bool
		var resolved *time.Time
		err := tx.QueryRow(ctx, `SELECT occurrence_id,starts_at,active,last_resolved_at FROM finding.poll_occurrences WHERE tenant_id=$1 AND source_id=$2 AND signal_key=$3 FOR UPDATE`, b.TenantID, b.SourceID, key).Scan(&id, &start, &active, &resolved)
		if errors.Is(err, pgx.ErrNoRows) {

			id = uuid.Must(uuid.NewV7()).String()
			if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&start); err != nil {
				return err
			}
			if len(observation) == 1 && !observation[0].IsZero() && !observation[0].After(start) {
				start = observation[0].UTC().Truncate(time.Microsecond)
			}
			_, err = tx.Exec(ctx, `INSERT INTO finding.poll_occurrences(tenant_id,source_id,signal_key,occurrence_id,starts_at,active) VALUES($1,$2,$3,$4,$5,$6)`, b.TenantID, b.SourceID, key, id, start, state == "firing")

			if err == nil && state == "resolved" {
				_, err = tx.Exec(ctx, `UPDATE finding.poll_occurrences SET last_resolved_at=$4 WHERE tenant_id=$1 AND source_id=$2 AND signal_key=$3`, b.TenantID, b.SourceID, key, start)
				noMutation = true
			}
			return err
		}
		if err != nil {
			return err
		}
		// Ingestion may have durably resolved before a process died ahead of
		// ClosePoll. Recover from the aggregate rather than trusting that flag.
		var aggregateResolved *time.Time
		if err := tx.QueryRow(ctx, `SELECT max(observed_at) FROM finding.records WHERE tenant_id=$1 AND source_id=$2 AND occurrence_id=$3 AND lifecycle_state='resolved'`, b.TenantID, b.SourceID, id).Scan(&aggregateResolved); err != nil {
			return err
		}
		if aggregateResolved != nil && (resolved == nil || aggregateResolved.After(*resolved)) {
			resolved = aggregateResolved
		}
		if aggregateResolved != nil {
			active = false
		}
		if !active && state == "resolved" {
			if len(observation) == 1 && !observation[0].IsZero() {
				_, err = tx.Exec(ctx, `UPDATE finding.poll_occurrences SET last_resolved_at=GREATEST(last_resolved_at,$4) WHERE tenant_id=$1 AND source_id=$2 AND signal_key=$3`, b.TenantID, b.SourceID, key, observation[0])
			}
			noMutation = true
			return err
		}
		if !active && state == "firing" {
			if resolved != nil && (len(observation) != 1 || !observation[0].After(*resolved)) {
				return pgx.ErrNoRows
			}
			id = uuid.Must(uuid.NewV7()).String()
			if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&start); err != nil {
				return err
			}
			if len(observation) == 1 && !observation[0].IsZero() && !observation[0].After(start) {
				start = observation[0].UTC().Truncate(time.Microsecond)
			}
			_, err = tx.Exec(ctx, `UPDATE finding.poll_occurrences SET occurrence_id=$4,starts_at=$5,active=true,last_resolved_at=$6 WHERE tenant_id=$1 AND source_id=$2 AND signal_key=$3`, b.TenantID, b.SourceID, key, id, start, resolved)
			return err
		}
		return nil
	})
	if err == nil && noMutation {
		err = pgx.ErrNoRows
	}
	return id, start.UTC(), err
}
func (s Service) ClosePoll(ctx context.Context, b source.BoundSourceContext, key, id string) error {
	return persistence.WithTenantTx(ctx, s.Pool, b.TenantID, func(tx pgx.Tx) error {
		var ns string
		if err := tx.QueryRow(ctx, `SELECT namespace FROM finding.records WHERE tenant_id=$1 AND source_id=$2 AND occurrence_id=$3 LIMIT 1`, b.TenantID, b.SourceID, id).Scan(&ns); err != nil {
			return err
		}
		if err := CheckBound(ctx, tx, b, ns, "finding-envelope/v2"); err != nil {
			return err
		}
		// Check durable resolved aggregate, not the local response to a timed-out write.
		_, err := tx.Exec(ctx, `UPDATE finding.poll_occurrences p SET active=false,last_resolved_at=(SELECT max(f.observed_at) FROM finding.records f WHERE f.tenant_id=p.tenant_id AND f.source_id=p.source_id AND f.occurrence_id=p.occurrence_id::text AND f.lifecycle_state='resolved') WHERE p.tenant_id=$1 AND p.source_id=$2 AND p.signal_key=$3 AND p.occurrence_id=$4 AND EXISTS(SELECT 1 FROM finding.records f WHERE f.tenant_id=p.tenant_id AND f.source_id=p.source_id AND f.occurrence_id=p.occurrence_id::text AND f.lifecycle_state='resolved')`, b.TenantID, b.SourceID, key, id)
		return err
	})
}
