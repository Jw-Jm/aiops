package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/authz"
	"ops-platform/internal/identity"
	"ops-platform/internal/persistence"
)

// Native MFA, step-up and recovery routes. Every handler derives identity from
// the verified native session; no request field can name a tenant, a role, an
// assurance level or a second factor policy.
type NativeMFAHandlers struct {
	Pool     persistence.TxBeginner
	MFA      *identity.MFARepository
	Sessions *identity.SessionRepository
	TenantID uuid.UUID
	Now      func() time.Time
}

func NewNativeMFAHandlers(mfa *identity.MFARepository, sessions *identity.SessionRepository, tenantID uuid.UUID, pool persistence.TxBeginner) (*NativeMFAHandlers, error) {
	if mfa == nil || sessions == nil || tenantID == uuid.Nil || pool == nil {
		return nil, errors.New("native MFA handlers require a repository and the fixed tenant")
	}
	return &NativeMFAHandlers{Pool: pool, MFA: mfa, Sessions: sessions, TenantID: tenantID, Now: time.Now}, nil
}

// ServeHTTP routes the MFA/step-up surface. Path parameters are read from the
// chi route context, so the OpenAPI operation is the single source of truth for
// which methods and paths exist.
func (h *NativeMFAHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/v1/auth/mfa/status" && r.Method == http.MethodGet:
		h.Status(w, r)
	case path == "/api/v1/auth/mfa/enrollments" && r.Method == http.MethodPost:
		h.CreateEnrollment(w, r)
	case strings.HasPrefix(path, "/api/v1/auth/mfa/enrollments/"):
		identifier := strings.TrimPrefix(path, "/api/v1/auth/mfa/enrollments/")
		verify := strings.HasSuffix(identifier, ":verify")
		identifier = strings.TrimSuffix(identifier, ":verify")
		enrollmentID, err := uuid.Parse(identifier)
		if err != nil {
			writeTenantError(w, http.StatusNotFound, "NOT_FOUND", "endpoint is unavailable", false, nativeRequestID(r))
			return
		}
		if verify && r.Method == http.MethodPost {
			h.VerifyEnrollment(w, r, enrollmentID)
			return
		}
		if !verify && r.Method == http.MethodGet {
			h.Enrollment(w, r, enrollmentID)
			return
		}
		writeTenantError(w, http.StatusMethodNotAllowed, "INVALID_ARGUMENT", "method is unavailable", false, nativeRequestID(r))
	case path == "/api/v1/auth/step-up-challenges" && r.Method == http.MethodPost:
		h.CreateChallenge(w, r)
	case strings.HasPrefix(path, "/api/v1/auth/step-up-challenges/"):
		identifier := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/auth/step-up-challenges/"), ":verify")
		challengeID, err := uuid.Parse(identifier)
		if err != nil || r.Method != http.MethodPost {
			writeTenantError(w, http.StatusNotFound, "NOT_FOUND", "endpoint is unavailable", false, nativeRequestID(r))
			return
		}
		h.VerifyChallenge(w, r, challengeID)
	case path == "/api/v1/auth/mfa/recovery-codes:verify" && r.Method == http.MethodPost:
		h.VerifyRecoveryCode(w, r)
	case strings.HasPrefix(path, "/api/v1/admin/users/") && strings.HasSuffix(path, "/mfa:reset") && r.Method == http.MethodPost:
		identifier := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/admin/users/"), "/mfa:reset")
		targetUserID, err := uuid.Parse(identifier)
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "userId is invalid", false, nativeRequestID(r))
			return
		}
		h.AdminResetMFA(w, r, targetUserID)
	default:
		writeTenantError(w, http.StatusNotFound, "NOT_FOUND", "endpoint is unavailable", false, nativeRequestID(r))
	}
}

func nativeMFASession(w http.ResponseWriter, r *http.Request) (auth.NativeSessionContext, bool) {
	requestID := nativeRequestID(r)
	native, ok := auth.NativeSessionFromContext(r.Context())
	if !ok || native.Session.Mode != "full" {
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a native session is required", false, requestID)
		return auth.NativeSessionContext{}, false
	}
	return native, true
}

