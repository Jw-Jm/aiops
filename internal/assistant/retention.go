package assistant

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
	"strings"
	"time"
)

func (r Repository) Cleanup(ctx context.Context, tenant uuid.UUID, limit int) (int, error) {
	var count int
	if limit < 1 || limit > 100 {
		return count, investigation.ErrInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := persistence.WithTenantTx(bounded, r.Pool, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(bounded, `SET LOCAL statement_timeout='2000ms'`); err != nil {
			return err
		}
		if err := tx.QueryRow(bounded, `SELECT investigation.assistant_cleanup($1,$2)`, tenant, limit).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			_, err := audit.Append(bounded, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: tenant, EntityKind: "assistant", EventType: "assistant.retention.deleted", Subject: "platform-worker", Payload: map[string]any{"deletedRecords": count, "ordinaryRetentionDays": 30}})
			return err
		}
		return nil
	})
	return count, err
}
func (r Repository) SetLegalHold(ctx context.Context, tenant, turn uuid.UUID, subject string, held bool, reason string) error {
	if turn == uuid.Nil || subject == "" || strings.TrimSpace(reason) == "" || len(reason) > 1024 {
		return investigation.ErrInvalid
	}
	return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		if _, err := investigation.AuditScopeTx(ctx, tx, tenant, subject); err != nil {
			return err
		}
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT investigation.assistant_set_hold($1,$2,$3,$4,$5)`, tenant, turn, subject, held, tools.RedactText(reason)).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return investigation.ErrDenied
		}
		_, err := audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: turn, EntityKind: "assistant_turn", EventType: "assistant.legal_hold.changed", Subject: subject, Payload: map[string]any{"held": held, "reason": tools.RedactText(reason)}})
		return err
	})
}
