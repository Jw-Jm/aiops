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

// RecoverWithSources recovers pending encryption from the immutable source
// query while it is still available. Only the original digest may be sealed;
// a changed or expired source produces an explicit unavailable pending intent.
func (s *ArchiveService) RecoverWithSources(ctx context.Context, tenant uuid.UUID, adapters map[string]Adapter) error {
	type pending struct {
		id        uuid.UUID
		metadata  Evidence
		namespace string
		queryArgs []byte
	}
	work := []pending{}
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT m.evidence_id,m.metadata,m.namespace,i.query_args FROM platform.evidence_metadata m JOIN platform.evidence_archive_intents i USING(tenant_id,evidence_id) WHERE m.tenant_id=$1 AND i.status='pending' AND i.envelope_bytes IS NULL AND m.replay_state='archive_pending' AND NOT m.deleting ORDER BY m.evidence_id LIMIT 100`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p pending
			var raw []byte
			if err := rows.Scan(&p.id, &raw, &p.namespace, &p.queryArgs); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &p.metadata); err != nil {
				return err
			}
			work = append(work, p)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	var last error
	for _, p := range work {
		if !time.Now().Before(p.metadata.SourceRetentionUntil) {
			if err := s.markSourceExpired(ctx, tenant, p.id); err != nil {
				last = err
			}
			continue
		}
		adapter, ok := adapters[p.metadata.SourceRegistrationID]
		if !ok || len(p.queryArgs) == 0 {
			last = errors.New("source unavailable for pending encryption")
			continue
		}
		var q Query
		if json.Unmarshal(p.queryArgs, &q) != nil {
			last = ErrArgument
			continue
		}
		q.Scope = p.metadata.EffectiveScope

		result, err := adapter.Query(ctx, q)
		if err != nil {
			last = errors.New("pending source replay unavailable")
			continue
		}
		restored := false
		for _, candidate := range result.Evidence {
			if candidate.ContentDigest != p.metadata.ContentDigest || Digest(candidate.Data) != p.metadata.ContentDigest || candidate.SourceRevision != p.metadata.SourceRevision || candidate.BackendLogicalID != p.metadata.BackendLogicalID || candidate.SourceRegistrationID != p.metadata.SourceRegistrationID || candidate.SourceSystem != p.metadata.SourceSystem || candidate.TenantID != p.metadata.TenantID || candidate.ResourceCanonicalID != p.metadata.ResourceCanonicalID || candidate.QueryHash != p.metadata.QueryHash || candidate.QueryTemplateVersion != p.metadata.QueryTemplateVersion {
				continue
			}
			p.metadata.Data = candidate.Data
			// Retention is already durable; Capture checks metadata identity before
			// replacing the previously absent encryption envelope.
			var retainUntil time.Time
			err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT retain_until FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2`, tenant, p.id).Scan(&retainUntil)
			})
			if err == nil {
				err = s.CaptureQuery(ctx, p.metadata, p.namespace, retainUntil, q)
			}
			if err != nil {
				last = err
			} else {
				restored = true
			}
			break
		}
		if !restored && last == nil {
			last = errors.New("source digest changed or expired")
		}
	}
	if err := s.RecoverTenant(ctx, tenant); err != nil {
		last = err
	}
	return last
}

// MaintenancePass resumes archive upload and deletion intents under their
// original tenant. It does not use a database route to elect Graph ownership.
func (s *ArchiveService) MaintenancePass(ctx context.Context, adapters map[string]Adapter) error {
	tenants := []uuid.UUID{}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT tenant_id FROM platform.tenants WHERE status='active' ORDER BY tenant_id`)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	for rows.Next() {
		var tenant uuid.UUID
		if err := rows.Scan(&tenant); err != nil {
			rows.Close()
			tx.Rollback(ctx)
			return err
		}
		tenants = append(tenants, tenant)
	}
	err = rows.Err()
	rows.Close()
	if err == nil {
		err = tx.Commit(ctx)
	} else {
		tx.Rollback(ctx)
	}
	if err != nil {
		return err
	}
	var last error
	for _, tenant := range tenants {
		if err := s.ReconcileTenantProtection(ctx, tenant); err != nil {
			last = err
		}
		if err := s.RecoverWithSources(ctx, tenant, adapters); err != nil {
			last = err
		}
		ids := []uuid.UUID{}
		err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT i.evidence_id FROM platform.evidence_archive_intents i JOIN platform.evidence_metadata m USING(tenant_id,evidence_id) WHERE i.tenant_id=$1 AND (i.status='deleting' OR (i.status='verified' AND m.retain_until<=clock_timestamp() AND NOT m.legal_hold AND (i.object_ref->'object'->>'retainUntil')::timestamptz<=clock_timestamp())) ORDER BY i.evidence_id LIMIT 100`, tenant)
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
			last = err
			continue
		}
		for _, id := range ids {
			if err := s.Cleanup(ctx, tenant, id); err != nil && !errors.Is(err, ErrProtected) {
				last = err
			}
		}
	}
	return last
}

// Only intents that still lack ciphertext can become source-expired. A
// concurrent Prepare wins atomically and leaves the durable envelope recoverable.
func (s *ArchiveService) markSourceExpired(ctx context.Context, tenant, id uuid.UUID) error {
	return persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		if err := retentionLock(ctx, tx, tenant); err != nil {
			return err
		}
		var encoded []byte
		if err := tx.QueryRow(ctx, `SELECT envelope_bytes FROM platform.evidence_archive_intents WHERE tenant_id=$1 AND evidence_id=$2 FOR UPDATE`, tenant, id).Scan(&encoded); err != nil {
			return err
		}
		if len(encoded) > 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE platform.evidence_archive_intents SET last_error='source_expired_before_encryption' WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE platform.evidence_metadata SET replay_state='unavailable' WHERE tenant_id=$1 AND evidence_id=$2 AND replay_state='archive_pending'`, tenant, id)
		return err
	})
}
