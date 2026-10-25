package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/bundle"
	"ops-platform/internal/persistence"
)

// Native MFA, step-up challenges and recovery (P2-03).
//
// Every operation is a fixed SECURITY DEFINER function; the plaintext TOTP
// secret exists only between the creation response and the sealing call, and
// only its ciphertext is persisted. Nothing in this file can raise a session to
// LoA2: the database only marks a challenge verified when password and TOTP were
// both accumulated, and only a verified challenge can mint a step-up session.

var (
	ErrMFAChallengeInvalid = errors.New("step-up challenge is not usable")
	ErrMFAVerification     = errors.New("verification code was not accepted")
	ErrMFATargetMismatch   = errors.New("target confirmation does not match")
	ErrMFANotEnrolled      = errors.New("the account has no confirmed second factor")
	// ErrMFAStepUpBudget means the account/factor failure budget is locked. It is
	// a bounded, retryable capacity answer, never a credential verdict.
	ErrMFAStepUpBudget = errors.New("step-up attempt budget is exhausted")
	// ErrMFAResetCredentialUnavailable means an administrator MFA reset found no
	// active password credential at the account's current version. The reset is
	// refused instead of guessing which historical hash to preserve.
	ErrMFAResetCredentialUnavailable = errors.New("the account has no active password credential to preserve")
)

// StepUpBudgetError carries the server-computed cooldown so the HTTP layer can
// send an accurate Retry-After header without duplicating the policy.
type StepUpBudgetError struct {
	RetryAfter time.Duration
}

func (e *StepUpBudgetError) Error() string { return ErrMFAStepUpBudget.Error() }
func (e *StepUpBudgetError) Unwrap() error { return ErrMFAStepUpBudget }

const (
	StepUpOperationMFAEnrollment = "mfa_enrollment"
	StepUpOperationMFAReset      = "mfa_reset"
	StepUpOperationRecovery      = "recovery_code_regenerate"

	StepUpIdleLifetime     = 10 * time.Minute
	StepUpAbsoluteLifetime = 15 * time.Minute
	mfaEnrollmentLifetime  = 10 * time.Minute
	// StepUpBudgetCooldown is the fixed server-side lock applied after five
	// failures inside fifteen minutes. It mirrors the native login lock.
	StepUpBudgetCooldown = 15 * time.Minute

	stepUpFactorPassword = "password"
	stepUpFactorTOTP     = "totp"
)

var stepUpOperations = map[string]struct{}{
	"command_execution": {}, "user_administration": {}, "mfa_enrollment": {},
	"mfa_reset": {}, "recovery_code_regenerate": {}, "source_administration": {},
	"cluster_administration": {}, "registry_administration": {},
	"execution_profile_administration": {},
}

type MFAEnrollment struct {
	ID          uuid.UUID
	State       string
	Revision    int
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConfirmedAt *time.Time
}

type MFAStatus struct {
	State                  string
	EnrollmentID           *uuid.UUID
	ConfirmedAt            *time.Time
	RecoveryCodesRemaining int
}

type StepUpChallenge struct {
	ID               uuid.UUID
	SessionID        uuid.UUID
	Operation        string
	Target           json.RawMessage
	BindingDigest    []byte
	State            string
	Revision         int
	RequiredMethods  []string
	CompletedMethods []string
	Attempts         int
	AttemptLimit     int
	// BudgetRemaining is the account/factor failure budget shared by every
	// challenge, session and API instance. It is never larger than the
	// per-challenge remainder and is nil when it could not be read.
	BudgetRemaining            *int
	IssuedCredentialVersion    int
	IssuedAuthorizationVersion int
	IssuedSessionVersion       int
	CreatedAt                  time.Time
	IdleExpiresAt              time.Time
	AbsoluteExpiresAt          time.Time
	VerifiedAt                 *time.Time
}