func writeMFAError(w http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, identity.ErrMFAVerification):
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "the verification code was not accepted", false, requestID)
	case errors.Is(err, identity.ErrMFATargetMismatch):
		writeTenantError(w, http.StatusConflict, "CONFLICT", "the target confirmation does not match", false, requestID)
	case errors.Is(err, identity.ErrMFANotEnrolled):
		writeTenantError(w, http.StatusForbidden, "STEP_UP_REQUIRED", "no confirmed second factor is available", false, requestID)
	case errors.Is(err, identity.ErrMFAChallengeInvalid):
		writeTenantError(w, http.StatusConflict, "CONFLICT", "the step-up challenge is not usable", false, requestID)
	case errors.Is(err, identity.ErrSecretBackendUnavailable):
		writeTenantError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "the second-factor secret backend is unavailable", true, requestID)
	case errors.Is(err, identity.ErrSessionVersion):
		writeTenantError(w, http.StatusConflict, "CONFLICT", "session, credential or challenge version is stale", false, requestID)
	case errors.Is(err, identity.ErrSessionNotFound):
		writeTenantError(w, http.StatusNotFound, "NOT_FOUND", "the requested object is unavailable", false, requestID)
	default:
		writeNativeSessionError(w, err, requestID)
	}
}

func mfaEnrollmentView(enrollment identity.MFAEnrollment) map[string]any {
	return map[string]any{
		"enrollmentId": enrollment.ID.String(),
		"method":       "totp",
		"state":        enrollment.State,
		"createdAt":    enrollment.CreatedAt,
		"expiresAt":    enrollment.ExpiresAt,
		"confirmedAt":  enrollment.ConfirmedAt,
	}
}

// CreateEnrollment returns the provisioning URI exactly once.
func (h *NativeMFAHandlers) CreateEnrollment(w http.ResponseWriter, r *http.Request) {
	requestID := nativeRequestID(r)
	native, ok := nativeMFASession(w, r)
	if !ok {
		return
	}
	var body struct {
		Method                    string `json:"method"`
		ExpectedCredentialVersion int    `json:"expectedCredentialVersion"`
	}
	if err := decodeStrictJSON(w, r, &body, requestID); err != nil {
		return
	}
	if body.Method != "totp" || body.ExpectedCredentialVersion < 1 {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "only totp enrolment is available", false, requestID)
		return
	}
	if body.ExpectedCredentialVersion != native.Session.CredentialVersion {
		writeTenantError(w, http.StatusConflict, "CONFLICT", "credential version is stale", false, requestID)
		return
	}
	enrollment, uri, err := h.MFA.StartEnrollment(r.Context(), native.Session)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, apiSuccess(requestID, map[string]any{
		"enrollmentId": enrollment.ID.String(),
		"method":       "totp",
		"state":        "pending_confirmation",
		"otpauthUri":   uri,
		"createdAt":    enrollment.CreatedAt,
		"expiresAt":    enrollment.ExpiresAt,
	}))
}

// Enrollment reports the status view. It never returns the provisioning secret.
func (h *NativeMFAHandlers) Enrollment(w http.ResponseWriter, r *http.Request, enrollmentID uuid.UUID) {
	requestID := nativeRequestID(r)
	native, ok := nativeMFASession(w, r)
	if !ok {
		return
	}
	enrollment, err := h.MFA.EnrollmentView(r.Context(), native.Session.TenantID, native.Session.UserID, enrollmentID)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiSuccess(requestID, mfaEnrollmentView(enrollment)))
}

