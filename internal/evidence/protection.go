package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
	"time"
)

// ReconcileProtection holds the same tenant lock as Hold, dependency writes and
// cleanup across object write/readback and the database receipt. A lost receipt
// can only leave excess physical protection and is safe to retry.
func (s *ArchiveService) ReconcileProtection(ctx context.Context, tenant, id uuid.UUID) error {
	return persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		if err := retentionLock(ctx, tx, tenant); err != nil {
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT i.object_ref FROM platform.evidence_archive_intents i JOIN platform.evidence_metadata m USING(tenant_id,evidence_id) WHERE i.tenant_id=$1 AND i.evidence_id=$2 AND i.status='verified' AND NOT m.deleting FOR UPDATE OF m,i`, tenant, id).Scan(&raw); err != nil {
			return err
		}
		var ref ArchiveRef
		if json.Unmarshal(raw, &ref) != nil || ref.BackendLogicalID != s.BackendLogicalID {
			return ErrArgument
		}
		var until time.Time
		var hold bool
		err := tx.QueryRow(ctx, `WITH RECURSIVE closure AS (SELECT $2::uuid AS evidence_id UNION SELECT d.referrer_id FROM platform.evidence_dependencies d JOIN closure c ON d.dependency_id=c.evidence_id WHERE d.tenant_id=$1), protection AS (SELECT m.retain_until AS until,m.legal_hold AS hold FROM closure c JOIN platform.evidence_metadata m ON m.tenant_id=$1 AND m.evidence_id=c.evidence_id UNION ALL SELECT r.retain_until,r.active FROM closure c JOIN platform.evidence_retention_references r ON r.tenant_id=$1 AND r.evidence_id=c.evidence_id) SELECT max(until),bool_or(hold) FROM protection`, tenant, id).Scan(&until, &hold)
		if err != nil {
			return err
		}
		// Never shorten compliance retention when a referrer or hold is withdrawn.
		if until.Before(ref.Object.RetainUntil) {
			until = ref.Object.RetainUntil
		}
		protected, err := s.Store.Protect(ctx, tenant, ref.Object, until, hold)
		if err != nil {
			return err
		}
		ref.Object = protected
		raw, err = json.Marshal(ref)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE platform.evidence_archive_intents SET object_ref=$3 WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id, raw); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE platform.evidence_metadata SET protection_synced_at=clock_timestamp() WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id)
		return err
	})
}
func (s *ArchiveService) ReconcileTenantProtection(ctx context.Context, tenant uuid.UUID) error {
	ids := []uuid.UUID{}
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT evidence_id FROM platform.evidence_metadata WHERE tenant_id=$1 AND replay_state='archived_verified' AND NOT deleting AND (protection_synced_at IS NULL OR protection_synced_at<clock_timestamp()-interval '1 minute') ORDER BY protection_synced_at NULLS FIRST,evidence_id LIMIT 100`, tenant)
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
		return err
	}
	var failures []error
	for _, id := range ids {
		if err := s.ReconcileProtection(ctx, tenant, id); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func invalidateProtection(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `WITH RECURSIVE dependencies AS (SELECT $2::uuid AS evidence_id UNION SELECT d.dependency_id FROM platform.evidence_dependencies d JOIN dependencies c ON d.referrer_id=c.evidence_id WHERE d.tenant_id=$1) UPDATE platform.evidence_metadata m SET protection_synced_at=NULL FROM dependencies d WHERE m.tenant_id=$1 AND m.evidence_id=d.evidence_id`, tenant, id)
	return err
}