type StepUpProof struct {
	Ref               uuid.UUID
	Operation         string
	BindingDigest     []byte
	VerifiedMethods   []string
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

type ConfirmResult struct {
	EnrollmentID      uuid.UUID
	ConfirmedAt       time.Time
	Revision          int
	CredentialVersion int
}

type RecoveryResult struct {
	Verified        bool
	Remaining       int
	SessionsRevoked int
	MustReenrollMFA bool
}

type AdminResetResult struct {
	CredentialVersion    int
	SessionVersion       int
	AuthorizationVersion int
	SessionsRevoked      int
	EnrollmentsRemoved   int
	RecoveryCodesRemoved int
}

type MFARepository struct {
	sessions *SessionRepository
	sealer   SecretSealer
	now      func() time.Time
}

func NewMFARepository(sessions *SessionRepository, sealer SecretSealer) (*MFARepository, error) {
	if sessions == nil || sessions.pool == nil {
		return nil, ErrRepositoryRequired
	}
	if sealer == nil {
		return nil, ErrSecretBackendUnavailable
	}
	return &MFARepository{sessions: sessions, sealer: sealer, now: time.Now}, nil
}

func (r *MFARepository) Now() time.Time { return r.now() }

// budgetState reads the account/factor failure budget. Creating or replacing a
// challenge and every factor verification consult it before any work is done.
func (r *MFARepository) budgetState(ctx context.Context, tenantID, userID uuid.UUID) (bool, int, *time.Time, error) {
	if tenantID == uuid.Nil || userID == uuid.Nil {
		return false, 0, nil, ErrSessionInvalid
	}
	var allowed bool
	var retryAt *time.Time
	var remaining int
	err := r.sessions.pool.QueryRow(ctx, `SELECT out_allowed, out_retry_at, out_remaining
		FROM identity.step_up_budget_check($1,$2)`, tenantID, userID).
		Scan(&allowed, &retryAt, &remaining)
	if err != nil {
		return false, 0, nil, mapMFADBError("read step-up failure budget", err)
	}
	if remaining < 0 {
		remaining = 0
	}
	return allowed, remaining, retryAt, nil
}

// budgetDenial turns a locked budget into the bounded retryable result. The
// cooldown comes from the database row so both API instances agree.
func budgetDenial(retryAt *time.Time) error {
	wait := StepUpBudgetCooldown
	if retryAt != nil {
		remaining := time.Until(*retryAt)
		switch {
		case remaining <= 0:
			// The row is already past its cooldown: the next call may succeed.
			wait = time.Second
		case remaining < wait:
			wait = remaining
		}
	}
	if wait < time.Second {
		wait = time.Second
	}
	return &StepUpBudgetError{RetryAfter: wait.Round(time.Second)}
}

// StepUpBindingDigest binds one challenge to the exact session, operation,
// target and version set. The same value is recomputed by the high-risk write,
// so a challenge cannot be replayed against another object or revision.
func StepUpBindingDigest(sessionRef, operation string, target json.RawMessage, sessionVersion, credentialVersion, authorizationVersion int) []byte {
	canonical := canonicalJSON(target)
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"ops-stepup-binding/v1|%s|%s|%s|%d|%d|%d",
		sessionRef, operation, canonical, sessionVersion, credentialVersion, authorizationVersion)))
	return sum[:]
}

func canonicalJSON(raw json.RawMessage) string {
	encoded, err := bundle.CanonicalizeJSON(raw)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}

// SessionRef is the contract-visible reference for a native session.
func SessionRef(sessionID uuid.UUID) string { return "session:" + sessionID.String() }

func validateTarget(operation string, target map[string]any) error {
	kind, _ := target["kind"].(string)
	allowed := map[string]string{
		"command_execution":                "k8s_object",
		"user_administration":              "user",
		"mfa_enrollment":                   "mfa_enrollment",
		"mfa_reset":                        "user",
		"recovery_code_regenerate":         "user",
		"source_administration":            "source_registration",
		"cluster_administration":           "cluster_registration",
		"registry_administration":          "registry_object",
		"execution_profile_administration": "execution_profile",
	}
	if kind == "administrative_write" {
		if operation != "user_administration" && operation != "source_administration" && operation != "cluster_administration" && operation != "registry_administration" && operation != "execution_profile_administration" {
			return ErrMFAChallengeInvalid
		}
		write, ok := target["write"].(map[string]any)
		if !ok || len(target) != 2 || len(write) != 3 {
			return ErrMFAChallengeInvalid
		}
		method, _ := write["method"].(string)
		path, _ := write["path"].(string)
		digest, _ := write["bodyDigest"].(string)
		if (method != "POST" && method != "PATCH" && method != "DELETE") || !strings.HasPrefix(path, "/api/v1/admin/") || strings.ContainsAny(path, "?#") || len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
			return ErrMFAChallengeInvalid
		}
		if _, err := hex.DecodeString(digest[7:]); err != nil {
			return ErrMFAChallengeInvalid
		}
		return nil
	}
	expected, ok := allowed[operation]
	if !ok || kind != expected {
		return ErrMFAChallengeInvalid
	}
	revision, ok := target["revision"].(float64)
	if value, isInt := target["revision"].(int); isInt {
		revision = float64(value)
		ok = true
	}
	if !ok || revision < 1 || revision > 2147483647 || revision != float64(int(revision)) {
		return ErrMFAChallengeInvalid
	}
	return nil
}

// ---------------------------------------------------------------------------
// Enrolment
// ---------------------------------------------------------------------------

