package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ops-platform/internal/audit"
	"ops-platform/internal/persistence"
)

var (
	ErrRepositoryRequired     = errors.New("identity database pool is required")
	ErrTenantUnavailable      = errors.New("tenant is unavailable")
	ErrAlreadyInitialized     = errors.New("administrator is already initialized")
	ErrLoginNameUnavailable   = errors.New("login name is unavailable")
	ErrLoginNotFound          = errors.New("login is not available")
	ErrSubjectMappingInvalid  = errors.New("subject mapping is invalid")
	ErrSessionInvalid         = errors.New("session request is invalid")
	ErrCredentialVersionStale = errors.New("credential version is stale")
	ErrAuthorizationStale     = errors.New("authorization version is stale")
	ErrBootstrapDuty          = errors.New("identity bootstrap duty connection is invalid")
	bootstrapActor            = "bootstrap:first-administrator"
)

// Repository is deliberately narrow. P2-01 exposes only the data operations
// with concrete callers: restricted bootstrap, restricted login lookup, and
// durable failure counters. Native HTTP routes arrive in P2-02.
type Pool interface {
	persistence.TxBeginner
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Repository struct {
	pool Pool
}

func NewRepository(pool Pool) (*Repository, error) {
	if pool == nil {
		return nil, ErrRepositoryRequired
	}
	return &Repository{pool: pool}, nil
}

// RequireNativeBootstrapDuty is the connection-time half of the bootstrap
// duty check. It rejects ordinary API, Worker and read-only LOGINs before any
// transaction is opened. The transaction separately verifies SET LOCAL ROLE.
func RequireNativeBootstrapDuty(ctx context.Context, pool Pool) error {
	if pool == nil {
		return ErrRepositoryRequired
	}
	var sessionUser, currentUser string
	var canLogin, superuser, inherit, bypassRLS, migrationMember bool
	err := pool.QueryRow(ctx, `
		SELECT session_user, current_user,
		       r.rolcanlogin, r.rolsuper, r.rolinherit, r.rolbypassrls,
		       pg_has_role(session_user, 'migration_role', 'member')
		FROM pg_roles r WHERE r.rolname = session_user
	`).Scan(&sessionUser, &currentUser, &canLogin, &superuser, &inherit,
		&bypassRLS, &migrationMember)
	if err != nil {
		return ErrBootstrapDuty
	}
	if sessionUser == "" || sessionUser != currentUser || !canLogin ||
		superuser || inherit || bypassRLS || !migrationMember {
		return ErrBootstrapDuty
	}
	return nil
}

// CreateFirstAdministrator runs one audited transaction. A tenant advisory
// lock makes the marker/account sequence linearizable. The marker is inserted
// only after the account exists, so an immediate FK is valid and any failure
// rolls the entire transaction back without leaving account residue.
func (r *Repository) CreateFirstAdministrator(ctx context.Context, input FirstAdministratorInput) (FirstAdministratorResult, error) {
	var result FirstAdministratorResult
	if r == nil || r.pool == nil {
		return result, ErrRepositoryRequired
	}
	input.LoginName = NormalizeLoginName(input.LoginName)
	if err := input.Validate(); err != nil {
		return result, err
	}
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return result, err
	}
	userID, err := uuid.NewV7()
	if err != nil {
		return result, fmt.Errorf("generate user id: %w", err)
	}
	subject := "user:" + userID.String()
	if err := validateSubject(subject); err != nil {
		return result, err
	}

	err = r.withBootstrapTx(ctx, input, func(tx pgx.Tx, tenantID uuid.UUID) error {
		return createFirstAdministratorTx(ctx, tx, tenantID, input, userID, subject, passwordHash, &result)
	})
	if err != nil {
		return FirstAdministratorResult{}, err
	}
	return result, nil
}

