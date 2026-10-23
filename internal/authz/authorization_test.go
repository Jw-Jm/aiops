package authz

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/identity"
)

type fakeRow struct {
	value bool
	err   error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	target, ok := dest[0].(*bool)
	if !ok {
		return errors.New("unexpected scan target")
	}
	*target = r.value
	return nil
}

type fakeQuerier struct {
	queries []string
	args    [][]any
	value   bool
	err     error
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.queries = append(q.queries, sql)
	q.args = append(q.args, args)
	return fakeRow{value: q.value, err: q.err}
}

var (
	tenantID  = uuid.MustParse("293812d4-deb8-4f15-aaa2-5b9aa05e6e3b")
	userID    = uuid.MustParse("01a1627d-4cd4-77b9-bacc-ed412395c861")
	sessionID = uuid.MustParse("01a1627d-4cd4-77b9-bacc-ed412395c862")
)

func nativeContext(t *testing.T, session identity.ActiveSession) context.Context {
	t.Helper()
	return auth.WithNativeSession(context.Background(), auth.NativeSessionContext{Session: session})
}

func nativeSession() identity.ActiveSession {
	return identity.ActiveSession{
		TenantID:             tenantID,
		UserID:               userID,
		SessionID:            sessionID,
		Mode:                 "full",
		Subject:              "user:" + userID.String(),
		CredentialVersion:    7,
		AuthorizationVersion: 4,
		SessionVersion:       9,
	}
}

func adminActor() auth.RequestContext {
	return auth.RequestContext{
		TenantID: tenantID,
		Subject:  "user:" + userID.String(),
		Roles:    []auth.Role{auth.PlatformAdmin},
	}
}

func TestRequireAdminUsesNativeGrantOnly(t *testing.T) {
	q := &fakeQuerier{value: true}
	if err := RequireAdmin(nativeContext(t, nativeSession()), q, adminActor()); err != nil {
		t.Fatalf("native administrator was rejected: %v", err)
	}
	if len(q.queries) != 1 {
		t.Fatalf("expected exactly one authorization query, got %d", len(q.queries))
	}
	if !strings.Contains(q.queries[0], "identity.authorize_native_session_role") {
		t.Fatalf("native session consulted the wrong source: %s", q.queries[0])
	}
	if strings.Contains(q.queries[0], "role_bindings") {
		t.Fatal("native session must never fall back to legacy role bindings")
	}
	if strings.Contains(q.queries[0], "identity.authorize_native_role(") {
		t.Fatal("the unbound check must not remain callable")
	}
	// tenant, user, session, role, authorizationVersion, credentialVersion,
	// sessionVersion: the statement is the whole revocation fence, so every
	// value must be bound rather than rebuilt from a client-supplied role.
	want := []any{tenantID, userID, sessionID, "platform_admin", 4, 7, 9}
	if len(q.args[0]) != len(want) {
		t.Fatalf("expected %d bound arguments, got %d", len(want), len(q.args[0]))
	}
	for index, expected := range want {
		if q.args[0][index] != expected {
			t.Fatalf("argument %d = %#v, want %#v", index, q.args[0][index], expected)
		}
	}
}

// A session that is not a full session, or one the middleware marks as
// first-password-only, must fail before the database is asked anything.
func TestRequireAdminRejectsNonFullNativeSession(t *testing.T) {
	for name, mutate := range map[string]func(*identity.ActiveSession){
		"first password mode": func(s *identity.ActiveSession) { s.Mode = "first_password_only" },
		"missing session id":  func(s *identity.ActiveSession) { s.SessionID = uuid.Nil },
		"missing credential":  func(s *identity.ActiveSession) { s.CredentialVersion = 0 },
		"missing session ver": func(s *identity.ActiveSession) { s.SessionVersion = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			session := nativeSession()
			mutate(&session)
			q := &fakeQuerier{value: true}
			err := RequireAdmin(nativeContext(t, session), q, adminActor())
			if !errors.Is(err, ErrIdentityMismatch) {
				t.Fatalf("want ErrIdentityMismatch, got %v", err)
			}
			if len(q.queries) != 0 {
				t.Fatal("a non-full session must not reach the database")
			}
		})
	}
	// The middleware flag is independent of the stored mode: both must deny.
	q := &fakeQuerier{value: true}
	native := auth.NativeSessionContext{Session: nativeSession(), FirstPasswordOnly: true}
	err := RequireAdmin(auth.WithNativeSession(context.Background(), native), q, adminActor())
	if !errors.Is(err, ErrIdentityMismatch) || len(q.queries) != 0 {
		t.Fatalf("first-password-only context must not be authorized: %v", err)
	}
}

