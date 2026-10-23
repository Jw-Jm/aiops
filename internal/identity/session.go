package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ops-platform/internal/audit"
	"ops-platform/internal/persistence"
)

var (
	ErrSessionNotFound    = errors.New("native session is not active")
	ErrSessionVersion     = errors.New("native session or credential version is stale")
	ErrSessionRateLimited = errors.New("native login is temporarily unavailable")
	ErrTenantInactive     = errors.New("deployment tenant is inactive")
	ErrLoginEntryLimited  = errors.New("login entry is temporarily unavailable")
)

// SessionRepository is the only native HTTP data path. It exposes fixed
// SECURITY DEFINER operations and never serializes password or token material.
type SessionRepository struct {
	pool Pool
}

func NewSessionRepository(pool Pool) (*SessionRepository, error) {
	if pool == nil {
		return nil, ErrRepositoryRequired
	}
	return &SessionRepository{pool: pool}, nil
}

// SessionGrant is a database-owned authorization projection for C-007.
type SessionGrant struct {
	BindingID            uuid.UUID       `json:"bindingId"`
	UserID               uuid.UUID       `json:"userId"`
	TenantID             uuid.UUID       `json:"tenantId"`
	Role                 string          `json:"role"`
	ClusterUIDs          []string        `json:"clusterUids"`
	Namespaces           []string        `json:"namespaces"`
	AuthorizationVersion int             `json:"authorizationVersion"`
	GrantedAt            time.Time       `json:"grantedAt"`
	RevokedAt            *time.Time      `json:"revokedAt"`
	RawScope             json.RawMessage `json:"scope"`
}

type SessionScope struct {
	ClusterUIDs []string `json:"clusterUids"`
	Namespaces  []string `json:"namespaces"`
}

type ActiveSession struct {
	TenantID             uuid.UUID
	UserID               uuid.UUID
	SessionID            uuid.UUID
	Mode                 string
	CSRFDigest           []byte
	IssuedAt             time.Time
	IdleExpiresAt        time.Time
	AbsoluteExpiresAt    time.Time
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
	PasswordHash         string
	Grants               []SessionGrant
}

// MarshalJSON deliberately omits token/CSRF digests and password material.
func (session ActiveSession) MarshalJSON() ([]byte, error) {
	type safeSession struct {
		TenantID             uuid.UUID      `json:"tenantId"`
		UserID               uuid.UUID      `json:"userId"`
		SessionID            uuid.UUID      `json:"sessionId"`
		Mode                 string         `json:"mode"`
		IssuedAt             time.Time      `json:"issuedAt"`
		IdleExpiresAt        time.Time      `json:"idleExpiresAt"`
		AbsoluteExpiresAt    time.Time      `json:"absoluteExpiresAt"`
		Subject              string         `json:"subject"`
		LoginName            string         `json:"loginName"`
		DisplayName          string         `json:"displayName"`
		Status               string         `json:"status"`
		PasswordState        string         `json:"passwordState"`
		MustChangePassword   bool           `json:"mustChangePassword"`
		AccountRevision      int            `json:"accountRevision"`
		CredentialVersion    int            `json:"credentialVersion"`
		AuthorizationVersion int            `json:"authorizationVersion"`
		SessionVersion       int            `json:"sessionVersion"`
		CSRFTokenPresent     bool           `json:"csrfTokenPresent"`
		PasswordHashPresent  bool           `json:"passwordHashPresent"`
		Grants               []SessionGrant `json:"grants"`
	}
	return json.Marshal(safeSession{
		TenantID: session.TenantID, UserID: session.UserID, SessionID: session.SessionID,
		Mode: session.Mode, IssuedAt: session.IssuedAt,
		IdleExpiresAt: session.IdleExpiresAt, AbsoluteExpiresAt: session.AbsoluteExpiresAt,
		Subject: session.Subject, LoginName: session.LoginName,
		DisplayName: session.DisplayName, Status: session.Status,
		PasswordState: session.PasswordState, MustChangePassword: session.MustChangePassword,
		AccountRevision: session.AccountRevision, CredentialVersion: session.CredentialVersion,
		AuthorizationVersion: session.AuthorizationVersion, SessionVersion: session.SessionVersion,
		CSRFTokenPresent:    len(session.CSRFDigest) == 32,
		PasswordHashPresent: session.PasswordHash != "",
		Grants:              append([]SessionGrant(nil), session.Grants...),
	})
}

