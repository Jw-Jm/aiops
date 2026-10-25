package integration

// MR-017: the API signs an internal context, the Worker re-proves it. This test
// exercises the Worker-side authorization entry points against a real PostgreSQL
// instance: a live native operator session, a revoked or re-granted authority, a
// disabled account, a moved credential/session version, a foreign tenant and a
// legacy context that must not borrow native grants.
//
// It is opt-in (native identity test guard) and is compiled, not executed, in the
// local environment.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
)

func nativeInternalSessionID(t *testing.T, ref string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(strings.TrimPrefix(ref, "session:"))
	if err != nil {
		t.Fatalf("session reference %q: %v", ref, err)
	}
	return id
}

func nativeInternalScope(t *testing.T, f *nativeMFAHardeningFixture, userID uuid.UUID, cluster string, version int) graph.Scope {
	t.Helper()
	var scope graph.Scope
	err := persistence.WithTenantTx(f.ctx, f.apiPool, f.tenantA, func(tx pgx.Tx) error {
		var err error
		scope, err = graph.EffectivePersistedNativeTx(f.ctx, tx, f.tenantA.String(),
			"user:"+userID.String(), cluster, userID, version)
		return err
	})
	if err != nil {
		t.Fatalf("prepare expected scope: %v", err)
	}
	return scope
}