func (r *MFARepository) StartEnrollment(ctx context.Context, session ActiveSession) (MFAEnrollment, string, error) {
	if session.TenantID == uuid.Nil || session.UserID == uuid.Nil || session.SessionID == uuid.Nil ||
		session.Mode != "full" || session.CredentialVersion < 1 || session.SessionVersion < 1 {
		return MFAEnrollment{}, "", ErrSessionInvalid
	}
	secret, err := GenerateTOTPSecret()
	if err != nil {
		return MFAEnrollment{}, "", err
	}
	enrollmentID := uuid.Must(uuid.NewV7())
	envelope, err := r.sealer.Seal(ctx, session.TenantID, session.UserID, enrollmentID, []byte(secret))
	if err != nil {
		return MFAEnrollment{}, "", err
	}
	expiresAt := r.now().UTC().Add(mfaEnrollmentLifetime)
	var enrollment MFAEnrollment
	err = persistence.WithTenantTx(ctx, r.sessions.pool, session.TenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT out_revision, out_expires_at
			FROM identity.mfa_begin_enrollment($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			session.TenantID, session.UserID, session.SessionID, enrollmentID,
			envelope.Ciphertext, envelope.KeyRef, envelope.KeyVersion,
			session.CredentialVersion, session.SessionVersion, expiresAt,
		).Scan(&enrollment.Revision, &enrollment.ExpiresAt); err != nil {
			return mapMFADBError("begin mfa enrollment", err)
		}
		_, err := audit.Append(ctx, tx, audit.Entry{
			TenantID: session.TenantID, RecordID: uuid.Must(uuid.NewV7()),
			EventType: "identity.mfa.enrollment_started", EntityKind: "mfa_enrollment",
			EntityID: enrollmentID, Subject: session.Subject,
			Payload: map[string]any{"method": "totp"},
		})
		return err
	})
	if err != nil {
		return MFAEnrollment{}, "", err
	}
	enrollment.ID = enrollmentID
	enrollment.State = "pending_confirmation"
	enrollment.CreatedAt = r.now().UTC()
	return enrollment, TOTPURI("智能运维平台", session.LoginName, secret), nil
}

func (r *MFARepository) EnrollmentView(ctx context.Context, tenantID, userID, enrollmentID uuid.UUID) (MFAEnrollment, error) {
	if tenantID == uuid.Nil || userID == uuid.Nil || enrollmentID == uuid.Nil {
		return MFAEnrollment{}, ErrSessionInvalid
	}
	var enrollment MFAEnrollment
	err := r.sessions.pool.QueryRow(ctx, `SELECT out_enrollment_id, out_state, out_revision,
		out_created_at, out_expires_at, out_confirmed_at
		FROM identity.mfa_enrollment_view($1,$2,$3)`,
		tenantID, userID, enrollmentID,
	).Scan(&enrollment.ID, &enrollment.State, &enrollment.Revision,
		&enrollment.CreatedAt, &enrollment.ExpiresAt, &enrollment.ConfirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MFAEnrollment{}, ErrSessionNotFound
	}
	if err != nil {
		return MFAEnrollment{}, mapMFADBError("read mfa enrollment", err)
	}
	return enrollment, nil
}

func (r *MFARepository) Status(ctx context.Context, tenantID, userID uuid.UUID) (MFAStatus, error) {
	if tenantID == uuid.Nil || userID == uuid.Nil {
		return MFAStatus{}, ErrSessionInvalid
	}
	var status MFAStatus
	var enrollmentID *uuid.UUID
	err := r.sessions.pool.QueryRow(ctx, `SELECT out_state, out_enrollment_id,
		out_confirmed_at, out_recovery_codes_remaining FROM identity.mfa_state($1,$2)`,
		tenantID, userID,
	).Scan(&status.State, &enrollmentID, &status.ConfirmedAt, &status.RecoveryCodesRemaining)
	if err != nil {
		return MFAStatus{}, mapMFADBError("read mfa state", err)
	}
	status.EnrollmentID = enrollmentID
	return status, nil
}

func (r *MFARepository) pendingSecret(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, tenantID, userID, enrollmentID uuid.UUID) (string, SecretEnvelope, int, time.Time, error) {
	var envelope SecretEnvelope
	var revision int
	var expiresAt time.Time
	if err := q.QueryRow(ctx, `SELECT out_ciphertext, out_key_ref, out_key_version,
		out_revision, out_expires_at FROM identity.mfa_enrollment_secret($1,$2,$3,'pending_confirmation')`,
		tenantID, userID, enrollmentID,
	).Scan(&envelope.Ciphertext, &envelope.KeyRef, &envelope.KeyVersion, &revision, &expiresAt); err != nil {
		return "", SecretEnvelope{}, 0, time.Time{}, mapMFADBError("read pending mfa secret", err)
	}
	secret, err := r.sealer.Open(ctx, tenantID, userID, enrollmentID, envelope)
	if err != nil {
		return "", SecretEnvelope{}, 0, time.Time{}, err
	}
	return string(secret), envelope, revision, expiresAt, nil
}

