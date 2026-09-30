package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
)

const (
	StepUpLifetime  = time.Hour
	StepUpACRLevel2 = "urn:ops:loa:2"
)

var ErrStepUpInvalid = errors.New("step-up session is missing, mismatched, revoked, or expired")

type StepUpSession struct {
	SessionID   uuid.UUID
	TenantID    uuid.UUID
	Subject     string
	KeycloakSID string
	ACR         string
	AuthTime    time.Time
	CreatedAt   time.Time
	LastUsedAt  time.Time
	RevokedAt   *time.Time
}

func ValidateStepUp(session StepUpSession, now time.Time) error {
	if session.SessionID == uuid.Nil || session.TenantID == uuid.Nil || session.Subject == "" ||
		session.KeycloakSID == "" || session.ACR == "" || session.AuthTime.IsZero() ||
		session.CreatedAt.IsZero() || session.LastUsedAt.IsZero() || session.RevokedAt != nil ||
		now.Before(session.CreatedAt) || now.Before(session.LastUsedAt) || now.Before(session.AuthTime) ||
		!now.Before(session.CreatedAt.Add(StepUpLifetime)) || !now.Before(session.LastUsedAt.Add(StepUpLifetime)) {
		return ErrStepUpInvalid
	}
	return nil
}

func ValidateStepUpForContext(session StepUpSession, request RequestContext, now time.Time) error {
	if !matchesStepUpContext(session, request) {
		return ErrStepUpInvalid
	}
	return ValidateStepUp(session, now)
}

func matchesStepUpContext(session StepUpSession, request RequestContext) bool {
	return session.TenantID == request.TenantID && session.Subject == request.Subject &&
		session.KeycloakSID == request.KeycloakSID && session.ACR == request.ACR &&
		session.AuthTime.Equal(request.AuthTime)
}

// RecordStepUpSession binds a freshly verified Keycloak reauthentication to the current
// tenant and subject. The caller must use the same tenant transaction for the associated audit.
func RecordStepUpSession(ctx context.Context, tx pgx.Tx, request RequestContext, sessionID uuid.UUID, acceptedACR []string) (StepUpSession, error) {
	if request.TenantID == uuid.Nil || request.Subject == "" || request.KeycloakSID == "" || request.AuthTime.IsZero() ||
		sessionID == uuid.Nil || !containsString(acceptedACR, request.ACR) {
		return StepUpSession{}, ErrStepUpInvalid
	}
	var session StepUpSession
	err := tx.QueryRow(ctx, `
		INSERT INTO platform.step_up_sessions
			(tenant_id, session_id, subject, keycloak_sid, acr, auth_time, created_at, last_used_at)
		SELECT $1, $2, $3, $4, $5, $6, $6, clock_timestamp()
		WHERE $6 <= clock_timestamp() + interval '30 seconds'
		  AND $6 >= clock_timestamp() - interval '5 minutes'
		ON CONFLICT (tenant_id, subject, keycloak_sid) DO UPDATE SET
			session_id = EXCLUDED.session_id, acr = EXCLUDED.acr, auth_time = EXCLUDED.auth_time,
			created_at = EXCLUDED.created_at, last_used_at = clock_timestamp(), revoked_at = NULL
		RETURNING session_id, tenant_id, subject, keycloak_sid, acr, auth_time, created_at, last_used_at, revoked_at`,
		request.TenantID, sessionID, request.Subject, request.KeycloakSID, request.ACR, request.AuthTime,
	).Scan(&session.SessionID, &session.TenantID, &session.Subject, &session.KeycloakSID, &session.ACR,
		&session.AuthTime, &session.CreatedAt, &session.LastUsedAt, &session.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StepUpSession{}, ErrStepUpInvalid
	}
	if err != nil {
		return StepUpSession{}, fmt.Errorf("record step-up session: %w", err)
	}
	if err := appendStepUpAudit(ctx, tx, session, "auth.step_up.created"); err != nil {
		return StepUpSession{}, err
	}
	return session, nil
}

