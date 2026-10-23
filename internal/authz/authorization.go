// Package authz is the single shared authorization layer for business
// transactions. Business packages must not carry their own copy of the
// administrator check: native sessions project roles from identity.role_grants
// while the legacy runtime stored them in platform.role_bindings, and a second
// implementation silently reintroduces the "native administrator is forbidden"
// defect (or, worse, a legacy-authority bypass).
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
)

var (
	// ErrUnauthorized is returned for every denial. Callers translate it to
	// their own public error so a probe cannot distinguish "no such grant" from
	// "wrong tenant".
	ErrUnauthorized = errors.New("authorization denied")
	// ErrIdentityMismatch means the request context and the server-side session
	// disagree. It is a denial, never a fallback to another identity source.
	ErrIdentityMismatch = errors.New("authorization identity mismatch")
)

// Querier is the narrow database surface this layer needs. A pgx.Tx satisfies
// it, so the check runs inside the caller's write transaction (holding its
// locks) rather than in a separate connection.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RequireAdmin re-verifies a platform administrator inside the caller's
// transaction. Exactly one identity source is consulted:
//
//   - a native session is proven only by
//     identity.authorize_native_session_role against the exact server session,
//     its issued credential/authorization/session versions, its revocation and
//     both expiry windows, and the grant itself;
//   - any other session is proven only by the legacy platform.role_bindings row.
//
// A native subject never falls back to platform.role_bindings, and a legacy
// subject never borrows a native grant, so neither path can inherit authority by
// sharing a name or a subject string.
//
// The session binding matters because the middleware runs before the handler's
// transaction: a request admitted with a valid cookie can reach its write after
// the same session was revoked, the password was changed, the account was
// disabled or a version was rotated. Passing only the authorization version (as
// migrations 47/48 did) let such a late transaction commit. The database
// function re-checks the live session row under FOR SHARE inside this
// transaction, so the write either linearizes before the revocation or is
// rejected with no business row and no audit row.
func RequireAdmin(ctx context.Context, q Querier, actor auth.RequestContext) error {
	if q == nil || actor.TenantID == uuid.Nil || actor.Subject == "" {
		return ErrUnauthorized
	}
	if !auth.HasRole(auth.WithRequestContext(context.Background(), actor), auth.PlatformAdmin) {
		return ErrUnauthorized
	}
	if native, ok := auth.NativeSessionFromContext(ctx); ok {
		return requireNativeRole(ctx, q, actor, native, "platform_admin")
	}
	var authorized bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM platform.tenants
			WHERE tenant_id = $1 AND status = 'active'
		) AND EXISTS (
			SELECT 1 FROM platform.role_bindings
			WHERE tenant_id = $1 AND subject = $2
			  AND role_name = 'platform_admin' AND status = 'active'
			FOR SHARE
		)`, actor.TenantID, actor.Subject).Scan(&authorized)
	if err != nil {
		return fmt.Errorf("verify legacy administrator: %w", err)
	}
	if !authorized {
		return ErrUnauthorized
	}
	return nil
}

// NativeRoleAuthorized answers the same question for callers that only hold a
// request context (for example an authority-revision projection that must show
// the same facts the write transaction enforces).
func NativeRoleAuthorized(ctx context.Context, q Querier, actor auth.RequestContext, role string) (bool, error) {
	native, ok := auth.NativeSessionFromContext(ctx)
	if !ok {
		return false, ErrIdentityMismatch
	}
	if err := requireNativeRole(ctx, q, actor, native, role); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// NativeAuthority is the current native authorization proof for an account,
// independent of any live browser session. Queued jobs outlive the session that
// created them, so a Worker cannot present a session id; it must still prove the
// grant from identity.role_grants and include the current authorization version
// in whatever digest it compares, instead of falling back to the legacy binding
// table. Request-path code must call RequireAdmin/NativeRoleAuthorized first.
type NativeAuthority struct {
	AuthorizationVersion int
	BindingIDs           []uuid.UUID
}

func NativeAuthorityFor(ctx context.Context, q Querier, tenantID, userID uuid.UUID, role string) (NativeAuthority, error) {
	if q == nil || tenantID == uuid.Nil || userID == uuid.Nil ||
		(role != string(auth.Operator) && role != string(auth.PlatformAdmin)) {
		return NativeAuthority{}, ErrUnauthorized
	}
	var version int
	var bindings []uuid.UUID
	err := q.QueryRow(ctx, `SELECT authorization_version, binding_ids
		FROM identity.native_role_authority($1, $2, $3)`,
		tenantID, userID, role).Scan(&version, &bindings)
	if errors.Is(err, pgx.ErrNoRows) {
		return NativeAuthority{}, ErrUnauthorized
	}
	if err != nil {
		return NativeAuthority{}, fmt.Errorf("read native %s authority: %w", role, err)
	}
	if version < 1 || len(bindings) == 0 {
		return NativeAuthority{}, ErrUnauthorized
	}
	return NativeAuthority{AuthorizationVersion: version, BindingIDs: bindings}, nil
}

func requireNativeRole(ctx context.Context, q Querier, actor auth.RequestContext, native auth.NativeSessionContext, role string) error {
	session := native.Session
	if session.TenantID != actor.TenantID || session.Subject != actor.Subject ||
		session.UserID == uuid.Nil || session.SessionID == uuid.Nil ||
		session.AuthorizationVersion < 1 || session.CredentialVersion < 1 ||
		session.SessionVersion < 1 || session.Mode != "full" {
		return ErrIdentityMismatch
	}
	// The challenge projection must be bound to this exact session, never to a
	// role name the browser presented.
	if native.FirstPasswordOnly {
		return ErrIdentityMismatch
	}
	var authorized bool
	if err := q.QueryRow(ctx,
		`SELECT identity.authorize_native_session_role($1, $2, $3, $4, $5, $6, $7)`,
		actor.TenantID, session.UserID, session.SessionID, role,
		session.AuthorizationVersion, session.CredentialVersion, session.SessionVersion,
	).Scan(&authorized); err != nil {
		return fmt.Errorf("verify native %s grant: %w", role, err)
	}
	if !authorized {
		return ErrUnauthorized
	}
	return nil
}