// FindLoginAccount uses the fixed SECURITY DEFINER lookup. It never grants the
// caller broad table access and returns at most one globally unique login.
func (r *Repository) FindLoginAccount(ctx context.Context, rawLoginName string) (LoginAccount, error) {
	var account LoginAccount
	if r == nil || r.pool == nil {
		return account, ErrRepositoryRequired
	}
	loginName := NormalizeLoginName(rawLoginName)
	if err := ValidateLoginName(loginName); err != nil {
		return account, err
	}
	err := r.pool.
		QueryRow(ctx, "SELECT * FROM identity.find_login_account($1)", loginName).
		Scan(&account.TenantID, &account.UserID, &account.LoginName, &account.DisplayName,
			&account.Status, &account.PasswordState, &account.MustChangePassword,
			&account.AccountRevision, &account.CredentialVersion,
			&account.AuthorizationVersion, &account.SessionVersion, &account.PasswordHash,
			&account.FailedCount, &account.WindowStartedAt, &account.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginAccount{}, ErrLoginNotFound
	}
	if err != nil {
		return LoginAccount{}, fmt.Errorf("find login account: %w", err)
	}
	return account, nil
}

// RecordPasswordFailure persists the P2-02 rate-limit reservation in the
// database. It does not implement HTTP login policy.
func (r *Repository) RecordPasswordFailure(ctx context.Context, tenantID, userID uuid.UUID, failedAt, lockedUntil *time.Time) error {
	if r == nil || r.pool == nil || tenantID == uuid.Nil || userID == uuid.Nil || failedAt == nil {
		return ErrSessionInvalid
	}
	_, err := r.pool.Exec(ctx,
		"SELECT identity.record_password_failure($1,$2,$3,$4)",
		tenantID, userID, *failedAt, nullableTime(lockedUntil))
	if err != nil {
		return fmt.Errorf("record password failure: %w", err)
	}
	return nil
}

func (r *Repository) ClearPasswordFailure(ctx context.Context, tenantID, userID uuid.UUID) error {
	if r == nil || r.pool == nil || tenantID == uuid.Nil || userID == uuid.Nil {
		return ErrSessionInvalid
	}
	_, err := r.pool.Exec(ctx, "SELECT identity.clear_password_failure($1,$2)", tenantID, userID)
	if err != nil {
		return fmt.Errorf("clear password failure: %w", err)
	}
	return nil
}

// createFirstAdministratorTx installs the first administrator inside an
// already-authorized bootstrap transaction. It is shared by the tenant-explicit
// command and by first-tenant creation so both paths write identical rows.
func createFirstAdministratorTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input FirstAdministratorInput, userID uuid.UUID, subject, passwordHash string, result *FirstAdministratorResult) error {
	// Serialize the rare tenant-level bootstrap before checking the marker.
	// This makes concurrent callers linearizable without deferring FKs and
	// without a loser leaving any account/credential/grant residue.
	if _, err := tx.Exec(ctx, `
			SELECT pg_advisory_xact_lock(
				hashtextextended('identity:first-administrator:' || $1::text, 0)
			)
		`, tenantID.String()); err != nil {
		return fmt.Errorf("lock first administrator bootstrap: %w", err)
	}
	var initialized bool
	if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM identity.administrator_initializations
				WHERE tenant_id = $1
			)
		`, tenantID).Scan(&initialized); err != nil {
		return fmt.Errorf("check first administrator marker: %w", err)
	}
	if initialized {
		return ErrAlreadyInitialized
	}

	if _, err := tx.Exec(ctx, `
			INSERT INTO identity.accounts
				(tenant_id, user_id, login_name, display_name, subject, status,
				 password_state, must_change_password, account_revision,
				 credential_version, authorization_version, session_version)
			VALUES ($1, $2, $3, $4, $5, 'active', 'change_required', true, 1, 1, 1, 1)
		`, tenantID, userID, input.LoginName, input.DisplayName, subject); err != nil {
		return mapAccountWrite(err)
	}
	if _, err := tx.Exec(ctx, `
			INSERT INTO identity.credentials
				(tenant_id, user_id, credential_version, password_hash, status)
			VALUES ($1, $2, 1, $3, 'active')
		`, tenantID, userID, passwordHash); err != nil {
		return fmt.Errorf("store initial credential: %w", err)
	}
	grantID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate first administrator grant id: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			INSERT INTO identity.role_grants
				(tenant_id, user_id, binding_id, role, scope, authorization_version, status)
			VALUES ($1, $2, $3, 'platform_admin', '{}'::jsonb, 1, 'active')
		`, tenantID, userID, grantID); err != nil {
		return fmt.Errorf("grant first administrator: %w", err)
	}
	if _, err := tx.Exec(ctx, `
			INSERT INTO identity.administrator_initializations
				(tenant_id, user_id, actor_subject)
			VALUES ($1, $2, $3)
		`, tenantID, userID, bootstrapActor); err != nil {
		return fmt.Errorf("record first administrator marker: %w", err)
	}

	recordID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate first administrator audit id: %w", err)
	}
	_, err = audit.Append(ctx, tx, audit.Entry{
		TenantID:   tenantID,
		RecordID:   recordID,
		EventType:  "identity.first_administrator.initialized",
		EntityKind: "native_account",
		EntityID:   userID,
		Subject:    bootstrapActor,
		Payload: map[string]any{
			"loginName":            input.LoginName,
			"displayName":          input.DisplayName,
			"role":                 string(RolePlatformAdmin),
			"initialState":         PasswordChangeRequired,
			"accountRevision":      1,
			"authorizationVersion": 1,
			"sessionVersion":       1,
		},
	})
	if err != nil {
		return err
	}

	*result = FirstAdministratorResult{
		TenantID: tenantID, UserID: userID, LoginName: input.LoginName,
		Subject: subject, AccountRevision: 1, CredentialVersion: 1,
		AuthorizationVersion: 1, SessionVersion: 1,
	}
	return nil
}

