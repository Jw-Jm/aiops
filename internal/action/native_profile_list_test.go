package action

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/identity"
)

type profileListPool struct{ tx *profileListTx }

func (p profileListPool) Begin(context.Context) (pgx.Tx, error) { return p.tx, nil }

type profileListRow struct {
	value any
	err   error
}

func (r profileListRow) Scan(out ...any) error {
	if r.err != nil {
		return r.err
	}
	switch p := out[0].(type) {
	case *bool:
		*p = r.value.(bool)
	case *string:
		*p = r.value.(string)
	default:
		return errors.New("unexpected scan")
	}
	return nil
}

type profileListRows struct{ pgx.Rows }

func (profileListRows) Next() bool { return false }
func (profileListRows) Close()     {}
func (profileListRows) Err() error { return nil }

type profileListTx struct {
	pgx.Tx
	admin, operator bool
	queries         []string
	read            bool
	dbErr           error
}

func (q *profileListTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.queries = append(q.queries, sql)
	if strings.Contains(sql, "set_config") {
		return profileListRow{value: args[0]}
	}
	if strings.Contains(sql, "identity.authorize_native_session_role") {
		role := args[3].(string)
		return profileListRow{value: map[string]bool{"operator": q.operator, "platform_admin": q.admin}[role], err: q.dbErr}
	}
	return profileListRow{err: errors.New("unexpected legacy authorization query")}
}
func (q *profileListTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	q.read = true
	return profileListRows{}, nil
}
func (q *profileListTx) Commit(context.Context) error   { return nil }
func (q *profileListTx) Rollback(context.Context) error { return nil }

// A transaction fixture proves branch/fencing choices, not live SQL or profile visibility.
func TestNativeProfileListUsesCurrentIndependentRoles(t *testing.T) {
	tenant, user, session := uuid.New(), uuid.New(), uuid.New()
	actor := auth.RequestContext{TenantID: tenant, Subject: "opaque-native-principal", Roles: []auth.Role{auth.PlatformAdmin}, TokenExpiresAt: time.Now().Add(time.Hour)}
	native := identity.ActiveSession{TenantID: tenant, UserID: user, SessionID: session, Subject: actor.Subject, Mode: "full", CredentialVersion: 1, SessionVersion: 1, AuthorizationVersion: 2}
	for _, tc := range []struct {
		name                          string
		roles                         []auth.Role
		admin, operator, wrongSubject bool
		dbErr                         error
		allowed                       bool
	}{
		{name: "native administrator", roles: []auth.Role{auth.PlatformAdmin}, admin: true, allowed: true},
		{name: "revoked administrator", roles: []auth.Role{auth.PlatformAdmin}},
		{name: "revoked operator", roles: []auth.Role{auth.Operator}},
		{name: "independent operator", roles: []auth.Role{auth.PlatformAdmin, auth.Operator}, operator: true, allowed: true},
		{name: "identity mismatch", roles: []auth.Role{auth.PlatformAdmin, auth.Operator}, admin: true, operator: true, wrongSubject: true},
		{name: "database failure", roles: []auth.Role{auth.PlatformAdmin}, dbErr: errors.New("db unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := actor
			current.Roles = tc.roles
			if tc.wrongSubject {
				current.Subject = "another principal"
			}
			tx := &profileListTx{admin: tc.admin, operator: tc.operator, dbErr: tc.dbErr}
			ctx := auth.WithNativeSession(context.Background(), auth.NativeSessionContext{Session: native})
			_, err := (Service{Pool: profileListPool{tx}}).ListProfiles(ctx, current)
			if (err == nil) != tc.allowed || tx.read != tc.allowed {
				t.Fatalf("read=%v err=%v allowed=%v", tx.read, err, tc.allowed)
			}
			for _, sql := range tx.queries {
				if strings.Contains(sql, "role_bindings") {
					t.Fatal("native request fell back to legacy")
				}
			}
			if tc.dbErr != nil && !errors.Is(err, tc.dbErr) {
				t.Fatalf("lost DB error: %v", err)
			}
		})
	}
}