// VerifyEnrollment confirms a pending enrolment after the password+TOTP
// challenge bound to this enrolment, and returns the recovery codes once.
func (h *NativeMFAHandlers) VerifyEnrollment(w http.ResponseWriter, r *http.Request, enrollmentID uuid.UUID) {
	requestID := nativeRequestID(r)
	native, ok := nativeMFASession(w, r)
	if !ok {
		return
	}
	var body struct {
		TOTPCode                   string `json:"totpCode"`
		ExpectedEnrollmentRevision int    `json:"expectedEnrollmentRevision"`
		StepUp                     struct {
			ChallengeID string `json:"challengeId"`
			SessionRef  string `json:"sessionRef"`
		} `json:"stepUp"`
	}
	if err := decodeStrictJSON(w, r, &body, requestID); err != nil {
		return
	}
	if len(body.TOTPCode) != 6 || body.ExpectedEnrollmentRevision < 1 || body.StepUp.SessionRef == "" {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a totp code and a bound step-up are required", false, requestID)
		return
	}
	if body.StepUp.SessionRef != identity.SessionRef(native.Session.SessionID) {
		writeTenantError(w, http.StatusConflict, "CONFLICT", "the step-up belongs to another session", false, requestID)
		return
	}
	challengeID, err := uuid.Parse(body.StepUp.ChallengeID)
	if err != nil {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "challengeId is invalid", false, requestID)
		return
	}
	proof, err := h.MFA.ProofForChallenge(r.Context(), native.Session, challengeID)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	result, codes, err := h.MFA.ConfirmEnrollment(r.Context(), native.Session, enrollmentID,
		body.ExpectedEnrollmentRevision, body.TOTPCode, proof)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiSuccess(requestID, map[string]any{
		"enrollmentId":      result.EnrollmentID.String(),
		"state":             "confirmed",
		"confirmedAt":       result.ConfirmedAt,
		"credentialVersion": result.CredentialVersion,
		"recoveryCodes":     map[string]any{"codes": codes},
	}))
}

// CreateChallenge persists a challenge bound to the current session, operation,
// target and versions. The client cannot choose the required methods.
func (h *NativeMFAHandlers) CreateChallenge(w http.ResponseWriter, r *http.Request) {
	requestID := nativeRequestID(r)
	native, ok := nativeMFASession(w, r)
	if !ok {
		return
	}
	var body struct {
		Operation              string          `json:"operation"`
		Target                 json.RawMessage `json:"target"`
		Command                json.RawMessage `json:"command"`
		ExpectedSessionVersion int             `json:"expectedSessionVersion"`
	}
	if err := decodeStrictJSON(w, r, &body, requestID); err != nil {
		return
	}
	if body.ExpectedSessionVersion != native.Session.SessionVersion {
		writeTenantError(w, http.StatusConflict, "CONFLICT", "session version is stale", false, requestID)
		return
	}
	var target map[string]any
	if err := json.Unmarshal(body.Target, &target); err != nil || target == nil {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "target is invalid", false, requestID)
		return
	}
	if body.Operation == "command_execution" {
		actor, ok := auth.RequestContextFromContext(r.Context())
		if !ok {
			writeMFAError(w, auth.ErrUnauthenticated, requestID)
			return
		}
		err := persistence.WithTenantTx(r.Context(), h.Pool, native.Session.TenantID, func(tx pgx.Tx) error {
			var err error
			target, err = action.ResolveNativeCommandStepUp(r.Context(), tx, actor, body.Target, body.Command)
			return err
		})
		if err != nil {
			actionError(w, err, requestID)
			return
		}
	} else if len(body.Command) > 0 {
		writeTenantError(w, 400, "INVALID_ARGUMENT", "only command execution accepts a command binding", false, requestID)
		return
	}
	challenge, err := h.MFA.BeginChallenge(r.Context(), native.Session, body.Operation, target)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, apiSuccess(requestID, challengeView(challenge)))
}