// withBootstrapTx resolves an active tenant by explicit slug/id before
// installing app.tenant_id. This supports slug bootstrap without ever setting
// a placeholder tenant for RLS.
func (r *Repository) withBootstrapTx(
	ctx context.Context,
	input FirstAdministratorInput,
	fn func(tx pgx.Tx, tenantID uuid.UUID) error,
) error {
	return r.withBootstrapDuty(ctx, func(tx pgx.Tx) error {
		tenantID, err := activeTenantID(ctx, tx, input)
		if err != nil {
			return err
		}
		var installed string
		if err = tx.QueryRow(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()).
			Scan(&installed); err != nil {
			return fmt.Errorf("set identity bootstrap tenant: %w", err)
		}
		if installed != tenantID.String() {
			return fmt.Errorf("set identity bootstrap tenant: database returned a different tenant context")
		}
		return fn(tx, tenantID)
	})
}

// withBootstrapDuty runs fn under the dedicated NOINHERIT migration-duty LOGIN.
// It is the only transaction shape allowed to write identity bootstrap rows; the
// duty checks run before any caller-supplied statement executes.
func (r *Repository) withBootstrapDuty(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin identity bootstrap transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
		return ErrBootstrapDuty
	}
	var roleState struct {
		SessionUser string
		CurrentUser string
		Superuser   bool
		Inherit     bool
		BypassRLS   bool
	}
	if err = tx.QueryRow(ctx, `
		SELECT session_user, current_user, r.rolsuper, r.rolinherit, r.rolbypassrls
		FROM pg_roles r WHERE r.rolname = session_user
	`).Scan(&roleState.SessionUser, &roleState.CurrentUser,
		&roleState.Superuser, &roleState.Inherit, &roleState.BypassRLS); err != nil {
		return ErrBootstrapDuty
	}
	if roleState.SessionUser == "" ||
		roleState.SessionUser == roleState.CurrentUser ||
		roleState.CurrentUser != "migration_role" ||
		roleState.Superuser || roleState.Inherit || roleState.BypassRLS {
		return ErrBootstrapDuty
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit identity bootstrap transaction: %w", err)
	}
	committed = true
	return nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func activeTenantID(ctx context.Context, tx pgx.Tx, input FirstAdministratorInput) (uuid.UUID, error) {
	var tenantID uuid.UUID
	var arg any = input.TenantID
	query := "SELECT tenant_id FROM platform.tenants WHERE status = 'active' AND tenant_id = $1"
	if input.TenantSlug != "" {
		query = "SELECT tenant_id FROM platform.tenants WHERE status = 'active' AND slug = $1"
		arg = strings.TrimSpace(input.TenantSlug)
	}
	err := tx.QueryRow(ctx, query, arg).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrTenantUnavailable
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("verify bootstrap tenant: %w", err)
	}
	return tenantID, nil
}

func mapAccountWrite(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrLoginNameUnavailable
	}
	return fmt.Errorf("create native account: %w", err)
}