// ConfirmEnrollment completes a pending enrolment. It requires a verified
// mfa_enrollment step-up bound to this session and to this enrolment revision,
// then re-verifies the new TOTP code and issues the recovery codes once.
func (r *MFARepository) ConfirmEnrollment(ctx context.Context, session ActiveSession, enrollmentID uuid.UUID, expectedRevision int, totpCode string, proof StepUpProof) (ConfirmResult, []string, error) {
	if session.Mode != "full" || proof.Ref == uuid.Nil || proof.Operation != StepUpOperationMFAEnrollment {
		return ConfirmResult{}, nil, ErrMFAChallengeInvalid
	}
	secret, _, revision, expiresAt, err := r.pendingSecret(ctx, r.sessions.pool, session.TenantID, session.UserID, enrollmentID)
	if err != nil {
		return ConfirmResult{}, nil, err
	}
	if revision != expectedRevision || r.now().UTC().After(expiresAt) {
		return ConfirmResult{}, nil, ErrSessionVersion
	}
	step, ok := VerifyTOTP(secret, totpCode, r.now().UTC())
	if !ok {
		return ConfirmResult{}, nil, ErrMFAVerification
	}
	codes, err := NewRecoveryCodes()
	if err != nil {
		return ConfirmResult{}, nil, err
	}
	digests := make([][]byte, 0, len(codes))
	for _, code := range codes {
		digests = append(digests, DigestRecoveryCode(code))
	}
	target, _ := json.Marshal(map[string]any{
		"kind": "mfa_enrollment", "enrollmentId": enrollmentID.String(),
		"revision": expectedRevision,
	})
	wantDigest := StepUpBindingDigest(SessionRef(session.SessionID), StepUpOperationMFAEnrollment,
		target, session.SessionVersion, session.CredentialVersion, session.AuthorizationVersion)
	var result ConfirmResult
	err = persistence.WithTenantTx(ctx, r.sessions.pool, session.TenantID, func(tx pgx.Tx) error {
		var rechecked bool
		if err := tx.QueryRow(ctx, `SELECT identity.step_up_recheck($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			session.TenantID, session.UserID, session.SessionID, proof.Ref,
			StepUpOperationMFAEnrollment, wantDigest,
			session.AuthorizationVersion, session.CredentialVersion, session.SessionVersion,
			int(StepUpIdleLifetime.Seconds()),
		).Scan(&rechecked); err != nil {
			return mapMFADBError("recheck mfa enrollment step-up", err)
		}
		if !rechecked {
			return ErrMFAChallengeInvalid
		}
		if err := tx.QueryRow(ctx, `SELECT out_revision, out_confirmed_at
			FROM identity.mfa_confirm_enrollment($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			session.TenantID, session.UserID, session.SessionID, enrollmentID,
			expectedRevision, session.CredentialVersion, session.SessionVersion,
			step, digests,
		).Scan(&result.Revision, &result.ConfirmedAt); err != nil {
			return mapMFADBError("confirm mfa enrollment", err)
		}
		_, err := audit.Append(ctx, tx, audit.Entry{
			TenantID: session.TenantID, RecordID: uuid.Must(uuid.NewV7()),
			EventType: "identity.mfa.enrollment_confirmed", EntityKind: "mfa_enrollment",
			EntityID: enrollmentID, Subject: session.Subject,
			Payload: map[string]any{"method": "totp", "recoveryCodesIssued": len(codes)},
		})
		return err
	})
	if err != nil {
		return ConfirmResult{}, nil, err
	}
	result.EnrollmentID = enrollmentID
	result.CredentialVersion = session.CredentialVersion
	return result, codes, nil
}

// ---------------------------------------------------------------------------
// Step-up challenges
// ---------------------------------------------------------------------------

func (r *MFARepository) BeginChallenge(ctx context.Context, session ActiveSession, operation string, target map[string]any) (StepUpChallenge, error) {
	if _, ok := stepUpOperations[operation]; !ok || session.Mode != "full" {
		return StepUpChallenge{}, ErrMFAChallengeInvalid
	}
	if err := validateTarget(operation, target); err != nil {
		return StepUpChallenge{}, err
	}
	// A new or replacement challenge never resets the account budget: refuse to
	// create one while any required factor is cooling down. Recovery codes keep
	// their own account lock and must stay usable.
	var budgetRemaining *int
	if operation != StepUpOperationRecovery {
		allowed, remaining, retryAt, budgetErr := r.budgetState(ctx, session.TenantID, session.UserID)
		if budgetErr != nil {
			return StepUpChallenge{}, budgetErr
		}
		if !allowed {
			return StepUpChallenge{}, budgetDenial(retryAt)
		}
		budgetRemaining = &remaining
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return StepUpChallenge{}, ErrMFAChallengeInvalid
	}
	digest := StepUpBindingDigest(SessionRef(session.SessionID), operation, encoded,
		session.SessionVersion, session.CredentialVersion, session.AuthorizationVersion)
	challengeID := uuid.Must(uuid.NewV7())
	now := r.now().UTC()
	var challenge StepUpChallenge
	err = r.sessions.pool.QueryRow(ctx, `SELECT out_challenge_id, out_revision,
		out_attempt_limit, out_created_at, out_idle_expires_at, out_absolute_expires_at
		FROM identity.step_up_challenge_create($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		session.TenantID, session.UserID, session.SessionID, challengeID, operation,
		encoded, digest, session.CredentialVersion, session.AuthorizationVersion,
		session.SessionVersion,
		now.Add(StepUpIdleLifetime), now.Add(StepUpAbsoluteLifetime),
	).Scan(&challenge.ID, &challenge.Revision, &challenge.AttemptLimit,
		&challenge.CreatedAt, &challenge.IdleExpiresAt, &challenge.AbsoluteExpiresAt)
	if err != nil {
		return StepUpChallenge{}, mapMFADBError("create step-up challenge", err)
	}
	challenge.SessionID = session.SessionID
	challenge.Operation = operation
	challenge.Target = encoded
	challenge.BindingDigest = digest
	challenge.State = "pending"
	challenge.RequiredMethods = []string{"password", "totp"}
	challenge.CompletedMethods = []string{}
	challenge.BudgetRemaining = budgetRemaining
	return challenge, nil
}

func (r *MFARepository) Challenge(ctx context.Context, tenantID, userID, challengeID uuid.UUID) (StepUpChallenge, error) {
	var challenge StepUpChallenge
	var completed []string
	var required []string
	err := r.sessions.pool.QueryRow(ctx, `SELECT out_challenge_id, out_session_id,
		out_operation, out_target, out_binding_digest, out_state, out_revision,
		out_required_methods, out_completed_methods, out_attempts, out_attempt_limit,
		out_issued_credential_version, out_issued_authorization_version,
		out_issued_session_version, out_created_at, out_idle_expires_at,
		out_absolute_expires_at, out_verified_at
		FROM identity.step_up_challenge_get($1,$2,$3)`,
		tenantID, userID, challengeID,
	).Scan(&challenge.ID, &challenge.SessionID, &challenge.Operation, &challenge.Target,
		&challenge.BindingDigest, &challenge.State, &challenge.Revision,
		&required, &completed, &challenge.Attempts, &challenge.AttemptLimit,
		&challenge.IssuedCredentialVersion, &challenge.IssuedAuthorizationVersion,
		&challenge.IssuedSessionVersion, &challenge.CreatedAt, &challenge.IdleExpiresAt,
		&challenge.AbsoluteExpiresAt, &challenge.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StepUpChallenge{}, ErrSessionNotFound
	}
	if err != nil {
		return StepUpChallenge{}, mapMFADBError("read step-up challenge", err)
	}
	challenge.RequiredMethods = required
	challenge.CompletedMethods = completed
	if challenge.CompletedMethods == nil {
		challenge.CompletedMethods = []string{}
	}
	if challenge.Operation != StepUpOperationRecovery {
		allowed, remaining, _, budgetErr := r.budgetState(ctx, tenantID, userID)
		if budgetErr != nil {
			return StepUpChallenge{}, budgetErr
		}
		if !allowed {
			remaining = 0
		}
		challenge.BudgetRemaining = &remaining
	}
	return challenge, nil
}

// stepUpAttempt is the reservation returned by identity.step_up_attempt_begin.
// The attempt is already counted when it is returned, so the caller may compare
// a credential only after this call succeeded.
type stepUpAttempt struct {
	Revision         int
	State            string
	Remaining        int
	AttemptRemaining int
	RetryAt          *time.Time
}

func beginStepUpAttempt(ctx context.Context, tx pgx.Tx, session ActiveSession, challenge StepUpChallenge, method string) (stepUpAttempt, error) {
	var attempt stepUpAttempt
	err := tx.QueryRow(ctx, `SELECT out_revision, out_state, out_remaining, out_attempt_remaining, out_retry_at
		FROM identity.step_up_attempt_begin($1,$2,$3,$4,$5,$6)`,
		session.TenantID, session.UserID, challenge.ID, session.SessionID,
		challenge.Revision, method,
	).Scan(&attempt.Revision, &attempt.State, &attempt.Remaining, &attempt.AttemptRemaining, &attempt.RetryAt)
	if err != nil {
		return attempt, mapMFADBError("reserve step-up attempt", err)
	}
	return attempt, nil
}

func failStepUpAttempt(ctx context.Context, tx pgx.Tx, session ActiveSession, challenge StepUpChallenge, method string, expectedRevision int) (stepUpAttempt, error) {
	var attempt stepUpAttempt
	err := tx.QueryRow(ctx, `SELECT out_revision, out_state, out_remaining, out_attempt_remaining, out_retry_at
		FROM identity.step_up_attempt_failed($1,$2,$3,$4,$5,$6)`,
		session.TenantID, session.UserID, challenge.ID, session.SessionID, expectedRevision, method,
	).Scan(&attempt.Revision, &attempt.State, &attempt.Remaining, &attempt.AttemptRemaining, &attempt.RetryAt)
	if err != nil {
		return attempt, mapMFADBError("record failed step-up attempt", err)
	}
	return attempt, nil
}

func (r *MFARepository) addStepUpMethod(ctx context.Context, tx pgx.Tx, session ActiveSession, challenge StepUpChallenge, method string, expectedRevision int) (StepUpChallenge, error) {
	var revision int
	var state string
	var completed []string
	var verifiedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT out_revision, out_state, out_completed_methods, out_verified_at
		FROM identity.step_up_challenge_add_method($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		session.TenantID, session.UserID, challenge.ID, session.SessionID,
		expectedRevision, method, session.CredentialVersion, session.SessionVersion,
		minTime(r.now().UTC().Add(StepUpIdleLifetime), challenge.AbsoluteExpiresAt),
	).Scan(&revision, &state, &completed, &verifiedAt)
	if err != nil {
		return StepUpChallenge{}, mapMFADBError("record verified step-up method", err)
	}
	updated := challenge
	updated.Revision = revision
	updated.State = state
	updated.CompletedMethods = completed
	updated.VerifiedAt = verifiedAt
	return updated, nil
}

// VerifyPasswordStep verifies the account password inside one challenge. The
// reservation, the comparison and the outcome share one transaction that holds
// the account row lock, so two API instances, sessions or challenges cannot
// compare guesses in parallel or lose a failure to a later CAS.
func (r *MFARepository) VerifyPasswordStep(ctx context.Context, session ActiveSession, challenge StepUpChallenge, password string) (StepUpChallenge, error) {
	var (
		updated StepUpChallenge
		failed  bool
	)
	err := persistence.WithTenantTx(ctx, r.sessions.pool, session.TenantID, func(tx pgx.Tx) error {
		attempt, err := beginStepUpAttempt(ctx, tx, session, challenge, stepUpFactorPassword)
		if err != nil {
			return err
		}
		account, err := r.sessions.FindLoginAccountTx(ctx, tx, session.LoginName)
		if err != nil || account.TenantID != session.TenantID ||
			account.UserID != session.UserID || account.Status != "active" {
			return ErrMFAVerification
		}
		valid, verifyErr := VerifyPasswordContext(ctx, password, account.PasswordHash)
		switch {
		case errors.Is(verifyErr, ErrPasswordWorkBusy), errors.Is(verifyErr, context.Canceled),
			errors.Is(verifyErr, context.DeadlineExceeded):
			// No comparison happened. Returning an error rolls the reservation
			// back, so capacity or cancellation never consumes an attempt and is
			// never recorded as a wrong password.
			return verifyErr
		case verifyErr != nil:
			// A malformed stored hash fails closed and is counted as an attempt.
			valid = false
		}
		if !valid {
			result, failErr := failStepUpAttempt(ctx, tx, session, challenge, stepUpFactorPassword, attempt.Revision)
			if failErr != nil {
				return failErr
			}
			updated = challenge
			updated.State = result.State
			updated.BudgetRemaining = &result.Remaining
			failed = true
			return nil
		}
		updated, err = r.addStepUpMethod(ctx, tx, session, challenge, stepUpFactorPassword, attempt.Revision)
		return err
	})
	if err != nil {
		return StepUpChallenge{}, err
	}
	if failed {
		return StepUpChallenge{}, ErrMFAVerification
	}
	return updated, nil
}

// VerifyTOTPStep verifies a TOTP code against the secret the operation targets
// (the pending enrolment for mfa_enrollment, the confirmed factor otherwise) and
// consumes the time step so it cannot be replayed. Like the password step it runs
// reserve -> compare -> record inside one account-locked transaction.
func (r *MFARepository) VerifyTOTPStep(ctx context.Context, session ActiveSession, challenge StepUpChallenge, code string) (StepUpChallenge, error) {
	var target struct {
		Kind         string `json:"kind"`
		EnrollmentID string `json:"enrollmentId"`
	}
	_ = json.Unmarshal(challenge.Target, &target)
	var (
		updated StepUpChallenge
		failed  bool
	)
	err := persistence.WithTenantTx(ctx, r.sessions.pool, session.TenantID, func(tx pgx.Tx) error {
		attempt, err := beginStepUpAttempt(ctx, tx, session, challenge, stepUpFactorTOTP)
		if err != nil {
			return err
		}
		var enrollmentID uuid.UUID
		state := "confirmed"
		var confirmedState string
		var confirmedID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT out_state, out_enrollment_id
			FROM identity.mfa_state($1,$2)`, session.TenantID, session.UserID).
			Scan(&confirmedState, &confirmedID); err != nil {
			return mapMFADBError("read mfa state", err)
		}
		if challenge.Operation == StepUpOperationMFAEnrollment && confirmedState != "confirmed" {
			parsed, parseErr := uuid.Parse(target.EnrollmentID)
			if parseErr != nil {
				return ErrMFAChallengeInvalid
			}
			enrollmentID = parsed
			state = "pending_confirmation"
		} else {
			if confirmedState != "confirmed" || confirmedID == nil {
				return ErrMFANotEnrolled
			}
			enrollmentID = *confirmedID
		}
		var envelope SecretEnvelope
		var revision int
		var expiresAt time.Time
		if err := tx.QueryRow(ctx, `SELECT out_ciphertext, out_key_ref, out_key_version,
			out_revision, out_expires_at FROM identity.mfa_enrollment_secret($1,$2,$3,$4)`,
			session.TenantID, session.UserID, enrollmentID, state,
		).Scan(&envelope.Ciphertext, &envelope.KeyRef, &envelope.KeyVersion, &revision, &expiresAt); err != nil {
			return ErrMFANotEnrolled
		}
		secret, err := r.sealer.Open(ctx, session.TenantID, session.UserID, enrollmentID, envelope)
		if err != nil {
			return err
		}
		step, accepted := VerifyTOTP(string(secret), code, r.now().UTC())
		if accepted && state == "confirmed" {
			if err := tx.QueryRow(ctx, `SELECT identity.mfa_record_totp_step($1,$2,$3,$4)`,
				session.TenantID, session.UserID, enrollmentID, step).Scan(&accepted); err != nil {
				return mapMFADBError("consume totp step", err)
			}
		}
		if !accepted {
			result, failErr := failStepUpAttempt(ctx, tx, session, challenge, stepUpFactorTOTP, attempt.Revision)
			if failErr != nil {
				return failErr
			}
			updated = challenge
			updated.State = result.State
			updated.BudgetRemaining = &result.Remaining
			failed = true
			return nil
		}
		updated, err = r.addStepUpMethod(ctx, tx, session, challenge, stepUpFactorTOTP, attempt.Revision)
		return err
	})
	if err != nil {
		return StepUpChallenge{}, err
	}
	if failed {
		return StepUpChallenge{}, ErrMFAVerification
	}
	return updated, nil
}