func challengeView(challenge identity.StepUpChallenge) map[string]any {
	completed := challenge.CompletedMethods
	if completed == nil {
		completed = []string{}
	}
	remaining := challenge.AttemptLimit - challenge.Attempts
	if remaining < 0 {
		remaining = 0
	}
	if challenge.State != "pending" {
		remaining = 0
	}
	var target map[string]any
	_ = json.Unmarshal(challenge.Target, &target)
	command := target["command"]
	delete(target, "command")
	view := map[string]any{
		"challengeId":       challenge.ID.String(),
		"operation":         challenge.Operation,
		"target":            target,
		"bindingDigest":     "sha256:" + identity.DigestHex(challenge.BindingDigest),
		"requiredMethods":   challenge.RequiredMethods,
		"completedMethods":  completed,
		"state":             challenge.State,
		"revision":          challenge.Revision,
		"createdAt":         challenge.CreatedAt,
		"expiresAt":         challenge.AbsoluteExpiresAt,
		"verifiedAt":        challenge.VerifiedAt,
		"attemptLimit":      challenge.AttemptLimit,
		"remainingAttempts": remaining,
	}
	if command != nil {
		view["command"] = command
	}
	return view
}

// VerifyChallenge accepts exactly one credential per call and reports the
// server-accumulated methods. Only password+TOTP mints a step-up session.
func (h *NativeMFAHandlers) VerifyChallenge(w http.ResponseWriter, r *http.Request, challengeID uuid.UUID) {
	requestID := nativeRequestID(r)
	native, ok := nativeMFASession(w, r)
	if !ok {
		return
	}
	var body struct {
		Method                    string `json:"method"`
		Credential                string `json:"credential"`
		ExpectedChallengeRevision int    `json:"expectedChallengeRevision"`
	}
	if err := decodeStrictJSON(w, r, &body, requestID); err != nil {
		return
	}
	if (body.Method != "password" && body.Method != "totp") || body.Credential == "" ||
		body.ExpectedChallengeRevision < 1 {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a single credential and the challenge revision are required", false, requestID)
		return
	}
	challenge, err := h.MFA.Challenge(r.Context(), native.Session.TenantID, native.Session.UserID, challengeID)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	if challenge.SessionID != native.Session.SessionID {
		writeTenantError(w, http.StatusForbidden, "FORBIDDEN", "the challenge belongs to another session", false, requestID)
		return
	}
	if challenge.State != "pending" || challenge.Revision != body.ExpectedChallengeRevision {
		writeTenantError(w, http.StatusConflict, "CONFLICT", "the challenge is not pending at this revision", false, requestID)
		return
	}
	if body.Method == "password" {
		challenge, err = h.MFA.VerifyPasswordStep(r.Context(), native.Session, challenge, body.Credential)
	} else {
		challenge, err = h.MFA.VerifyTOTPStep(r.Context(), native.Session, challenge, body.Credential)
	}
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		if errors.Is(err, identity.ErrMFAVerification) {
			// A rejected factor is a truthful result, not a server error: the
			// challenge stays pending, LoA1, and no step-up session exists.
			current, readErr := h.MFA.Challenge(r.Context(), native.Session.TenantID, native.Session.UserID, challengeID)
			if readErr != nil {
				writeMFAError(w, readErr, requestID)
				return
			}
			writeJSON(w, http.StatusOK, apiSuccess(requestID, challengeResult(current, body.Method, nil, "invalid_credential")))
			return
		}
		writeMFAError(w, err, requestID)
		return
	}
	if challenge.State != "verified" {
		writeJSON(w, http.StatusOK, apiSuccess(requestID, challengeResult(challenge, body.Method, nil, "")))
		return
	}
	proof, err := h.MFA.MintStepUp(r.Context(), native.Session, challenge)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, apiSuccess(requestID, challengeResult(challenge, body.Method, &proof, "")))
}

