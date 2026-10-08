package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/archive"
	"ops-platform/internal/audit"
	"ops-platform/internal/persistence"
	"time"
)

var ErrProtected = errors.New("retention dependency or Legal Hold active")

// Legal Hold and cleanup serialize on the same tenant lock and evidence row.
// Metadata guards use NO KEY UPDATE: keys are immutable, and FK references may
// already hold KEY SHARE before Protect takes the tenant lock. FOR UPDATE would
// deadlock that order against an archive/hold/protection pass. NO KEY UPDATE
// still excludes metadata writers and DELETE; Protect rechecks the tombstone
// before any reference transaction can commit. Dependency writes must use it.
func retentionLock(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,180))`, tenant.String())
	return err
}
func SetLegalHold(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, hold bool, subject string) error {
	if err := retentionLock(ctx, tx, tenant); err != nil {
		return err
	}
	var deleting bool
	if err := tx.QueryRow(ctx, `SELECT deleting FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2 FOR NO KEY UPDATE`, tenant, id).Scan(&deleting); err != nil {
		return err
	}
	if deleting {
		return errors.New("evidence cleanup already committed")
	}
	_, err := tx.Exec(ctx, `UPDATE platform.evidence_metadata SET legal_hold=$3 WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id, hold)
	if err != nil {
		return err
	}
	recordID := uuid.Must(uuid.NewV7())
	_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: recordID, EntityID: id, EntityKind: "evidence", EventType: "evidence.legal_hold", Subject: subject, Payload: map[string]any{"hold": hold}})
	if err != nil {
		return err
	}
	return Protect(ctx, tx, tenant, id, recordID, "audit", time.Now().Add(365*24*time.Hour), false)
}
func Protect(ctx context.Context, tx pgx.Tx, tenant, id, reference uuid.UUID, kind string, until time.Time, active bool) error {
	if err := retentionLock(ctx, tx, tenant); err != nil {
		return err
	}
	var deleting bool
	if err := tx.QueryRow(ctx, `SELECT deleting FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2 FOR NO KEY UPDATE`, tenant, id).Scan(&deleting); err != nil {
		return err
	}
	if deleting {
		return errors.New("evidence cleanup already committed")
	}
	_, err := tx.Exec(ctx, `INSERT INTO platform.evidence_retention_references(tenant_id,evidence_id,reference_kind,reference_id,retain_until,active) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(tenant_id,reference_kind,reference_id,evidence_id) DO UPDATE SET retain_until=GREATEST(evidence_retention_references.retain_until,EXCLUDED.retain_until),active=EXCLUDED.active`, tenant, id, kind, reference, until, active)
	if err != nil {
		return err
	}
	return invalidateProtection(ctx, tx, tenant, id)
}
func (s *ArchiveService) Cleanup(ctx context.Context, tenant, id uuid.UUID) error {
	var ref ArchiveRef
	deleted := false
	// Commit a tombstone before external deletion. A crash after deletion cannot
	// roll back to archived_verified; retry resumes the durable deleting intent.
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		if err := retentionLock(ctx, tx, tenant); err != nil {
			return err
		}
		var raw []byte
		var status string
		if err := tx.QueryRow(ctx, `SELECT object_ref,status FROM platform.evidence_archive_intents WHERE tenant_id=$1 AND evidence_id=$2 FOR UPDATE`, tenant, id).Scan(&raw, &status); err != nil {
			return err
		}
		if status == "deleted" {
			deleted = true
			return nil
		}
		if json.Unmarshal(raw, &ref) != nil || ref.BackendLogicalID != s.BackendLogicalID {
			return ErrArgument
		}
		if status == "deleting" {
			return nil
		}
		if status != "verified" {
			return errors.New("archive not verified")
		}
		var protected bool
		err := tx.QueryRow(ctx, `WITH RECURSIVE closure AS (SELECT evidence_id FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2 UNION SELECT d.referrer_id FROM platform.evidence_dependencies d JOIN closure c ON d.dependency_id=c.evidence_id WHERE d.tenant_id=$1) SELECT EXISTS(SELECT 1 FROM closure c JOIN platform.evidence_metadata m ON m.evidence_id=c.evidence_id AND m.tenant_id=$1 WHERE m.legal_hold OR m.retain_until>clock_timestamp() OR m.replay_state='archive_pending') OR EXISTS(SELECT 1 FROM closure c JOIN platform.evidence_retention_references r ON r.evidence_id=c.evidence_id AND r.tenant_id=$1 WHERE r.active OR r.retain_until>clock_timestamp())`, tenant, id).Scan(&protected)
		if err != nil {
			return err
		}
		if protected || time.Now().Before(ref.Object.RetainUntil) {
			return ErrProtected
		}
		if _, err = tx.Exec(ctx, `UPDATE platform.evidence_metadata SET deleting=true,replay_state='unavailable' WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE platform.evidence_archive_intents SET status='deleting' WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: id, EntityKind: "evidence", EventType: "evidence.retention_deleting", Subject: "platform-worker", Payload: map[string]any{"contentDigest": ref.PlaintextDigest}})
		return err
	})
	if err != nil || deleted {
		return err
	}
	if err = s.Store.Delete(ctx, tenant, ref.Object); err != nil && !errors.Is(err, archive.ErrObjectNotFound) {
		return err
	}
	return persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		if err := retentionLock(ctx, tx, tenant); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE platform.evidence_archive_intents SET status='deleted',envelope=NULL,envelope_bytes=NULL WHERE tenant_id=$1 AND evidence_id=$2 AND status='deleting'`, tenant, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: id, EntityKind: "evidence", EventType: "evidence.retention_deleted", Subject: "platform-worker", Payload: map[string]any{"contentDigest": ref.PlaintextDigest}})
		return err
	})
}