type CreateSessionInput struct {
	TenantID             uuid.UUID
	UserID               uuid.UUID
	SessionID            uuid.UUID
	Mode                 string
	TokenDigest          []byte
	CSRFDigest           []byte
	CredentialVersion    int
	AuthorizationVersion int
	SessionVersion       int
	IdleExpiresAt        time.Time
	AbsoluteExpiresAt    time.Time
}

type sessionQuerier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func createSessionWith(ctx context.Context, q sessionQuerier, input CreateSessionInput) error {
	if err := validateSessionInput(input); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `
		SELECT identity.create_native_session($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, input.TenantID, input.UserID, input.SessionID, input.Mode,
		input.TokenDigest, input.CSRFDigest,
		input.CredentialVersion, input.AuthorizationVersion, input.SessionVersion,
		input.IdleExpiresAt, input.AbsoluteExpiresAt)
	return mapSessionDBError("create native session", err)
}

func (r *SessionRepository) CreateSession(ctx context.Context, input CreateSessionInput) error {
	if r == nil || r.pool == nil {
		return ErrRepositoryRequired
	}
	return createSessionWith(ctx, r.pool, input)
}

// CreateSessionWithAudit commits the session row and its audit event in the
// same tenant transaction. A caller never gets a usable cookie after a failed
// audit write.
func (r *SessionRepository) CreateSessionWithAudit(ctx context.Context, input CreateSessionInput, entry audit.Entry) (SessionMaterial, error) {
	if r == nil || r.pool == nil {
		return SessionMaterial{}, ErrRepositoryRequired
	}
	if entry.TenantID != input.TenantID {
		return SessionMaterial{}, ErrSessionInvalid
	}
	var material SessionMaterial
	err := persistence.WithTenantTx(ctx, r.pool, input.TenantID, func(tx pgx.Tx) error {
		if err := createSessionWith(ctx, tx, input); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "SELECT identity.clear_password_failure($1,$2)", input.TenantID, input.UserID); err != nil {
			return err
		}
		var err error
		material.Session, err = findActiveSessionWith(ctx, tx, input.TokenDigest)
		if err != nil {
			return err
		}
		material.Clusters, err = authorizedClustersForSessionWith(ctx, tx, material.Session)
		if err != nil {
			return err
		}
		material.State, err = administratorStateWith(ctx, tx, material.Session.TenantID, material.Session.UserID)
		if err != nil {
			return err
		}
		entry.Subject = material.Session.Subject
		_, err = audit.Append(ctx, tx, entry)
		return err
	})
	return material, err
}

func findActiveSessionWith(ctx context.Context, q sessionQuerier, tokenDigest []byte) (ActiveSession, error) {
	var session ActiveSession
	var grantsJSON []byte
	err := q.QueryRow(ctx, `
		SELECT * FROM identity.find_active_session($1)
	`, tokenDigest).Scan(
		&session.TenantID, &session.UserID, &session.SessionID, &session.Mode,
		&session.CSRFDigest, &session.IssuedAt, &session.IdleExpiresAt,
		&session.AbsoluteExpiresAt, &session.Subject, &session.LoginName,
		&session.DisplayName, &session.Status, &session.PasswordState,
		&session.MustChangePassword, &session.AccountRevision,
		&session.CredentialVersion, &session.AuthorizationVersion,
		&session.SessionVersion, &session.PasswordHash, &grantsJSON,
	)
	if err != nil {
		return ActiveSession{}, mapSessionDBError("find native session", err)
	}
	if err := q.QueryRow(ctx, `SELECT identity.mfa_setup_required($1,$2)`, session.TenantID, session.UserID).Scan(&session.MustReenrollMFA); err != nil {
		return ActiveSession{}, mapSessionDBError("read mfa setup requirement", err)
	}
	if err := json.Unmarshal(grantsJSON, &session.Grants); err != nil {
		return ActiveSession{}, fmt.Errorf("decode native session grants: %w", err)
	}
	for index := range session.Grants {
		scope := SessionScope{ClusterUIDs: []string{}, Namespaces: []string{}}
		if err := json.Unmarshal(session.Grants[index].RawScope, &scope); err != nil {
			return ActiveSession{}, fmt.Errorf("decode native session scope: %w", err)
		}
		session.Grants[index].ClusterUIDs = scope.ClusterUIDs
		session.Grants[index].Namespaces = scope.Namespaces
	}
	return session, nil
}

func (r *SessionRepository) FindActiveSession(ctx context.Context, tokenDigest []byte) (ActiveSession, error) {
	if r == nil || r.pool == nil {
		return ActiveSession{}, ErrRepositoryRequired
	}
	return findActiveSessionWith(ctx, r.pool, tokenDigest)
}

func (r *SessionRepository) TouchSession(ctx context.Context, tokenDigest []byte, idleTTL time.Duration) (time.Time, error) {
	if r == nil || r.pool == nil || len(tokenDigest) != 32 || idleTTL <= 0 || idleTTL > 24*time.Hour {
		return time.Time{}, ErrSessionInvalid
	}
	var idleExpiresAt time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT identity.touch_native_session($1,$2)
	`, tokenDigest, int(idleTTL.Seconds())).Scan(&idleExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrSessionNotFound
	}
	if err != nil {
		return time.Time{}, mapSessionDBError("touch native session", err)
	}
	return idleExpiresAt, nil
}