func (r *MFARepository) MintStepUp(ctx context.Context, session ActiveSession, challenge StepUpChallenge) (StepUpProof, error) {
	ref := uuid.Must(uuid.NewV7())
	var proof StepUpProof
	err := r.sessions.pool.QueryRow(ctx, `SELECT out_step_up_ref, out_verified_methods,
		out_created_at, out_idle_expires_at, out_absolute_expires_at
		FROM identity.step_up_session_mint($1,$2,$3,$4,$5,$6,$7)`,
		session.TenantID, session.UserID, session.SessionID, challenge.ID, ref,
		int(StepUpIdleLifetime.Seconds()), int(StepUpAbsoluteLifetime.Seconds()),
	).Scan(&proof.Ref, &proof.VerifiedMethods, new(time.Time),
		&proof.IdleExpiresAt, &proof.AbsoluteExpiresAt)
	if err != nil {
		return StepUpProof{}, mapMFADBError("mint step-up session", err)
	}
	proof.Operation = challenge.Operation
	proof.BindingDigest = challenge.BindingDigest
	return proof, nil
}

// ActiveProof returns the current verified step-up proof for this session, which
// is what makes the session view report LoA2.
func (r *MFARepository) ActiveProof(ctx context.Context, session ActiveSession) (*StepUpProof, error) {
	var proof StepUpProof
	err := r.sessions.pool.QueryRow(ctx, `SELECT out_step_up_ref, out_operation,
		out_binding_digest, out_verified_methods, out_idle_expires_at, out_absolute_expires_at
		FROM identity.step_up_session_view($1,$2,$3)`,
		session.TenantID, session.UserID, session.SessionID,
	).Scan(&proof.Ref, &proof.Operation, &proof.BindingDigest,
		&proof.VerifiedMethods, &proof.IdleExpiresAt, &proof.AbsoluteExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapMFADBError("read step-up session", err)
	}
	return &proof, nil
}

