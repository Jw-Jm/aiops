package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/identity"
)

const (
	defaultNativeIdleTTL     = 30 * time.Minute
	defaultNativeAbsoluteTTL = 12 * time.Hour
	defaultLoginFailureLimit = 5
	defaultLoginFailureLock  = 15 * time.Minute
	defaultLoginEntryLimit   = 60
	defaultLoginEntryWindow  = time.Minute
	defaultLoginEntryLock    = time.Minute
	maxNativeJSONBodyBytes   = 8 << 10
	dummyPasswordHash        = "argon2id$v=19$m=65536,t=2,p=1$kyNne0mZIGcBH+IKoKSQbA$u0Vip2Md38oVK/yn81yTs2xd8EHUxXoUlZQXOhH2FC0"
)

type NativeAuthHandlers struct {
	Sessions *identity.SessionRepository
	// MFA is optional: when it is wired, a verified password+TOTP step-up raises
	// the reported session assurance to LoA2. Without it the session stays LoA1.
	MFA          *identity.MFARepository
	TenantID     uuid.UUID
	IdleTTL      time.Duration
	AbsoluteTTL  time.Duration
	FailureLimit int
	FailureLock  time.Duration
	EntryLimit   int
	EntryWindow  time.Duration
	EntryLock    time.Duration
	PublicOrigin string
	Now          func() time.Time
}

func NewNativeAuthHandlers(sessions *identity.SessionRepository, tenantID uuid.UUID) (*NativeAuthHandlers, error) {
	if sessions == nil {
		return nil, errors.New("native session repository is required")
	}
	if tenantID == uuid.Nil {
		return nil, errors.New("fixed native deployment tenant is required")
	}
	return &NativeAuthHandlers{
		Sessions: sessions, TenantID: tenantID,
		IdleTTL: defaultNativeIdleTTL, AbsoluteTTL: defaultNativeAbsoluteTTL,
		FailureLimit: defaultLoginFailureLimit, FailureLock: defaultLoginFailureLock,
		EntryLimit: defaultLoginEntryLimit, EntryWindow: defaultLoginEntryWindow,
		EntryLock: defaultLoginEntryLock, Now: time.Now,
	}, nil
}

func (h *NativeAuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	requestID := nativeRequestID(r)
	if err := auth.VerifyNativeOrigin(r, h.PublicOrigin); err != nil {
		writeTenantError(w, http.StatusForbidden, "FORBIDDEN", "login requires a same-origin browser request", false, requestID)
		return
	}
	var body struct {
		LoginName   string `json:"loginName"`
		Password    string `json:"password"`
		DeviceLabel string `json:"deviceLabel"`
	}
	if err := decodeStrictJSON(w, r, &body, requestID); err != nil {
		return
	}
	loginName := identity.NormalizeLoginName(body.LoginName)
	if identity.ValidateLoginName(loginName) != nil || identity.ValidatePassword(body.Password) != nil ||
		len(body.DeviceLabel) > 128 {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "login credentials are invalid", false, requestID)
		return
	}
	if err := h.Sessions.ActiveDeploymentTenant(r.Context(), h.TenantID); err != nil {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "deployment organization is unavailable", true, requestID)
		return
	}
	now := h.Now().UTC().Truncate(time.Microsecond)
	allowed, retryAt, err := h.Sessions.AdmitLoginAttempt(
		r.Context(), now, h.EntryLimit, h.EntryWindow, h.EntryLock,
	)
	if err != nil {
		writeTenantError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "login is temporarily unavailable", true, requestID)
		return
	}
	if !allowed {
		if retryAt != nil {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(*retryAt).Seconds()))))
		}
		writeTenantError(w, http.StatusTooManyRequests, "RATE_LIMITED", "login is temporarily unavailable", false, requestID)
		return
	}
	account, err := h.Sessions.FindLoginAccount(r.Context(), loginName)
	if err != nil {
		if errors.Is(err, identity.ErrLoginNotFound) {
			// Equalize the expensive password path for unknown accounts without
			// persisting attacker-chosen usernames. Congestion is still reported
			// as capacity, never as a rejected credential.
			if writePasswordWorkUnavailable(w, verifyDummyPassword(r.Context(), body.Password), requestID) {
				return
			}
			writeConstantLoginFailure(w, requestID)
			return
		}
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "login could not be checked", true, requestID)
		return
	}
	if account.LockedUntil != nil && now.Before(*account.LockedUntil) {
		if writePasswordWorkUnavailable(w, verifyDummyPassword(r.Context(), body.Password), requestID) {
			return
		}
		writeConstantLoginFailure(w, requestID)
		return
	}
	valid, verifyErr := identity.VerifyPasswordContext(r.Context(), body.Password, account.PasswordHash)
	if writePasswordWorkUnavailable(w, verifyErr, requestID) {
		// A busy or cancelled hash never counts as a failed password attempt.
		return
	}
	if verifyErr != nil || !valid || account.Status != "active" {
		if account.Status == "active" {
			if _, _, fenceErr := h.Sessions.RecordKnownLoginFailure(
				r.Context(), account.TenantID, account.UserID, now, h.FailureLimit, h.FailureLock,
			); fenceErr != nil {
				writeTenantError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "login is temporarily unavailable", true, requestID)
				return
			}
		}
		writeConstantLoginFailure(w, requestID)
		return
	}
	mode := "full"
	if account.MustChangePassword {
		mode = "first_password_only"
	}
	issued, err := h.createSession(r.Context(), account, mode, now)
	if err != nil {
		writeNativeSessionError(w, err, requestID)
		return
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "session audit could not be created", true, requestID)
		return
	}
	material, err := h.Sessions.CreateSessionWithAudit(r.Context(), issued.input, audit.Entry{
		TenantID: account.TenantID, RecordID: auditID,
		EventType: "identity.native_login.succeeded", EntityKind: "native_session",
		EntityID: issued.SessionID,
		Payload:  map[string]any{"mode": mode, "authMethods": []string{"password"}},
	})
	if err != nil {
		writeNativeSessionError(w, err, requestID)
		return
	}
	issued = issuedFromActive(material.Session, issued.SessionToken, issued.CSRFToken)
	setSessionCookie(w, issued.SessionToken, int(h.AbsoluteTTL.Seconds()))
	writeJSON(w, http.StatusCreated, apiSuccess(requestID, nativeIssuedSessionView(
		issued, issued.CSRFToken, issued.IdleExpiresAt, material.State, material.Clusters,
	)))
}