type RevokeSessionInput struct {
	TenantID             uuid.UUID
	UserID               uuid.UUID
	SessionID            uuid.UUID
	RevokedAt            time.Time
	Reason               string
	CredentialVersion    int
	AuthorizationVersion int
	SessionVersion       int
}

type SessionRevocation struct {
	CredentialVersion    int
	SessionVersion       int
	AuthorizationVersion int
}

func revokeSessionWith(ctx context.Context, q sessionQuerier, input RevokeSessionInput) (SessionRevocation, error) {
	var result SessionRevocation
	if input.TenantID == uuid.Nil || input.UserID == uuid.Nil || input.SessionID == uuid.Nil ||
		input.Reason == "" || len(input.Reason) > 512 ||
		input.CredentialVersion < 1 || input.AuthorizationVersion < 1 || input.SessionVersion < 1 {
		return result, ErrSessionInvalid
	}
	err := q.QueryRow(ctx, `
		SELECT * FROM identity.revoke_native_session($1,$2,$3,$4,$5,$6,$7,$8)
	`, input.TenantID, input.UserID, input.SessionID, input.RevokedAt, input.Reason,
		input.CredentialVersion, input.AuthorizationVersion, input.SessionVersion).
		Scan(&result.CredentialVersion, &result.SessionVersion, &result.AuthorizationVersion)
	if err != nil {
		return SessionRevocation{}, mapSessionDBError("revoke native session", err)
	}
	return result, nil
}

func (r *SessionRepository) RevokeSession(ctx context.Context, input RevokeSessionInput) (SessionRevocation, error) {
	if r == nil || r.pool == nil {
		return SessionRevocation{}, ErrRepositoryRequired
	}
	return revokeSessionWith(ctx, r.pool, input)
}

func (r *SessionRepository) RevokeSessionWithAudit(ctx context.Context, input RevokeSessionInput, entry audit.Entry) (SessionRevocation, error) {
	if r == nil || r.pool == nil {
		return SessionRevocation{}, ErrRepositoryRequired
	}
	if entry.TenantID != input.TenantID {
		return SessionRevocation{}, ErrSessionInvalid
	}
	var result SessionRevocation
	err := persistence.WithTenantTx(ctx, r.pool, input.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = revokeSessionWith(ctx, tx, input)
		if err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, entry)
		return err
	})
	return result, err
}

type ChangeOwnPasswordInput struct {
	TenantID                     uuid.UUID
	UserID                       uuid.UUID
	OldSessionID                 uuid.UUID
	NewSessionID                 uuid.UUID
	NewTokenDigest               []byte
	NewCSRFDigest                []byte
	ExpectedCredentialVersion    int
	ExpectedAuthorizationVersion int
	ExpectedSessionVersion       int
	NewPasswordHash              string
	IdleExpiresAt                time.Time
	AbsoluteExpiresAt            time.Time
}

type PasswordChangeResult struct {
	CredentialVersion    int
	SessionVersion       int
	AuthorizationVersion int
	AccountRevision      int
}

