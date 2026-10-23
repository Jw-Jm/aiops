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
)

const (
	StepUpOperationMFAEnrollment = "mfa_enrollment"
	StepUpOperationMFAReset      = "mfa_reset"
	StepUpOperationRecovery      = "recovery_code_regenerate"

	StepUpIdleLifetime     = 10 * time.Minute
	StepUpAbsoluteLifetime = 15 * time.Minute
	mfaEnrollmentLifetime  = 10 * time.Minute
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
	ID                         uuid.UUID
	SessionID                  uuid.UUID
	Operation                  string
	Target                     json.RawMessage
	BindingDigest              []byte
	State                      string
	Revision                   int
	RequiredMethods            []string
	CompletedMethods           []string
	Attempts                   int
	AttemptLimit               int
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
	return challenge, nil
}

func (r *MFARepository) FailChallenge(ctx context.Context, tenantID, userID, challengeID, sessionID uuid.UUID, expectedRevision int) (StepUpChallenge, error) {
	err := r.sessions.pool.QueryRow(ctx, `SELECT out_revision, out_state, out_remaining
		FROM identity.step_up_challenge_fail($1,$2,$3,$4,$5)`,
		tenantID, userID, challengeID, sessionID, expectedRevision,
	).Scan(new(int), new(string), new(int))
	if err != nil {
		return StepUpChallenge{}, mapMFADBError("record failed step-up attempt", err)
	}
	return r.Challenge(ctx, tenantID, userID, challengeID)
}

func (r *MFARepository) addMethod(ctx context.Context, session ActiveSession, challenge StepUpChallenge, method string) (StepUpChallenge, error) {
	next, err := r.Challenge(ctx, session.TenantID, session.UserID, challenge.ID)
	if err != nil {
		return StepUpChallenge{}, err
	}
	next.IdleExpiresAt = r.now().UTC().Add(StepUpIdleLifetime)
	var revision int
	var state string
	var completed []string
	var verifiedAt *time.Time
	err = r.sessions.pool.QueryRow(ctx, `SELECT out_revision, out_state, out_completed_methods, out_verified_at
		FROM identity.step_up_challenge_add_method($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		session.TenantID, session.UserID, challenge.ID, session.SessionID,
		challenge.Revision, method, session.CredentialVersion, session.SessionVersion,
		minTime(next.IdleExpiresAt, challenge.AbsoluteExpiresAt),
	).Scan(&revision, &state, &completed, &verifiedAt)
	if err != nil {
		return StepUpChallenge{}, mapMFADBError("record verified step-up method", err)
	}
	challenge.Revision = revision
	challenge.State = state
	challenge.CompletedMethods = completed
	challenge.VerifiedAt = verifiedAt
	return challenge, nil
}

// VerifyPasswordStep verifies the account password inside one challenge. A
// wrong password only consumes an attempt: it never yields a method.
func (r *MFARepository) VerifyPasswordStep(ctx context.Context, session ActiveSession, challenge StepUpChallenge, password string) (StepUpChallenge, error) {
	account, err := r.sessions.FindLoginAccount(ctx, session.LoginName)
	if err != nil || account.TenantID != session.TenantID || account.UserID != session.UserID || account.Status != "active" {
		return StepUpChallenge{}, ErrMFAVerification
	}
	valid, verifyErr := VerifyPassword(password, account.PasswordHash)
	if verifyErr != nil || !valid {
		if _, failErr := r.FailChallenge(ctx, session.TenantID, session.UserID, challenge.ID, session.SessionID, challenge.Revision); failErr != nil {
			return StepUpChallenge{}, failErr
		}
		return StepUpChallenge{}, ErrMFAVerification
	}
	return r.addMethod(ctx, session, challenge, "password")
}

// VerifyTOTPStep verifies a TOTP code against the secret the operation targets
// (the pending enrolment for mfa_enrollment, the confirmed factor otherwise) and
// consumes the time step so it cannot be replayed.
func (r *MFARepository) VerifyTOTPStep(ctx context.Context, session ActiveSession, challenge StepUpChallenge, code string) (StepUpChallenge, error) {
	var target struct {
		Kind         string `json:"kind"`
		EnrollmentID string `json:"enrollmentId"`
	}
	_ = json.Unmarshal(challenge.Target, &target)
	var enrollmentID uuid.UUID
	state := "confirmed"
	status, statusErr := r.Status(ctx, session.TenantID, session.UserID)
	if statusErr != nil {
		return StepUpChallenge{}, statusErr
	}
	if challenge.Operation == StepUpOperationMFAEnrollment && status.State != "confirmed" {
		parsed, err := uuid.Parse(target.EnrollmentID)
		if err != nil {
			return StepUpChallenge{}, ErrMFAChallengeInvalid
		}
		enrollmentID = parsed
		state = "pending_confirmation"
	} else {
		if status.State != "confirmed" || status.EnrollmentID == nil {
			return StepUpChallenge{}, ErrMFANotEnrolled
		}
		enrollmentID = *status.EnrollmentID
	}
	var envelope SecretEnvelope
	var revision int
	var expiresAt time.Time
	if err := r.sessions.pool.QueryRow(ctx, `SELECT out_ciphertext, out_key_ref, out_key_version,
		out_revision, out_expires_at FROM identity.mfa_enrollment_secret($1,$2,$3,$4)`,
		session.TenantID, session.UserID, enrollmentID, state,
	).Scan(&envelope.Ciphertext, &envelope.KeyRef, &envelope.KeyVersion, &revision, &expiresAt); err != nil {
		return StepUpChallenge{}, ErrMFANotEnrolled
	}
	secret, err := r.sealer.Open(ctx, session.TenantID, session.UserID, enrollmentID, envelope)
	if err != nil {
		return StepUpChallenge{}, err
	}
	step, ok := VerifyTOTP(string(secret), code, r.now().UTC())
	if !ok {
		if _, failErr := r.FailChallenge(ctx, session.TenantID, session.UserID, challenge.ID, session.SessionID, challenge.Revision); failErr != nil {
			return StepUpChallenge{}, failErr
		}
		return StepUpChallenge{}, ErrMFAVerification
	}
	var updated StepUpChallenge
	err = persistence.WithTenantTx(ctx, r.sessions.pool, session.TenantID, func(tx pgx.Tx) error {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT identity.mfa_session_proof($1,$2,$3,$4,$5)`, session.TenantID, session.UserID, session.SessionID, session.CredentialVersion, session.SessionVersion).Scan(&active); err != nil {
			return err
		}
		if !active {
			return ErrMFAChallengeInvalid
		}
		if state == "confirmed" {
			var accepted bool
			if err := tx.QueryRow(ctx, `SELECT identity.mfa_record_totp_step($1,$2,$3,$4)`, session.TenantID, session.UserID, enrollmentID, step).Scan(&accepted); err != nil {
				return err
			}
			if !accepted {
				return ErrMFAVerification
			}
		}
		updated = challenge
		return tx.QueryRow(ctx, `SELECT out_revision, out_state, out_completed_methods, out_verified_at FROM identity.step_up_challenge_add_method($1,$2,$3,$4,$5,$6,$7,$8,$9)`, session.TenantID, session.UserID, challenge.ID, session.SessionID, challenge.Revision, "totp", session.CredentialVersion, session.SessionVersion, minTime(r.now().UTC().Add(StepUpIdleLifetime), challenge.AbsoluteExpiresAt)).Scan(&updated.Revision, &updated.State, &updated.CompletedMethods, &updated.VerifiedAt)
	})
	if err != nil {
		return StepUpChallenge{}, mapMFADBError("verify totp challenge", err)
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