func challengeResult(challenge identity.StepUpChallenge, method string, proof *identity.StepUpProof, failureCode string) map[string]any {
	completed := challenge.CompletedMethods
	if completed == nil {
		completed = []string{}
	}
	verified := challenge.State == "verified" && proof != nil
	acr := auth.NativeACRLevel1
	var stepUp any
	if verified {
		acr = auth.StepUpACRLevel2
		stepUp = map[string]any{
			"sessionRef":        "stepup:" + proof.Ref.String(),
			"operation":         proof.Operation,
			"bindingDigest":     "sha256:" + identity.DigestHex(proof.BindingDigest),
			"verifiedMethods":   proof.VerifiedMethods,
			"absoluteExpiresAt": proof.AbsoluteExpiresAt,
			"idleExpiresAt":     proof.IdleExpiresAt,
		}
	}
	return map[string]any{
		"verified":          verified,
		"method":            method,
		"challengeState":    challenge.State,
		"revision":          challenge.Revision,
		"completedMethods":  completed,
		"acr":               acr,
		"stepUpSession":     stepUp,
		"verifiedAt":        challenge.VerifiedAt,
		"failureCode":       nullableString(failureCode),
		"remainingAttempts": max(challenge.AttemptLimit-challenge.Attempts, 0),
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nativeActor projects the verified native session into the shared request
// context. The roles come from the server-side grant rows of this session, not
// from a request field.
func nativeActor(native auth.NativeSessionContext) auth.RequestContext {
	roles := make([]auth.Role, 0, len(native.Session.Grants))
	for _, grant := range native.Session.Grants {
		roles = append(roles, auth.Role(grant.Role))
	}
	return auth.RequestContext{
		TenantID: native.Session.TenantID,
		Subject:  native.Session.Subject,
		Roles:    roles,
	}
}

// VerifyRecoveryCode consumes one recovery code inside the bound challenge. It
// never yields LoA2: the result is LoA1, every session is revoked and MFA must be
// re-enrolled.
func (h *NativeMFAHandlers) VerifyRecoveryCode(w http.ResponseWriter, r *http.Request) {
	requestID := nativeRequestID(r)
	native, ok := nativeMFASession(w, r)
	if !ok {
		return
	}
	var body struct {
		ChallengeID               string `json:"challengeId"`
		RecoveryCode              string `json:"recoveryCode"`
		ExpectedChallengeRevision int    `json:"expectedChallengeRevision"`
	}
	if err := decodeStrictJSON(w, r, &body, requestID); err != nil {
		return
	}
	challengeID, err := uuid.Parse(body.ChallengeID)
	if err != nil || body.ExpectedChallengeRevision < 1 {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "challengeId and challenge revision are required", false, requestID)
		return
	}
	challenge, err := h.MFA.Challenge(r.Context(), native.Session.TenantID, native.Session.UserID, challengeID)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	if challenge.SessionID != native.Session.SessionID || challenge.Operation != identity.StepUpOperationRecovery {
		writeTenantError(w, http.StatusForbidden, "FORBIDDEN", "the recovery challenge does not belong to this session", false, requestID)
		return
	}
	result, err := h.MFA.ConsumeRecoveryCode(r.Context(), native.Session, challengeID,
		body.ExpectedChallengeRevision, body.RecoveryCode)
	if err != nil {
		if errors.Is(err, identity.ErrMFAVerification) {
			current, readErr := h.MFA.Challenge(r.Context(), native.Session.TenantID, native.Session.UserID, challengeID)
			if readErr != nil {
				writeMFAError(w, readErr, requestID)
				return
			}
			writeJSON(w, http.StatusOK, apiSuccess(requestID, map[string]any{
				"challengeRevision": current.Revision, "challengeState": current.State,
				"verified": false, "method": "recovery_code", "acr": auth.NativeACRLevel1,
				"sessionsRevoked": 0, "recoveryCodesRemaining": 0,
				"mustReenrollMfa": false, "stepUpSession": nil,
			}))
			return
		}
		writeMFAError(w, err, requestID)
		return
	}
	writeJSON(w, http.StatusOK, apiSuccess(requestID, map[string]any{
		"verified":               result.Verified,
		"method":                 "recovery_code",
		"acr":                    auth.NativeACRLevel1,
		"sessionsRevoked":        result.SessionsRevoked,
		"recoveryCodesRemaining": result.Remaining,
		"mustReenrollMfa":        result.MustReenrollMFA,
		"stepUpSession":          nil,
	}))
}

// AdminResetMFA is a high-risk administrator write. The grant proof, the
// step-up recheck, the version bumps, the revocations and the audit record all
// commit in one transaction.
func (h *NativeMFAHandlers) AdminResetMFA(w http.ResponseWriter, r *http.Request, targetUserID uuid.UUID) {
	requestID := nativeRequestID(r)
	native, ok := nativeMFASession(w, r)
	if !ok {
		return
	}
	var body struct {
		UserID                    string `json:"userId"`
		ConfirmLoginName          string `json:"confirmLoginName"`
		Reason                    string `json:"reason"`
		ExpectedUserRevision      int    `json:"expectedUserRevision"`
		ExpectedCredentialVersion int    `json:"expectedCredentialVersion"`
		StepUp                    struct {
			ChallengeID string `json:"challengeId"`
			SessionRef  string `json:"sessionRef"`
		} `json:"stepUp"`
	}
	if err := decodeStrictJSON(w, r, &body, requestID); err != nil {
		return
	}
	if body.UserID != targetUserID.String() || body.StepUp.SessionRef != identity.SessionRef(native.Session.SessionID) {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "the target or session reference does not match the request path", false, requestID)
		return
	}
	if body.ExpectedUserRevision < 1 || body.ExpectedCredentialVersion < 1 || len(strings.TrimSpace(body.Reason)) < 5 {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "revision, credential version and reason are required", false, requestID)
		return
	}
	challengeID, err := uuid.Parse(body.StepUp.ChallengeID)
	if err != nil {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "challengeId is invalid", false, requestID)
		return
	}
	proof, err := h.MFA.ProofForChallenge(r.Context(), native.Session, challengeID)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	if proof.Operation != identity.StepUpOperationMFAReset {
		writeTenantError(w, http.StatusForbidden, "STEP_UP_REQUIRED", "the step-up is not bound to an MFA reset", false, requestID)
		return
	}
	target, _ := json.Marshal(map[string]any{
		"kind": "user", "userId": targetUserID.String(), "revision": body.ExpectedUserRevision,
	})
	started := h.Now().UTC()
	authorize := func(ctx context.Context, tx pgx.Tx) error {
		return authz.RequireAdmin(ctx, tx, nativeActor(native))
	}
	result, auditID, err := h.MFA.AdminReset(r.Context(), native.Session, native.Session.Subject,
		authorize, targetUserID, body.ConfirmLoginName, body.Reason,
		body.ExpectedUserRevision, body.ExpectedCredentialVersion, target, proof)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	attemptID := uuid.Must(uuid.NewV7())
	sessionDigest := identity.StepUpBindingDigest(identity.SessionRef(native.Session.SessionID),
		"mfa_reset", target, native.Session.SessionVersion, native.Session.CredentialVersion,
		native.Session.AuthorizationVersion)
	writeJSON(w, http.StatusOK, apiSuccess(requestID, map[string]any{
		"userId":            targetUserID.String(),
		"mfaState":          "reset_required",
		"credentialVersion": result.CredentialVersion,
		"sessionsRevoked":   result.SessionsRevoked,
		"revocation": map[string]any{
			"credentialVersion":    result.CredentialVersion,
			"sessionVersion":       result.SessionVersion,
			"authorizationVersion": result.AuthorizationVersion,
		},
		"writeRecheck": map[string]any{
			"writeAttemptId":       attemptID.String(),
			"writeOperation":       "mfa_reset",
			"checkedAt":            started,
			"result":               "passed",
			"sessionDigest":        "sha256:" + identity.DigestHex(sessionDigest),
			"authorizationVersion": native.Session.AuthorizationVersion,
			"credentialVersion":    native.Session.CredentialVersion,
			"objectDigest":         "sha256:" + identity.DigestHex(identity.DigestSHA256(target)),
			"actualCommandDigest":  nil,
		},
		"auditRecordId": auditID.String(),
	}))
}