func (h *NativeAuthHandlers) Session(w http.ResponseWriter, r *http.Request) {
	requestID := nativeRequestID(r)
	native, ok := auth.NativeSessionFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a native session is required", false, requestID)
		return
	}
	state, err := h.Sessions.AdministratorState(r.Context(), native.Session.TenantID, native.Session.UserID)
	if err != nil {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "session state could not be loaded", true, requestID)
		return
	}
	clusters, clusterErr := h.authorizedClusters(r.Context(), native.Session)
	if clusterErr != nil {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "authorized cluster scope could not be loaded", true, requestID)
		return
	}
	view := nativeActiveSessionView(native.Session, native.CSRFToken, native.IdleExpiresAt, state, clusters)
	// A session is LoA2 only while a verified password+TOTP step-up is active for
	// it. The proof is read from the server-side session, never from a client
	// field, and it expires with its own idle and absolute windows.
	if h.MFA != nil {
		if proof, proofErr := h.MFA.ActiveProof(r.Context(), native.Session); proofErr == nil && proof != nil {
			view["acr"] = auth.StepUpACRLevel2
			view["authMethods"] = []string{"password", "totp"}
			view["stepUpSession"] = map[string]any{
				"sessionRef":        "stepup:" + proof.Ref.String(),
				"operation":         proof.Operation,
				"bindingDigest":     "sha256:" + identity.DigestHex(proof.BindingDigest),
				"verifiedMethods":   proof.VerifiedMethods,
				"absoluteExpiresAt": proof.AbsoluteExpiresAt,
				"idleExpiresAt":     proof.IdleExpiresAt,
			}
		}
	}
	writeJSON(w, http.StatusOK, apiSuccess(requestID, view))
}

