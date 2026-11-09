package assistant

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
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
		allowed, err := setLegalHoldTx(ctx, tx, tenant, turn, subject, held, tools.RedactText(reason))
		if err != nil {
			return err
		}
		if !allowed {
			return investigation.ErrDenied
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: turn, EntityKind: "assistant_turn", EventType: "assistant.legal_hold.changed", Subject: subject, Payload: map[string]any{"held": held, "reason": tools.RedactText(reason)}})
		return err
	})
}

// setLegalHoldTx writes the hold through the fence that matches the proven
// identity source. A legacy administrator keeps the frozen migration 42 fence
// (platform.role_bindings). A native administrator is fenced inside the
// database by the live session, the account's own subject and the current
// platform_admin grant, so the write can never depend on a legacy binding.
func setLegalHoldTx(ctx context.Context, tx pgx.Tx, tenant, turn uuid.UUID, subject string, held bool, reason string) (bool, error) {
	var allowed bool
	if investigation.AuditAuthorityFromContext(ctx).Source == investigation.AuditSourceNative {
		native, ok := auth.NativeSessionFromContext(ctx)
		if !ok {
			return false, investigation.ErrDenied
		}
		session := native.Session
		if session.TenantID != tenant || session.Subject != subject ||
			session.UserID == uuid.Nil || session.SessionID == uuid.Nil ||
			session.AuthorizationVersion < 1 || session.CredentialVersion < 1 || session.SessionVersion < 1 {
			return false, investigation.ErrDenied
		}
		err := tx.QueryRow(ctx, `SELECT investigation.assistant_set_hold_native($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			tenant, turn, session.UserID, session.SessionID, session.AuthorizationVersion,
			session.CredentialVersion, session.SessionVersion, subject, held, reason).Scan(&allowed)
		return allowed, err
	}
	err := tx.QueryRow(ctx, `SELECT investigation.assistant_set_hold($1,$2,$3,$4,$5)`, tenant, turn, subject, held, reason).Scan(&allowed)
	return allowed, err
}
