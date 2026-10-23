package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/identity"
)

// Set only by a business boundary after deriving the exact operation target.
// A proof header alone never establishes assurance or authorizes a write.
type NativeStepUpIntent struct {
	ChallengeID uuid.UUID
	Operation   string
	Target      json.RawMessage
}
type nativeStepUpIntentKey struct{}

func WithNativeStepUpIntent(ctx context.Context, intent NativeStepUpIntent) context.Context {
	return context.WithValue(ctx, nativeStepUpIntentKey{}, intent)
}

func touchNativeStepUp(ctx context.Context, tx pgx.Tx, request RequestContext) (StepUpSession, error) {
	native, ok := NativeSessionFromContext(ctx)
	intent, bound := ctx.Value(nativeStepUpIntentKey{}).(NativeStepUpIntent)
	s := native.Session
	if !ok || !bound || intent.ChallengeID == uuid.Nil || intent.Operation == "" || s.TenantID != request.TenantID || s.Subject != request.Subject || s.Mode != "full" || s.MustReenrollMFA {
		return StepUpSession{}, ErrStepUpInvalid
	}
	var ref uuid.UUID
	var operation string
	var digest []byte
	var methods []string
	var idle, absolute time.Time
	err := tx.QueryRow(ctx, `SELECT out_step_up_ref,out_operation,out_binding_digest,out_verified_methods,out_idle_expires_at,out_absolute_expires_at FROM identity.step_up_proof_for_challenge($1,$2,$3,$4)`, s.TenantID, s.UserID, s.SessionID, intent.ChallengeID).Scan(&ref, &operation, &digest, &methods, &idle, &absolute)
	if errors.Is(err, pgx.ErrNoRows) {
		return StepUpSession{}, ErrStepUpInvalid
	}
	if err != nil {
		return StepUpSession{}, err
	}
	want := identity.StepUpBindingDigest(identity.SessionRef(s.SessionID), intent.Operation, intent.Target, s.SessionVersion, s.CredentialVersion, s.AuthorizationVersion)
	if operation != intent.Operation || !identity.ConstantTimeDigest(digest, want) {
		return StepUpSession{}, ErrStepUpInvalid
	}
	var accepted bool
	err = tx.QueryRow(ctx, `SELECT identity.step_up_recheck($1,$2,$3,$4,$5,$6,$7,$8,$9,600)`, s.TenantID, s.UserID, s.SessionID, ref, intent.Operation, want, s.AuthorizationVersion, s.CredentialVersion, s.SessionVersion).Scan(&accepted)
	if err != nil {
		return StepUpSession{}, err
	}
	if !accepted {
		return StepUpSession{}, ErrStepUpInvalid
	}
	_, err = audit.Append(ctx, tx, audit.Entry{TenantID: s.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "auth.native_step_up.used", EntityKind: "step_up_session", EntityID: ref, Subject: s.Subject, Payload: map[string]any{"operation": operation, "challengeId": intent.ChallengeID.String(), "bindingDigest": "sha256:" + identity.DigestHex(want)}})
	return StepUpSession{SessionID: ref, TenantID: s.TenantID, Subject: s.Subject, ACR: StepUpACRLevel2, CreatedAt: absolute.Add(-identity.StepUpAbsoluteLifetime), LastUsedAt: idle.Add(-identity.StepUpIdleLifetime)}, err
}