// TouchStepUpSession atomically revalidates identity, tenant, ACR, and both expiry windows using
// PostgreSQL time before moving the idle-expiry clock forward.
func TouchStepUpSession(ctx context.Context, tx pgx.Tx, session StepUpSession, request RequestContext) (StepUpSession, error) {
	if !matchesStepUpContext(session, request) || session.SessionID == uuid.Nil {
		return StepUpSession{}, ErrStepUpInvalid
	}
	err := tx.QueryRow(ctx, `
		UPDATE platform.step_up_sessions
		SET last_used_at = clock_timestamp()
		WHERE tenant_id = $1 AND session_id = $2 AND subject = $3 AND keycloak_sid = $4
		  AND acr = $5 AND auth_time = $6 AND revoked_at IS NULL
		  AND created_at + interval '1 hour' > clock_timestamp()
		  AND last_used_at + interval '1 hour' > clock_timestamp()
		RETURNING session_id, tenant_id, subject, keycloak_sid, acr, auth_time, created_at, last_used_at, revoked_at`,
		session.TenantID, session.SessionID, request.Subject, request.KeycloakSID, request.ACR, request.AuthTime,
	).Scan(&session.SessionID, &session.TenantID, &session.Subject, &session.KeycloakSID, &session.ACR,
		&session.AuthTime, &session.CreatedAt, &session.LastUsedAt, &session.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StepUpSession{}, ErrStepUpInvalid
	}
	if err != nil {
		return StepUpSession{}, fmt.Errorf("touch step-up session: %w", err)
	}
	if err := appendStepUpAudit(ctx, tx, session, "auth.step_up.used"); err != nil {
		return StepUpSession{}, err
	}
	return session, nil
}

// TouchCurrentStepUpSession resolves the session exclusively from the verified request identity,
// then applies the accepted-ACR check and database-time expiry update in the caller's transaction.
func TouchCurrentStepUpSession(ctx context.Context, tx pgx.Tx, request RequestContext, acceptedACRs []string) (StepUpSession, error) {
	if request.TenantID == uuid.Nil || request.Subject == "" || request.KeycloakSID == "" || request.AuthTime.IsZero() ||
		!containsString(acceptedACRs, request.ACR) {
		return StepUpSession{}, ErrStepUpInvalid
	}
	var session StepUpSession
	err := tx.QueryRow(ctx, `
		SELECT session_id, tenant_id, subject, keycloak_sid, acr, auth_time, created_at, last_used_at, revoked_at
		FROM platform.step_up_sessions
		WHERE tenant_id = $1 AND subject = $2 AND keycloak_sid = $3 AND acr = $4 AND auth_time = $5 AND revoked_at IS NULL`,
		request.TenantID, request.Subject, request.KeycloakSID, request.ACR, request.AuthTime,
	).Scan(&session.SessionID, &session.TenantID, &session.Subject, &session.KeycloakSID, &session.ACR,
		&session.AuthTime, &session.CreatedAt, &session.LastUsedAt, &session.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StepUpSession{}, ErrStepUpInvalid
	}
	if err != nil {
		return StepUpSession{}, fmt.Errorf("load current step-up session: %w", err)
	}
	return TouchStepUpSession(ctx, tx, session, request)
}

func appendStepUpAudit(ctx context.Context, tx pgx.Tx, session StepUpSession, eventType string) error {
	sidDigest := sha256.Sum256([]byte(session.KeycloakSID))
	_, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: session.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: eventType,
		EntityKind: "step_up_session", EntityID: session.SessionID, Subject: session.Subject,
		Payload: map[string]any{
			"acr": session.ACR, "auth_time": session.AuthTime.UTC().Format(time.RFC3339Nano),
			"keycloak_sid_sha256": "sha256:" + hex.EncodeToString(sidDigest[:]),
		},
	})
	if err != nil {
		return fmt.Errorf("audit step-up session: %w", err)
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