// MFAStatus reports the second-factor state of the signed-in account. The
// provisioning secret is never part of this projection.
func (h *NativeMFAHandlers) Status(w http.ResponseWriter, r *http.Request) {
	requestID := nativeRequestID(r)
	native, ok := nativeMFASession(w, r)
	if !ok {
		return
	}
	status, err := h.MFA.Status(r.Context(), native.Session.TenantID, native.Session.UserID)
	if err != nil {
		writeMFAError(w, err, requestID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiSuccess(requestID, map[string]any{
		"state": status.State, "enrollmentId": status.EnrollmentID,
		"confirmedAt":            status.ConfirmedAt,
		"recoveryCodesRemaining": status.RecoveryCodesRemaining,
		"method":                 "totp",
	}))
}

func (h *NativeAuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	requestID := nativeRequestID(r)
	native, ok := auth.NativeSessionFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a native session is required", false, requestID)
		return
	}
	var body struct {
		ExpectedSessionVersion int    `json:"expectedSessionVersion"`
		Reason                 string `json:"reason"`
	}
	if err := decodeStrictJSON(w, r, &body, requestID); err != nil {
		return
	}
	if body.ExpectedSessionVersion < 1 || strings.TrimSpace(body.Reason) == "" {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "session version and reason are required", false, requestID)
		return
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "logout audit could not be created", true, requestID)
		return
	}
	revoked, err := h.Sessions.RevokeSessionWithAudit(r.Context(), identity.RevokeSessionInput{
		TenantID:             native.Session.TenantID,
		UserID:               native.Session.UserID,
		SessionID:            native.Session.SessionID,
		RevokedAt:            h.Now().UTC().Truncate(time.Microsecond),
		Reason:               body.Reason,
		CredentialVersion:    native.Session.CredentialVersion,
		AuthorizationVersion: native.Session.AuthorizationVersion,
		SessionVersion:       body.ExpectedSessionVersion,
	}, audit.Entry{
		TenantID: native.Session.TenantID, RecordID: auditID,
		EventType: "identity.native_session.revoked", EntityKind: "native_session",
		EntityID: native.Session.SessionID, Subject: native.Session.Subject,
		Payload: map[string]any{"reason": "logout"},
	})
	if errors.Is(err, identity.ErrSessionVersion) {
		writeTenantError(w, http.StatusConflict, "CONFLICT", "session version is stale", false, requestID)
		return
	}
	if err != nil {
		writeNativeSessionError(w, err, requestID)
		return
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, apiSuccess(requestID, map[string]any{
		"credentialVersion":    revoked.CredentialVersion,
		"sessionVersion":       revoked.SessionVersion,
		"authorizationVersion": revoked.AuthorizationVersion,
	}))
}

func (h *NativeAuthHandlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	requestID := nativeRequestID(r)
	native, ok := auth.NativeSessionFromContext(r.Context())
	if !ok {
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a native session is required", false, requestID)
		return
	}
	if err := auth.VerifyNativeOrigin(r, h.PublicOrigin); err != nil {
		writeTenantError(w, http.StatusForbidden, "FORBIDDEN", "password change requires a same-origin browser request", false, requestID)
		return
	}
	var body struct {
		CurrentPassword           string `json:"currentPassword"`
		NewPassword               string `json:"newPassword"`
		ExpectedCredentialVersion int    `json:"expectedCredentialVersion"`
	}
	if err := decodeStrictJSON(w, r, &body, requestID); err != nil {
		return
	}
	if identity.ValidatePassword(body.CurrentPassword) != nil ||
		identity.ValidatePassword(body.NewPassword) != nil ||
		body.ExpectedCredentialVersion < 1 {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "current and new credentials are required", false, requestID)
		return
	}
	if body.ExpectedCredentialVersion != native.Session.CredentialVersion {
		writeTenantError(w, http.StatusConflict, "CONFLICT", "credential version is stale", false, requestID)
		return
	}
	now := h.Now().UTC().Truncate(time.Microsecond)
	allowed, retryAt, err := h.Sessions.AdmitLoginAttempt(
		r.Context(), now, h.EntryLimit, h.EntryWindow, h.EntryLock,
	)
	if err != nil {
		writeTenantError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "password change is temporarily unavailable", true, requestID)
		return
	}
	if !allowed {
		if retryAt != nil {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAt.Sub(now).Seconds()))))
		}
		writeTenantError(w, http.StatusTooManyRequests, "RATE_LIMITED", "password change is temporarily unavailable", false, requestID)
		return
	}
	account, err := h.Sessions.FindLoginAccount(r.Context(), native.Session.LoginName)
	if err != nil {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "account state could not be checked", true, requestID)
		return
	}
	if account.LockedUntil != nil && now.Before(*account.LockedUntil) {
		writeTenantError(w, http.StatusTooManyRequests, "RATE_LIMITED", "password change is temporarily unavailable", false, requestID)
		return
	}
	valid, err := identity.VerifyPasswordContext(r.Context(), body.CurrentPassword, account.PasswordHash)
	if writePasswordWorkUnavailable(w, err, requestID) {
		// Capacity and cancellation are not evidence about the current password.
		return
	}
	if err != nil || !valid || account.Status != "active" {
		if account.Status == "active" {
			if _, _, fenceErr := h.Sessions.RecordKnownLoginFailure(
				r.Context(), account.TenantID, account.UserID, now, h.FailureLimit, h.FailureLock,
			); fenceErr != nil {
				writeTenantError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "password change is temporarily unavailable", true, requestID)
				return
			}
		}
		if account.Status != "active" {
			writeConstantLoginFailure(w, requestID)
		} else {
			writeTenantError(w, http.StatusUnauthorized, "CURRENT_PASSWORD_INVALID", "current password was not accepted", false, requestID)
		}
		return
	}
	newHash, err := identity.HashPasswordContext(r.Context(), body.NewPassword)
	if writePasswordWorkUnavailable(w, err, requestID) {
		return
	}
	if err != nil {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "new credentials are invalid", false, requestID)
		return
	}
	issued, err := h.changeOwnPassword(r.Context(), native.Session, newHash, now)
	if err != nil {
		writeNativeSessionError(w, err, requestID)
		return
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "password audit could not be created", true, requestID)
		return
	}
	material, err := h.Sessions.ChangeOwnPasswordWithAudit(r.Context(), issued.passwordInput, audit.Entry{
		TenantID: account.TenantID, RecordID: auditID,
		EventType: "identity.native_password.changed", EntityKind: "native_account",
		EntityID: account.UserID,
		Payload:  map[string]any{"authMethods": []string{"password"}},
	})
	if err != nil {
		writeNativeSessionError(w, err, requestID)
		return
	}
	issued = issuedFromActive(material.Session, issued.SessionToken, issued.CSRFToken)
	setSessionCookie(w, issued.SessionToken, int(h.AbsoluteTTL.Seconds()))
	writeJSON(w, http.StatusCreated, apiSuccess(requestID, nativeIssuedSessionView(
		issued, issued.CSRFToken, issued.IdleExpiresAt, material.State, material.Clusters,
	)))
}

