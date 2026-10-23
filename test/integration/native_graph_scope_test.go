package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/identity"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
)

func assertNativeGraphScopes(t *testing.T, ctx context.Context, bootstrap, api, worker *pgxpool.Pool, sessions *identity.SessionRepository, client mfaClient, tenant uuid.UUID, target *mfaSession, login, password, secret string, admin mfaSession) {
	t.Helper()
	clusterA, clusterB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uidA, uidB := "native-a-"+clusterA.String(), "native-b-"+clusterB.String()
	err := persistence.WithTenantTx(ctx, api, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'same-name'),($1,$4,$5,'same-name')`, tenant, clusterA, uidA, clusterB, uidB)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingID := uuid.Must(uuid.NewV7())
	scope, _ := json.Marshal(map[string]any{"clusterUids": []string{uidA}, "namespaces": []string{"allowed-ns"}})
	err = persistence.WithTenantTx(ctx, bootstrap, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		var av int
		if err := tx.QueryRow(ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1,account_revision=account_revision+1 WHERE tenant_id=$1 AND user_id=$2 RETURNING authorization_version`, tenant, target.userID).Scan(&av); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'operator',$4,$5,'active')`, tenant, target.userID, bindingID, scope, av)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	*target, _ = mfaLoginWithCookie(t, client, login, password)
	active, err := sessions.FindActiveSession(ctx, identity.DigestToken(target.cookie.Value))
	if err != nil {
		t.Fatal(err)
	}
	nativeCtx := auth.WithNativeSession(ctx, auth.NativeSessionContext{Session: active})
	read := func(ctx context.Context, subject, cluster string) (graph.Scope, error) {
		var result graph.Scope
		err := persistence.WithTenantTx(ctx, api, tenant, func(tx pgx.Tx) error {
			var err error
			result, err = graph.EffectiveTx(ctx, tx, tenant.String(), subject, cluster, true)
			return err
		})
		return result, err
	}
	resourceID := resource.CanonicalID{Domain: "kubernetes", Tenant: tenant.String(), Scope: uidA, APIGroup: "core", Kind: "Pod", StableID: "pod-a"}.String()
	got, err := read(nativeCtx, active.Subject, uidA)
	if err != nil || got.ClusterScoped || !got.Allows(resourceID, "allowed-ns") || got.Allows(resourceID, "other-ns") {
		t.Fatalf("native namespace scope widened or unavailable: %+v %v", got, err)
	}
	if _, err := read(nativeCtx, active.Subject, uidB); err == nil {
		t.Fatal("native grant crossed same-name cluster")
	}
	if _, err := read(ctx, active.Subject, uidA); err == nil {
		t.Fatal("subject prefix guessed native authority without trusted context")
	}
	adminActive, err := sessions.FindActiveSession(ctx, identity.DigestToken(admin.cookie.Value))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := read(auth.WithNativeSession(ctx, auth.NativeSessionContext{Session: adminActive}), adminActive.Subject, uidA); err == nil {
		t.Fatal("administrator inherited operator graph rights")
	}
	assertNativeCommandProof(t, ctx, api, worker, client, *target, active, clusterA, uidA, resourceID, password, secret)
	err = persistence.WithTenantTx(ctx, bootstrap, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1,account_revision=account_revision+1 WHERE tenant_id=$1 AND user_id=$2`, tenant, target.userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='isolated scope regression' WHERE tenant_id=$1 AND binding_id=$2`, tenant, bindingID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := read(nativeCtx, active.Subject, uidA); err == nil {
		t.Fatal("late native graph transaction ignored grant revocation")
	}
	*target, _ = mfaLoginWithCookie(t, client, login, password)
}
