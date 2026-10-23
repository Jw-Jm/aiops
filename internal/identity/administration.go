package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
)

// The caller holds one tenant transaction across account locks, operation-bound
// proof, idempotency, mutation and audit. No credential material is returned.
func LockAdminAccounts(ctx context.Context, tx pgx.Tx, s ActiveSession, target uuid.UUID) error {
	if s.Mode != "full" || s.MustReenrollMFA {
		return ErrSessionInvalid
	}
	var accepted bool
	if err := tx.QueryRow(ctx, `SELECT identity.admin_lock_accounts($1,$2,$3,$4,$5,$6,$7)`, s.TenantID, s.UserID, s.SessionID, s.CredentialVersion, s.AuthorizationVersion, s.SessionVersion, target).Scan(&accepted); err != nil {
		return err
	}
	if !accepted {
		return ErrSessionInvalid
	}
	return nil
}
func AdminWrite(ctx context.Context, tx pgx.Tx, s ActiveSession, target uuid.UUID, operation string, body json.RawMessage) (json.RawMessage, error) {
	var data json.RawMessage
	err := tx.QueryRow(ctx, `SELECT identity.admin_account_write($1,$2,$3,$4,$5,$6,$7,$8,$9)`, s.TenantID, s.UserID, s.SessionID, s.CredentialVersion, s.AuthorizationVersion, s.SessionVersion, target, operation, []byte(body)).Scan(&data)
	return data, err
}
func AdminAccounts(ctx context.Context, pool persistence.TxBeginner, s ActiveSession, after *uuid.UUID, limit int) ([]json.RawMessage, error) {
	result := []json.RawMessage{}
	if limit < 1 || limit > 101 || s.Mode != "full" || s.MustReenrollMFA {
		return nil, ErrSessionInvalid
	}
	err := persistence.WithTenantTx(ctx, pool, s.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT identity.admin_accounts($1,$2,$3,$4,$5,$6,$7,$8)`, s.TenantID, s.UserID, s.SessionID, s.CredentialVersion, s.AuthorizationVersion, s.SessionVersion, after, limit)
		if err != nil {
			return fmt.Errorf("list native accounts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var data json.RawMessage
			if err := rows.Scan(&data); err != nil {
				return err
			}
			result = append(result, data)
		}
		return rows.Err()
	})
	return result, err
}

func AdminMapSubject(ctx context.Context, tx pgx.Tx, s ActiveSession, target uuid.UUID, body json.RawMessage) (json.RawMessage, error) {
	var data json.RawMessage
	err := tx.QueryRow(ctx, `SELECT identity.admin_map_subject($1,$2,$3,$4,$5,$6,$7,$8)`, s.TenantID, s.UserID, s.SessionID, s.CredentialVersion, s.AuthorizationVersion, s.SessionVersion, target, []byte(body)).Scan(&data)
	return data, err
}
func AdminSubjectMappings(ctx context.Context, pool persistence.TxBeginner, s ActiveSession, userID *uuid.UUID, subject *string, after *uuid.UUID, limit int) ([]json.RawMessage, error) {
	result := []json.RawMessage{}
	if limit < 1 || limit > 101 || s.Mode != "full" || s.MustReenrollMFA {
		return nil, ErrSessionInvalid
	}
	err := persistence.WithTenantTx(ctx, pool, s.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT identity.admin_subject_mappings($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, s.TenantID, s.UserID, s.SessionID, s.CredentialVersion, s.AuthorizationVersion, s.SessionVersion, userID, subject, after, limit)
		if err != nil {
			return fmt.Errorf("list subject mappings: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var data json.RawMessage
			if err := rows.Scan(&data); err != nil {
				return err
			}
			result = append(result, data)
		}
		return rows.Err()
	})
	return result, err
}