func changeOwnPasswordWith(ctx context.Context, q sessionQuerier, input ChangeOwnPasswordInput) (PasswordChangeResult, error) {
	var result PasswordChangeResult
	if input.TenantID == uuid.Nil || input.UserID == uuid.Nil ||
		input.OldSessionID == uuid.Nil || input.NewSessionID == uuid.Nil ||
		len(input.NewTokenDigest) != 32 || len(input.NewCSRFDigest) != 32 ||
		input.ExpectedCredentialVersion < 1 || input.ExpectedAuthorizationVersion < 1 ||
		input.ExpectedSessionVersion < 1 || input.NewPasswordHash == "" {
		return result, ErrSessionInvalid
	}
	err := q.QueryRow(ctx, `
		SELECT * FROM identity.change_own_password(
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12
		)
	`, input.TenantID, input.UserID, input.OldSessionID, input.NewSessionID,
		input.NewTokenDigest, input.NewCSRFDigest,
		input.ExpectedCredentialVersion, input.ExpectedAuthorizationVersion,
		input.ExpectedSessionVersion, input.NewPasswordHash,
		input.IdleExpiresAt, input.AbsoluteExpiresAt).
		Scan(&result.CredentialVersion, &result.SessionVersion,
			&result.AuthorizationVersion, &result.AccountRevision)
	if err != nil {
		return PasswordChangeResult{}, mapSessionDBError("change native password", err)
	}
	return result, nil
}

func (r *SessionRepository) ChangeOwnPassword(ctx context.Context, input ChangeOwnPasswordInput) (PasswordChangeResult, error) {
	if r == nil || r.pool == nil {
		return PasswordChangeResult{}, ErrRepositoryRequired
	}
	return changeOwnPasswordWith(ctx, r.pool, input)
}

func (r *SessionRepository) ChangeOwnPasswordWithAudit(ctx context.Context, input ChangeOwnPasswordInput, entry audit.Entry) (SessionMaterial, error) {
	if r == nil || r.pool == nil {
		return SessionMaterial{}, ErrRepositoryRequired
	}
	if entry.TenantID != input.TenantID {
		return SessionMaterial{}, ErrSessionInvalid
	}
	var material SessionMaterial
	err := persistence.WithTenantTx(ctx, r.pool, input.TenantID, func(tx pgx.Tx) error {
		if _, err := changeOwnPasswordWith(ctx, tx, input); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "SELECT identity.clear_password_failure($1,$2)", input.TenantID, input.UserID); err != nil {
			return err
		}
		var err error
		material.Session, err = findActiveSessionWith(ctx, tx, input.NewTokenDigest)
		if err != nil {
			return err
		}
		material.Clusters, err = authorizedClustersForSessionWith(ctx, tx, material.Session)
		if err != nil {
			return err
		}
		material.State, err = administratorStateWith(ctx, tx, material.Session.TenantID, material.Session.UserID)
		if err != nil {
			return err
		}
		entry.Subject = material.Session.Subject
		_, err = audit.Append(ctx, tx, entry)
		return err
	})
	return material, err
}

func (r *SessionRepository) AdmitLoginAttempt(ctx context.Context, now time.Time, limit int, window, lock time.Duration) (bool, *time.Time, error) {
	if r == nil || r.pool == nil || now.IsZero() || limit < 1 || window <= 0 || lock <= 0 {
		return false, nil, ErrSessionInvalid
	}
	var allowed bool
	var retryAt *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT allowed, retry_at FROM identity.admit_login_attempt($1,$2,$3,$4)
	`, now, limit, int(window.Seconds()), int(lock.Seconds())).Scan(&allowed, &retryAt)
	if err != nil {
		return false, nil, mapSessionDBError("admit native login", err)
	}
	return allowed, retryAt, nil
}

func (r *SessionRepository) RecordKnownLoginFailure(ctx context.Context, tenantID, userID uuid.UUID, failedAt time.Time, limit int, lock time.Duration) (int, *time.Time, error) {
	if r == nil || r.pool == nil || tenantID == uuid.Nil || userID == uuid.Nil || failedAt.IsZero() || limit < 1 || lock <= 0 {
		return 0, nil, ErrSessionInvalid
	}
	var count int
	var lockedUntil *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT out_failed_count, out_locked_until FROM identity.record_known_login_failure($1,$2,$3,$4,$5)
	`, tenantID, userID, failedAt, limit, int(lock.Seconds())).Scan(&count, &lockedUntil)
	if err != nil {
		return 0, nil, mapSessionDBError("record native login failure", err)
	}
	return count, lockedUntil, nil
}