func TestRequireAdminRejectsRevokedNativeGrant(t *testing.T) {
	q := &fakeQuerier{value: false}
	err := RequireAdmin(nativeContext(t, nativeSession()), q, adminActor())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestRequireAdminRejectsMismatchedNativeSession(t *testing.T) {
	for name, mutate := range map[string]func(*identity.ActiveSession){
		"other tenant":   func(s *identity.ActiveSession) { s.TenantID = uuid.MustParse("00000000-0000-4000-8000-000000000009") },
		"other subject":  func(s *identity.ActiveSession) { s.Subject = "user:someone-else" },
		"missing user":   func(s *identity.ActiveSession) { s.UserID = uuid.Nil },
		"no authz value": func(s *identity.ActiveSession) { s.AuthorizationVersion = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			session := nativeSession()
			mutate(&session)
			q := &fakeQuerier{value: true}
			err := RequireAdmin(nativeContext(t, session), q, adminActor())
			if !errors.Is(err, ErrIdentityMismatch) {
				t.Fatalf("want ErrIdentityMismatch, got %v", err)
			}
			if len(q.queries) != 0 {
				t.Fatal("a mismatched session must not reach the database")
			}
		})
	}
}

func TestRequireAdminLegacyPathUsesBindings(t *testing.T) {
	actor := auth.RequestContext{
		TenantID: tenantID,
		Subject:  "legacy-subject",
		Roles:    []auth.Role{auth.PlatformAdmin},
	}
	q := &fakeQuerier{value: true}
	if err := RequireAdmin(context.Background(), q, actor); err != nil {
		t.Fatalf("legacy administrator was rejected: %v", err)
	}
	if !strings.Contains(q.queries[0], "platform.role_bindings") {
		t.Fatalf("legacy session consulted the wrong source: %s", q.queries[0])
	}
	if strings.Contains(q.queries[0], "identity.authorize_native_role") {
		t.Fatal("legacy session must not borrow a native grant")
	}

	q = &fakeQuerier{value: false}
	if err := RequireAdmin(context.Background(), q, actor); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestRequireAdminRequiresContextRole(t *testing.T) {
	q := &fakeQuerier{value: true}
	actor := adminActor()
	actor.Roles = []auth.Role{auth.Operator}
	if err := RequireAdmin(context.Background(), q, actor); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("operator must not pass the administrator check: %v", err)
	}
	if len(q.queries) != 0 {
		t.Fatal("a missing context role must not reach the database")
	}
}

func TestRequireAdminPropagatesDatabaseFailure(t *testing.T) {
	q := &fakeQuerier{err: errors.New("connection reset")}
	if err := RequireAdmin(nativeContext(t, nativeSession()), q, adminActor()); err == nil ||
		errors.Is(err, ErrUnauthorized) {
		t.Fatalf("database failure must surface as an error, got %v", err)
	}
}

func TestNativeRoleAuthorizedProjection(t *testing.T) {
	q := &fakeQuerier{value: true}
	allowed, err := NativeRoleAuthorized(nativeContext(t, nativeSession()), q, adminActor(), "platform_admin")
	if err != nil || !allowed {
		t.Fatalf("native projection denied an active grant: %v %v", allowed, err)
	}
	q = &fakeQuerier{value: false}
	allowed, err = NativeRoleAuthorized(nativeContext(t, nativeSession()), q, adminActor(), "platform_admin")
	if err != nil || allowed {
		t.Fatalf("native projection accepted a revoked grant: %v %v", allowed, err)
	}
	if _, err = NativeRoleAuthorized(context.Background(), q, adminActor(), "platform_admin"); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("legacy context must not use the native projection: %v", err)
	}
}