// ---------------------------------------------------------------------------
// Recovery
// ---------------------------------------------------------------------------

func (r *MFARepository) ConsumeRecoveryCode(ctx context.Context, session ActiveSession, challengeID uuid.UUID, expectedRevision int, code string) (RecoveryResult, error) {
	normalized, ok := NormalizeRecoveryCode(code)
	if !ok {
		normalized = "invalid:" + code
	}
	var result RecoveryResult
	err := persistence.WithTenantTx(ctx, r.sessions.pool, session.TenantID, func(tx pgx.Tx) error {
		var remaining, revoked int
		var challengeRevision int
		if err := tx.QueryRow(ctx, `SELECT out_remaining, out_sessions_revoked,
			out_challenge_revision FROM identity.recovery_code_consume($1,$2,$3,$4,$5,$6)`,
			session.TenantID, session.UserID, session.SessionID, challengeID,
			DigestRecoveryCode(normalized), expectedRevision,
		).Scan(&remaining, &revoked, &challengeRevision); err != nil {
			return mapMFADBError("consume recovery code", err)
		}
		if revoked < 1 {
			return nil
		}
		_, err := audit.Append(ctx, tx, audit.Entry{
			TenantID: session.TenantID, RecordID: uuid.Must(uuid.NewV7()),
			EventType: "identity.mfa.recovery_code_used", EntityKind: "native_account",
			EntityID: session.UserID, Subject: session.Subject,
			Payload: map[string]any{
				"challengeId": challengeID.String(), "sessionsRevoked": revoked,
				"challengeRevision": challengeRevision,
			},
		})
		if err != nil {
			return err
		}
		result = RecoveryResult{Verified: true, Remaining: remaining,
			SessionsRevoked: revoked, MustReenrollMFA: true}
		return nil
	})
	if err != nil {
		return RecoveryResult{}, err
	}
	if !result.Verified {
		return result, ErrMFAVerification
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Administrator reset
// ---------------------------------------------------------------------------

// ProofForChallenge resolves the verified step-up session that a specific
// challenge minted, and requires it to belong to the current session.
func (r *MFARepository) ProofForChallenge(ctx context.Context, session ActiveSession, challengeID uuid.UUID) (StepUpProof, error) {
	var proof StepUpProof
	err := r.sessions.pool.QueryRow(ctx, `SELECT out_step_up_ref, out_operation,
		out_binding_digest, out_verified_methods, out_idle_expires_at, out_absolute_expires_at
		FROM identity.step_up_proof_for_challenge($1,$2,$3,$4)`,
		session.TenantID, session.UserID, session.SessionID, challengeID,
	).Scan(&proof.Ref, &proof.Operation, &proof.BindingDigest,
		&proof.VerifiedMethods, &proof.IdleExpiresAt, &proof.AbsoluteExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StepUpProof{}, ErrMFAChallengeInvalid
	}
	if err != nil {
		return StepUpProof{}, mapMFADBError("read step-up session", err)
	}
	challenge, err := r.Challenge(ctx, session.TenantID, session.UserID, challengeID)
	if err != nil {
		return StepUpProof{}, err
	}
	if challenge.State != "used" || challenge.SessionID != session.SessionID ||
		challenge.Operation != proof.Operation ||
		!equalBytes(challenge.BindingDigest, proof.BindingDigest) {
		return StepUpProof{}, ErrMFAChallengeInvalid
	}
	return proof, nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (r *MFARepository) AdminReset(ctx context.Context, actor ActiveSession, actorSubject string, authorize func(context.Context, pgx.Tx) error, targetUserID uuid.UUID, confirmLoginName, reason string, expectedRevision, expectedCredentialVersion int, target json.RawMessage, proof StepUpProof) (AdminResetResult, uuid.UUID, error) {
	if targetUserID == uuid.Nil || proof.Ref == uuid.Nil || proof.Operation != StepUpOperationMFAReset ||
		expectedRevision < 1 || expectedCredentialVersion < 1 ||
		strings.TrimSpace(confirmLoginName) == "" || len(reason) < 5 {
		return AdminResetResult{}, uuid.Nil, ErrMFATargetMismatch
	}
	digest := StepUpBindingDigest(SessionRef(actor.SessionID), StepUpOperationMFAReset,
		target, actor.SessionVersion, actor.CredentialVersion, actor.AuthorizationVersion)
	var result AdminResetResult
	auditID := uuid.Must(uuid.NewV7())
	err := persistence.WithTenantTx(ctx, r.sessions.pool, actor.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT identity.mfa_lock_reset_accounts($1,$2,$3)`, actor.TenantID, actor.UserID, targetUserID); err != nil {
			return err
		}
		if authorize != nil {
			// The administrator proof and the write share this transaction: the
			// session-bound grant check pins the account/session/grant rows for
			// the rest of the reset.
			if err := authorize(ctx, tx); err != nil {
				return err
			}
		}
		var rechecked bool
		if err := tx.QueryRow(ctx, `SELECT identity.step_up_recheck($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			actor.TenantID, actor.UserID, actor.SessionID, proof.Ref,
			StepUpOperationMFAReset, digest,
			actor.AuthorizationVersion, actor.CredentialVersion, actor.SessionVersion,
			int(StepUpIdleLifetime.Seconds()),
		).Scan(&rechecked); err != nil {
			return mapMFADBError("recheck mfa reset step-up", err)
		}
		if !rechecked {
			// The step-up must be bound to this actor session and to the target
			// revision; anything else is a denial, never a best effort.
			return ErrMFAChallengeInvalid
		}
		if err := tx.QueryRow(ctx, `SELECT out_credential_version, out_session_version,
			out_authorization_version, out_sessions_revoked, out_enrollments_removed,
			out_recovery_codes_removed FROM identity.mfa_admin_reset($1,$2,$3,$4,$5,$6)`,
			actor.TenantID, actor.UserID, targetUserID, confirmLoginName,
			expectedRevision, expectedCredentialVersion,
		).Scan(&result.CredentialVersion, &result.SessionVersion,
			&result.AuthorizationVersion, &result.SessionsRevoked,
			&result.EnrollmentsRemoved, &result.RecoveryCodesRemoved); err != nil {
			return mapMFADBError("reset mfa by administrator", err)
		}
		_, err := audit.Append(ctx, tx, audit.Entry{
			TenantID: actor.TenantID, RecordID: auditID,
			EventType: "identity.mfa.reset_by_administrator", EntityKind: "native_account",
			EntityID: targetUserID, Subject: actorSubject,
			Payload: map[string]any{
				"reason": reason, "sessionsRevoked": result.SessionsRevoked,
				"enrollmentsRemoved":   result.EnrollmentsRemoved,
				"recoveryCodesRemoved": result.RecoveryCodesRemoved,
				"stepUpRef":            proof.Ref.String(),
			},
		})
		return err
	})
	if err != nil {
		return AdminResetResult{}, uuid.Nil, err
	}
	return result, auditID, nil
}

func mapMFADBError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		switch pgErr.SQLState() {
		case "40001":
			return ErrSessionVersion
		case "28000":
			return ErrMFAChallengeInvalid
		case "42900":
			return budgetDenial(nil)
		case "55000":
			return ErrMFAResetCredentialUnavailable
		case "22023", "23505", "23503", "23514":
			return ErrMFAVerification
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

// DigestHex is used in evidence and tests to compare digests without exposing
// the underlying material.
func DigestHex(value []byte) string { return hex.EncodeToString(value) }

// DigestSHA256 is the object digest used in writeRecheck evidence.
func DigestSHA256(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}

// sortedMethods is a small helper used by tests and response projection.
func sortedMethods(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