type issuedNativeSession struct {
	SessionID            uuid.UUID
	SessionToken         string
	CSRFToken            string
	TenantID             uuid.UUID
	UserID               uuid.UUID
	Mode                 string
	Subject              string
	LoginName            string
	DisplayName          string
	Status               string
	PasswordState        string
	MustReenrollMFA      bool
	MustChangePassword   bool
	AccountRevision      int
	CredentialVersion    int
	AuthorizationVersion int
	SessionVersion       int
	Grants               []identity.SessionGrant
	IssuedAt             time.Time
	IdleExpiresAt        time.Time
	AbsoluteExpiresAt    time.Time
	input                identity.CreateSessionInput
	passwordInput        identity.ChangeOwnPasswordInput
}

func issuedFromActive(session identity.ActiveSession, token, csrf string) issuedNativeSession {
	return issuedNativeSession{
		SessionID: session.SessionID, SessionToken: token, CSRFToken: csrf,
		TenantID: session.TenantID, UserID: session.UserID, Mode: session.Mode,
		Subject: session.Subject, LoginName: session.LoginName,
		DisplayName: session.DisplayName, Status: session.Status,
		PasswordState: session.PasswordState, MustChangePassword: session.MustChangePassword, MustReenrollMFA: session.MustReenrollMFA,
		AccountRevision: session.AccountRevision, CredentialVersion: session.CredentialVersion,
		AuthorizationVersion: session.AuthorizationVersion, SessionVersion: session.SessionVersion,
		Grants: session.Grants, IssuedAt: session.IssuedAt,
		IdleExpiresAt: session.IdleExpiresAt, AbsoluteExpiresAt: session.AbsoluteExpiresAt,
	}
}

func (issued issuedNativeSession) issuedSession() identity.ActiveSession {
	return identity.ActiveSession{
		SessionID: issued.SessionID, Mode: issued.Mode, TenantID: issued.TenantID,
		UserID: issued.UserID, Subject: issued.Subject, LoginName: issued.LoginName,
		DisplayName: issued.DisplayName, Status: issued.Status,
		PasswordState: issued.PasswordState, MustChangePassword: issued.MustChangePassword, MustReenrollMFA: issued.MustReenrollMFA,
		AccountRevision: issued.AccountRevision, CredentialVersion: issued.CredentialVersion,
		AuthorizationVersion: issued.AuthorizationVersion, SessionVersion: issued.SessionVersion,
		Grants: issued.Grants, IssuedAt: issued.IssuedAt,
		IdleExpiresAt: issued.IdleExpiresAt, AbsoluteExpiresAt: issued.AbsoluteExpiresAt,
	}
}