func mapSessionDBError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001":
			return ErrSessionVersion
		case "28000":
			return ErrSessionNotFound
		case "22023", "23514":
			return ErrSessionInvalid
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// ActiveDeploymentTenant validates the fixed startup tenant before any login
// attempt. It does not accept a tenant from a request header or body.
func (r *SessionRepository) ActiveDeploymentTenant(ctx context.Context, tenantID uuid.UUID) error {
	if r == nil || r.pool == nil || tenantID == uuid.Nil {
		return ErrTenantInactive
	}
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT status FROM platform.tenants WHERE tenant_id = $1
	`, tenantID).Scan(&status)
	if err != nil || status != "active" {
		return ErrTenantInactive
	}
	return nil
}

// Account lookup and failure counters are reused from the P2-01 repository.
func (r *SessionRepository) FindLoginAccount(ctx context.Context, loginName string) (LoginAccount, error) {
	accounts, err := NewRepository(r.pool)
	if err != nil {
		return LoginAccount{}, err
	}
	return accounts.FindLoginAccount(ctx, loginName)
}

func (r *SessionRepository) RecordPasswordFailure(ctx context.Context, tenantID, userID uuid.UUID, failedAt, lockedUntil *time.Time) error {
	accounts, err := NewRepository(r.pool)
	if err != nil {
		return err
	}
	return accounts.RecordPasswordFailure(ctx, tenantID, userID, failedAt, lockedUntil)
}

func (r *SessionRepository) ClearPasswordFailure(ctx context.Context, tenantID, userID uuid.UUID) error {
	accounts, err := NewRepository(r.pool)
	if err != nil {
		return err
	}
	return accounts.ClearPasswordFailure(ctx, tenantID, userID)
}

// Pool exposes the least-privilege transaction starter for audit writes only.
func (r *SessionRepository) Pool() Pool { return r.pool }

type AuthorizedCluster struct {
	ClusterID   uuid.UUID `json:"clusterId"`
	ClusterUID  string    `json:"clusterUid"`
	DisplayName string    `json:"displayName"`
}

func resolveClusterIDsWith(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, tenantID uuid.UUID, clusterUIDs []string) (map[string]uuid.UUID, error) {
	result := make(map[string]uuid.UUID, len(clusterUIDs))
	if len(clusterUIDs) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `SELECT cluster_uid, cluster_id FROM identity.resolve_cluster_ids($1,$2)`, tenantID, clusterUIDs)
	if err != nil {
		return nil, mapSessionDBError("resolve native cluster scopes", err)
	}
	defer rows.Close()
	for rows.Next() {
		var uid string
		var id uuid.UUID
		if err := rows.Scan(&uid, &id); err != nil {
			return nil, err
		}
		result[uid] = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, uid := range clusterUIDs {
		if _, ok := result[uid]; !ok {
			return nil, ErrSessionInvalid
		}
	}
	return result, nil
}

func listAuthorizedClustersWith(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, tenantID uuid.UUID, clusterIDs []uuid.UUID) ([]AuthorizedCluster, error) {
	if len(clusterIDs) == 0 {
		return []AuthorizedCluster{}, nil
	}
	rows, err := q.Query(ctx, `SELECT cluster_id, cluster_uid, display_name FROM identity.list_authorized_clusters($1,$2)`, tenantID, clusterIDs)
	if err != nil {
		return nil, mapSessionDBError("list native authorized clusters", err)
	}
	defer rows.Close()
	out := make([]AuthorizedCluster, 0, len(clusterIDs))
	for rows.Next() {
		var item AuthorizedCluster
		if err := rows.Scan(&item.ClusterID, &item.ClusterUID, &item.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SessionRepository) ResolveClusterIDs(ctx context.Context, tenantID uuid.UUID, clusterUIDs []string) (map[string]uuid.UUID, error) {
	if r == nil || r.pool == nil || tenantID == uuid.Nil {
		return nil, ErrSessionInvalid
	}
	var result map[string]uuid.UUID
	err := persistence.WithTenantTx(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
		var err error
		result, err = resolveClusterIDsWith(ctx, tx, tenantID, clusterUIDs)
		return err
	})
	return result, err
}

func (r *SessionRepository) AuthorizedClusters(ctx context.Context, tenantID uuid.UUID, clusterIDs []uuid.UUID) ([]AuthorizedCluster, error) {
	if r == nil || r.pool == nil || tenantID == uuid.Nil {
		return nil, ErrSessionInvalid
	}
	var out []AuthorizedCluster
	err := persistence.WithTenantTx(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = listAuthorizedClustersWith(ctx, tx, tenantID, clusterIDs)
		return err
	})
	return out, err
}

func authorizedClustersForSessionWith(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, session ActiveSession) ([]AuthorizedCluster, error) {
	if session.Mode == "first_password_only" || session.MustReenrollMFA {
		return []AuthorizedCluster{}, nil
	}
	uids := make([]string, 0)
	for _, grant := range session.Grants {
		if grant.Role == "operator" {
			uids = append(uids, grant.ClusterUIDs...)
		}
	}
	if len(uids) == 0 {
		return []AuthorizedCluster{}, nil
	}
	resolved, err := resolveClusterIDsWith(ctx, q, session.TenantID, uids)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(resolved))
	seen := map[uuid.UUID]bool{}
	for _, id := range resolved {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return listAuthorizedClustersWith(ctx, q, session.TenantID, ids)
}

// AuthorizedClustersForSession preserves role semantics: only operator grants
// project operator cluster/namespace scope. Administrator-only scope never
// widens an operator's access.
func (r *SessionRepository) AuthorizedClustersForSession(ctx context.Context, session ActiveSession) ([]AuthorizedCluster, error) {
	if r == nil || r.pool == nil || session.TenantID == uuid.Nil {
		return nil, ErrSessionInvalid
	}
	var out []AuthorizedCluster
	err := persistence.WithTenantTx(ctx, r.pool, session.TenantID, func(tx pgx.Tx) error {
		var err error
		out, err = authorizedClustersForSessionWith(ctx, tx, session)
		return err
	})
	return out, err
}

type AdministratorState struct {
	ActiveAdministrators      int
	IsAdministrator           bool
	IsLastActiveAdministrator bool
}

type SessionMaterial struct {
	Session  ActiveSession
	Clusters []AuthorizedCluster
	State    AdministratorState
}

func administratorStateWith(ctx context.Context, q sessionQuerier, tenantID, userID uuid.UUID) (AdministratorState, error) {
	var result AdministratorState
	err := q.QueryRow(ctx, `SELECT * FROM identity.administrator_state($1,$2)`, tenantID, userID).Scan(
		&result.ActiveAdministrators, &result.IsAdministrator,
		&result.IsLastActiveAdministrator)
	if err != nil {
		return result, mapSessionDBError("find administrator state", err)
	}
	return result, nil
}

func (r *SessionRepository) AdministratorState(ctx context.Context, tenantID, userID uuid.UUID) (AdministratorState, error) {
	if r == nil || r.pool == nil || tenantID == uuid.Nil || userID == uuid.Nil {
		return AdministratorState{}, ErrSessionInvalid
	}
	return administratorStateWith(ctx, r.pool, tenantID, userID)
}

// DeriveCSRFToken binds a stable per-session CSRF token to the opaque session
// token. A cross-site attacker cannot read the HttpOnly cookie, while a page
// reload can recover the same token without storing it in the database, URL,
// localStorage or logs.
func DeriveCSRFToken(sessionToken string) string {
	mac := hmac.New(sha256.New, []byte(sessionToken))
	_, _ = mac.Write([]byte("ops-native-csrf-v1"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// DigestToken derives the database representation of an opaque bearer cookie.
// The plaintext value is never persisted or logged.
func DigestToken(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}

// ConstantTimeDigest compares two SHA-256 digests without early exit.
func ConstantTimeDigest(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare(left, right) == 1
}

func validateSessionInput(input CreateSessionInput) error {
	if input.TenantID == uuid.Nil || input.UserID == uuid.Nil || input.SessionID == uuid.Nil ||
		(input.Mode != "full" && input.Mode != "first_password_only") ||
		len(input.TokenDigest) != 32 || len(input.CSRFDigest) != 32 ||
		input.CredentialVersion < 1 || input.AuthorizationVersion < 1 ||
		input.SessionVersion < 1 ||
		input.IdleExpiresAt.IsZero() || input.AbsoluteExpiresAt.IsZero() ||
		!input.AbsoluteExpiresAt.After(input.IdleExpiresAt) {
		return ErrSessionInvalid
	}
	return nil
}