// TestNativeInternalAuthorityReproof is the Worker-side acceptance for MR-017:
// only persisted, current authority reproduces the signed scope, and a native
// context never falls back to the legacy binding table.
func TestNativeInternalAuthorityReproof(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	password := "mfa-hardening-internal-password"
	login, userID, session, _ := nativeMFAHardeningEnrolled(t, f, "mfa.internal.", password)
	sessionID := nativeInternalSessionID(t, session.sessionRef)

	clusterUID := "internal-cluster-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	clusterID := uuid.Must(uuid.NewV7())
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		INSERT INTO platform.cluster_registrations
			(tenant_id, cluster_id, cluster_uid, display_name, status, revision)
		VALUES ($1,$2,$3,'internal authority','active',1)
	`, f.tenantA, clusterID, clusterUID)
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		INSERT INTO identity.role_grants
			(tenant_id, user_id, binding_id, role, scope, authorization_version, status)
		VALUES ($1,$2,$3,'operator',$4::jsonb,1,'active')
	`, f.tenantA, userID, uuid.Must(uuid.NewV7()),
		`{"clusterUids":["`+clusterUID+`"],"namespaces":[]}`)

	expected := nativeInternalScope(t, f, userID, clusterUID, 1)
	claims := graph.Claims{
		Source:   graph.SourceNativeInteractive,
		Native:   &graph.NativeProof{SessionID: sessionID.String()},
		Audience: "platform-graph-query",
		Subject:  session.subject,
		Scope:    expected,
		Deadline: time.Now().Add(10 * time.Second),
	}
	authorization := graph.Authorization{Pool: f.apiPool}
	scope, err := authorization.EffectiveClaim(f.ctx, claims)
	if err != nil {
		t.Fatalf("native interactive claim rejected: %v", err)
	}
	if graph.ScopeDigest(scope) != graph.ScopeDigest(expected) || !scope.ClusterScoped {
		t.Fatalf("rebuilt scope = %+v, want the persisted cluster grant", scope)
	}
	if err := persistence.WithTenantTx(f.ctx, f.apiPool, f.tenantA, func(tx pgx.Tx) error {
		return graph.RequireNativeRoleTx(f.ctx, tx, claims, "operator")
	}); err != nil {
		t.Fatalf("operator role re-proof rejected: %v", err)
	}
	if err := persistence.WithTenantTx(f.ctx, f.apiPool, f.tenantA, func(tx pgx.Tx) error {
		return graph.RequireNativeRoleTx(f.ctx, tx, claims, "platform_admin")
	}); err == nil {
		t.Fatal("an operator-only account passed the platform_admin re-proof")
	}

	// The signed scope is not trusted: widening it must fail even with live grants.
	widened := claims
	widened.Scope = expected
	widened.Scope.Namespaces = append(widened.Scope.Namespaces, "default")
	if _, err := authorization.EffectiveClaim(f.ctx, widened); err == nil {
		t.Fatal("a widened signed scope was accepted")
	}
	// A missing or foreign reference must fail before any scope work.
	missing := claims
	missing.Native = nil
	if _, err := authorization.EffectiveClaim(f.ctx, missing); err == nil {
		t.Fatal("a native claim without a proof was accepted")
	}
	foreign := claims
	foreign.Scope.Tenant = f.tenantB.String()
	if _, err := authorization.EffectiveClaim(f.ctx, foreign); err == nil {
		t.Fatal("a session from another tenant was accepted")
	}
	// A legacy context resolves legacy bindings only. There are none here, so it
	// must not borrow the native operator grant above.
	legacy := claims
	legacy.Source = graph.SourceLegacy
	legacy.Native = nil
	if _, err := authorization.EffectiveClaim(f.ctx, legacy); err == nil {
		t.Fatal("a legacy claim borrowed native authority")
	}

	// Revocation: the live grant disappears, so the frozen decision cannot revive.
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		UPDATE identity.role_grants
		SET status='revoked', revoked_at=clock_timestamp(), revoked_reason='integration revocation'
		WHERE tenant_id=$1 AND user_id=$2 AND role='operator' AND status='active'
	`, f.tenantA, userID)
	if _, err := authorization.EffectiveClaim(f.ctx, claims); err == nil {
		t.Fatal("a revoked operator grant was accepted")
	}

	// Re-grant at a new authorization version must not revive the old proof.
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		UPDATE identity.accounts SET authorization_version=2, session_version=session_version+1,
		    account_revision=account_revision+1 WHERE tenant_id=$1 AND user_id=$2
	`, f.tenantA, userID)
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		INSERT INTO identity.role_grants
			(tenant_id, user_id, binding_id, role, scope, authorization_version, status)
		VALUES ($1,$2,$3,'operator',$4::jsonb,2,'active')
	`, f.tenantA, userID, uuid.Must(uuid.NewV7()),
		`{"clusterUids":["`+clusterUID+`"],"namespaces":[]}`)
	if _, err := authorization.EffectiveClaim(f.ctx, claims); err == nil {
		t.Fatal("a re-granted authority revived a stale signed context")
	}

	// A revoked session and a disabled account each invalidate the interactive
	// reference. A real sign-in after the re-grant yields the fresh session and
	// versions; the test never lowers a version or revives an old session.
	renewed, _ := mfaLoginWithCookie(t, f.client, login, password)
	renewedID := nativeInternalSessionID(t, renewed.sessionRef)
	fresh := graph.Claims{
		Source:   graph.SourceNativeInteractive,
		Native:   &graph.NativeProof{SessionID: renewedID.String()},
		Audience: "platform-graph-query",
		Subject:  renewed.subject,
		Scope:    nativeInternalScope(t, f, userID, clusterUID, 2),
		Deadline: time.Now().Add(10 * time.Second),
	}
	if _, err := authorization.EffectiveClaim(f.ctx, fresh); err != nil {
		t.Fatalf("fresh authority claim rejected: %v", err)
	}
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		UPDATE identity.sessions SET revoked_at=clock_timestamp(), revoked_reason='integration logout'
		WHERE tenant_id=$1 AND session_id=$2
	`, f.tenantA, renewedID)
	if _, err := authorization.EffectiveClaim(f.ctx, fresh); err == nil {
		t.Fatal("a revoked session was accepted")
	}
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		UPDATE identity.accounts SET status='disabled' WHERE tenant_id=$1 AND user_id=$2
	`, f.tenantA, userID)
	if _, err := authorization.EffectiveClaim(f.ctx, fresh); err == nil {
		t.Fatal("a disabled account was accepted")
	}

	// The audit path proves administration, not operator business scope.
	admin := f.admin
	adminClaims := graph.Claims{
		Source:   graph.SourceNativeInteractive,
		Native:   &graph.NativeProof{SessionID: nativeInternalSessionID(t, admin.sessionRef).String()},
		Audience: "platform-audit-verify",
		Subject:  admin.subject,
		Scope:    graph.Scope{Tenant: f.tenantA.String(), Cluster: "audit"},
		Deadline: time.Now().Add(10 * time.Second),
	}
	if err := persistence.WithTenantTx(f.ctx, f.apiPool, f.tenantA, func(tx pgx.Tx) error {
		return graph.RequireNativeRoleTx(f.ctx, tx, adminClaims, "platform_admin")
	}); err != nil {
		t.Fatalf("native platform_admin re-proof rejected: %v", err)
	}
	if _, err := authorization.EffectiveClaim(f.ctx, adminClaims); err == nil {
		t.Fatal("an audit claim was accepted as operator graph scope")
	}
}