func (h *NativeAuthHandlers) createSession(ctx context.Context, account identity.LoginAccount, mode string, now time.Time) (issuedNativeSession, error) {
	token, err := newOpaqueToken()
	if err != nil {
		return issuedNativeSession{}, err
	}
	csrf := identity.DeriveCSRFToken(token)
	sessionID, err := uuid.NewV7()
	if err != nil {
		return issuedNativeSession{}, fmt.Errorf("generate native session id: %w", err)
	}
	input := identity.CreateSessionInput{
		TenantID: account.TenantID, UserID: account.UserID, SessionID: sessionID,
		Mode: mode, TokenDigest: identity.DigestToken(token), CSRFDigest: identity.DigestToken(csrf),
		CredentialVersion: account.CredentialVersion, AuthorizationVersion: account.AuthorizationVersion,
		SessionVersion: account.SessionVersion,
		IdleExpiresAt:  now.Add(h.IdleTTL), AbsoluteExpiresAt: now.Add(h.AbsoluteTTL),
	}
	return issuedNativeSession{SessionID: sessionID, SessionToken: token, CSRFToken: csrf, input: input}, nil
}

func (h *NativeAuthHandlers) changeOwnPassword(ctx context.Context, session identity.ActiveSession, newHash string, now time.Time) (issuedNativeSession, error) {
	newToken, err := newOpaqueToken()
	if err != nil {
		return issuedNativeSession{}, err
	}
	newCSRF := identity.DeriveCSRFToken(newToken)
	newSessionID, err := uuid.NewV7()
	if err != nil {
		return issuedNativeSession{}, fmt.Errorf("generate rotated session id: %w", err)
	}
	input := identity.ChangeOwnPasswordInput{
		TenantID: session.TenantID, UserID: session.UserID,
		OldSessionID: session.SessionID, NewSessionID: newSessionID,
		NewTokenDigest: identity.DigestToken(newToken), NewCSRFDigest: identity.DigestToken(newCSRF),
		ExpectedCredentialVersion:    session.CredentialVersion,
		ExpectedAuthorizationVersion: session.AuthorizationVersion,
		ExpectedSessionVersion:       session.SessionVersion,
		NewPasswordHash:              newHash,
		IdleExpiresAt:                now.Add(h.IdleTTL), AbsoluteExpiresAt: now.Add(h.AbsoluteTTL),
	}
	return issuedNativeSession{
		SessionID: newSessionID, SessionToken: newToken, CSRFToken: newCSRF,
		passwordInput: input,
	}, nil
}

func (h *NativeAuthHandlers) authorizedClusters(ctx context.Context, session identity.ActiveSession) ([]identity.AuthorizedCluster, error) {
	return h.Sessions.AuthorizedClustersForSession(ctx, session)
}

func nativeGrantViews(session identity.ActiveSession) []map[string]any {
	if session.Mode == "first_password_only" || session.MustReenrollMFA {
		return []map[string]any{}
	}
	grants := make([]map[string]any, 0, len(session.Grants))
	for _, grant := range session.Grants {
		grants = append(grants, map[string]any{
			"bindingId":            grant.BindingID,
			"userId":               grant.UserID,
			"tenantId":             grant.TenantID,
			"role":                 grant.Role,
			"scope":                map[string]any{"clusterUids": grant.ClusterUIDs, "namespaces": grant.Namespaces},
			"authorizationVersion": grant.AuthorizationVersion,
			"grantedAt":            grant.GrantedAt,
			"revokedAt":            grant.RevokedAt,
		})
	}
	return grants
}

func nativeClusterViews(clusters []identity.AuthorizedCluster) []map[string]any {
	out := make([]map[string]any, 0, len(clusters))
	for _, cluster := range clusters {
		out = append(out, map[string]any{
			"clusterId": cluster.ClusterID, "clusterUid": cluster.ClusterUID,
			"displayName": cluster.DisplayName,
		})
	}
	return out
}

