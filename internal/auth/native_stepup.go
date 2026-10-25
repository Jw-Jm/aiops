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

// NativeStepUpIntentFromContext returns the intent a business boundary bound for
// this request. It exists so the boundary itself can be asserted without
// reaching into unexported context keys.
func NativeStepUpIntentFromContext(ctx context.Context) (NativeStepUpIntent, bool) {
	intent, ok := ctx.Value(nativeStepUpIntentKey{}).(NativeStepUpIntent)
	return intent, ok
}

// NativeProof is the accepted native step-up proof for one exact operation and
// target. A business boundary that must authorize the same operation again
// without a browser session (asynchronous dispatch, runner claim) freezes this
// reference together with its command and re-proves it from the database.
type NativeProof struct {
	SessionID     uuid.UUID
	Ref           uuid.UUID
	Operation     string
	BindingDigest []byte
	ExpiresAt     time.Time
}

type verifiedNativeStepUpKey struct{}

// WithVerifiedNativeStepUp marks a transaction in which the caller has already
// re-proved a persisted native proof. It is only set by platform code that just
// verified the frozen reference against the database; a request header can never
// reach it. The policy decision in the same transaction still re-checks the
// operation and digest before asserting step-up.
func WithVerifiedNativeStepUp(ctx context.Context, proof NativeProof) context.Context {
	return context.WithValue(ctx, verifiedNativeStepUpKey{}, proof)
}

func VerifiedNativeStepUpFromContext(ctx context.Context) (NativeProof, bool) {
	proof, ok := ctx.Value(verifiedNativeStepUpKey{}).(NativeProof)
	return proof, ok
}

func touchNativeStepUp(ctx context.Context, tx pgx.Tx, request RequestContext) (StepUpSession, NativeProof, error) {
	native, ok := NativeSessionFromContext(ctx)
	intent, bound := ctx.Value(nativeStepUpIntentKey{}).(NativeStepUpIntent)
	s := native.Session
	if !ok || !bound || intent.ChallengeID == uuid.Nil || intent.Operation == "" || s.TenantID != request.TenantID || s.Subject != request.Subject || s.Mode != "full" || s.MustReenrollMFA {
		return StepUpSession{}, NativeProof{}, ErrStepUpInvalid
	}
	var ref uuid.UUID
	var operation string
	var digest []byte
	var methods []string
	var idle, absolute time.Time
	err := tx.QueryRow(ctx, `SELECT out_step_up_ref,out_operation,out_binding_digest,out_verified_methods,out_idle_expires_at,out_absolute_expires_at FROM identity.step_up_proof_for_challenge($1,$2,$3,$4)`, s.TenantID, s.UserID, s.SessionID, intent.ChallengeID).Scan(&ref, &operation, &digest, &methods, &idle, &absolute)
	if errors.Is(err, pgx.ErrNoRows) {
		return StepUpSession{}, NativeProof{}, ErrStepUpInvalid
	}
	if err != nil {
		return StepUpSession{}, NativeProof{}, err
	}
	want := identity.StepUpBindingDigest(identity.SessionRef(s.SessionID), intent.Operation, intent.Target, s.SessionVersion, s.CredentialVersion, s.AuthorizationVersion)
	if operation != intent.Operation || !identity.ConstantTimeDigest(digest, want) {
		return StepUpSession{}, NativeProof{}, ErrStepUpInvalid
	}
	var accepted bool
	err = tx.QueryRow(ctx, `SELECT identity.step_up_recheck($1,$2,$3,$4,$5,$6,$7,$8,$9,600)`, s.TenantID, s.UserID, s.SessionID, ref, intent.Operation, want, s.AuthorizationVersion, s.CredentialVersion, s.SessionVersion).Scan(&accepted)
	if err != nil {
		return StepUpSession{}, NativeProof{}, err
	}
	if !accepted {
		return StepUpSession{}, NativeProof{}, ErrStepUpInvalid
	}
	_, err = audit.Append(ctx, tx, audit.Entry{TenantID: s.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "auth.native_step_up.used", EntityKind: "step_up_session", EntityID: ref, Subject: s.Subject, Payload: map[string]any{"operation": operation, "challengeId": intent.ChallengeID.String(), "bindingDigest": "sha256:" + identity.DigestHex(want)}})
	expires := idle
	if absolute.Before(expires) {
		expires = absolute
	}
	return StepUpSession{SessionID: ref, TenantID: s.TenantID, Subject: s.Subject, ACR: StepUpACRLevel2, CreatedAt: absolute.Add(-identity.StepUpAbsoluteLifetime), LastUsedAt: idle.Add(-identity.StepUpIdleLifetime)},
		NativeProof{SessionID: s.SessionID, Ref: ref, Operation: operation, BindingDigest: append([]byte(nil), want...), ExpiresAt: expires}, err
}