func nativeIssuedSessionView(session issuedNativeSession, csrfToken string, idleExpiresAt time.Time, state identity.AdministratorState, clusters []identity.AuthorizedCluster) map[string]any {
	active := session.issuedSession()
	return map[string]any{
		"sessionRef":         "session:" + session.SessionID.String(),
		"subject":            session.Subject,
		"tenantId":           session.TenantID,
		"mode":               session.Mode,
		"authMethods":        []string{"password"},
		"acr":                auth.NativeACRLevel1,
		"issuedAt":           session.IssuedAt,
		"absoluteExpiresAt":  session.AbsoluteExpiresAt,
		"idleExpiresAt":      idleExpiresAt,
		"mustChangePassword": session.MustChangePassword,
		"mustReenrollMfa":    session.MustReenrollMFA,
		"accountRevision":    session.AccountRevision,
		"revocation": map[string]any{
			"credentialVersion":    session.CredentialVersion,
			"sessionVersion":       session.SessionVersion,
			"authorizationVersion": session.AuthorizationVersion,
		},
		"csrf": map[string]any{
			"headerName": auth.NativeCSRFHeader, "token": csrfToken,
			"cookieName": auth.NativeSessionCookieName, "sameSite": "Strict",
		},
		"authorization":      map[string]any{"grants": nativeGrantViews(active)},
		"authorizedClusters": nativeClusterViews(clusters),
		"administratorState": map[string]any{
			"activeAdministrators":      state.ActiveAdministrators,
			"isAdministrator":           state.IsAdministrator,
			"isLastActiveAdministrator": state.IsLastActiveAdministrator,
		},
	}
}

func nativeActiveSessionView(session identity.ActiveSession, csrfToken string, idleExpiresAt time.Time, state identity.AdministratorState, clusters []identity.AuthorizedCluster) map[string]any {
	return nativeIssuedSessionView(issuedFromActive(session, "", csrfToken), csrfToken, idleExpiresAt, state, clusters)
}

func apiSuccess(requestID string, data any) map[string]any {
	return map[string]any{"data": data, "requestId": requestID}
}

func writeConstantLoginFailure(w http.ResponseWriter, requestID string) {
	writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "login credentials were not accepted", false, requestID)
}

// verifyDummyPassword spends the same bounded password work as a real account
// so an unknown or locked login name cannot be told apart by timing or by
// capacity behaviour.
func verifyDummyPassword(ctx context.Context, password string) error {
	_, err := identity.VerifyPasswordContext(ctx, password, dummyPasswordHash)
	return err
}

// writePasswordWorkUnavailable maps the bounded password-work result. Congestion
// is a retryable capacity answer and cancellation is a client-side stop: neither
// may be reported as "the password was wrong" nor counted as a failed attempt.
func writePasswordWorkUnavailable(w http.ResponseWriter, err error, requestID string) bool {
	switch {
	case errors.Is(err, identity.ErrPasswordWorkBusy):
		w.Header().Set("Retry-After", "1")
		writeTenantError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "password verification is at capacity; retry shortly", true, requestID)
		return true
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeTenantError(w, http.StatusRequestTimeout, "TIMEOUT", "password verification was cancelled", true, requestID)
		return true
	}
	return false
}

func writeNativeSessionError(w http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, identity.ErrPasswordWorkBusy):
		w.Header().Set("Retry-After", "1")
		writeTenantError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "password verification is at capacity; retry shortly", true, requestID)
	case errors.Is(err, identity.ErrMFAStepUpBudget):
		var budget *identity.StepUpBudgetError
		retryAfter := identity.StepUpBudgetCooldown
		if errors.As(err, &budget) && budget.RetryAfter > 0 {
			retryAfter = budget.RetryAfter
		}
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second).Seconds()))))
		writeTenantError(w, http.StatusTooManyRequests, "RATE_LIMITED", "the account verification attempt budget is exhausted; retry after the cooldown", true, requestID)
	case errors.Is(err, identity.ErrSessionVersion):
		writeTenantError(w, http.StatusConflict, "CONFLICT", "session or credential version is stale", false, requestID)
	case errors.Is(err, identity.ErrSessionNotFound):
		writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "the native session is unavailable", false, requestID)
	case errors.Is(err, identity.ErrSessionRateLimited):
		writeTenantError(w, http.StatusTooManyRequests, "RATE_LIMITED", "native session operation is temporarily unavailable", false, requestID)
	default:
		writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "native session operation failed", true, requestID)
	}
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, target any, requestID string) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxNativeJSONBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid", false, requestID)
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeTenantError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body contains trailing JSON", false, requestID)
		return errors.New("trailing JSON")
	}
	return nil
}

func setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.NativeSessionCookieName, Value: token, Path: "/",
		MaxAge: maxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.NativeSessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func newOpaqueToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate opaque token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func nativeRequestID(r *http.Request) string {
	return auth.RequestIDFromHeader(r.Header.Get("X-Request-ID"))
}
