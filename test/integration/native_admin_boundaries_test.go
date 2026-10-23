package integration

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeActiveAdministratorCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID) int {
	t.Helper()
	return mfaTenantInt(t, ctx, pool, tenant, `SELECT count(*) FROM identity.accounts a JOIN identity.role_grants g USING(tenant_id,user_id) WHERE a.tenant_id=$1 AND a.status='active' AND g.role='platform_admin' AND g.status='active' AND g.authorization_version=a.authorization_version`, tenant)
}

func assertNativeAdminAuditRollback(t *testing.T, ctx context.Context, setup, pool *pgxpool.Pool, client mfaClient, tenant uuid.UUID, admin *mfaSession, secret, id string) {
	t.Helper()
	target := uuid.MustParse(id)
	tag := strings.ReplaceAll(uuid.NewString(), "-", "")
	name := pgx.Identifier{"ui_v12_admin_fault_" + tag}.Sanitize()
	function := pgx.Identifier{"audit", "ui_v12_admin_fault_" + tag}.Sanitize()
	// This trigger can reject only this test tenant, target and event; cleanup
	// drops exactly the objects created here, even when an assertion fails.
	install := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.tenant_id='%s'::uuid AND NEW.entity_id='%s'::uuid AND NEW.event_type='identity.admin.update' THEN RAISE EXCEPTION 'task scoped audit fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER %s BEFORE INSERT ON audit.records FOR EACH ROW EXECUTE FUNCTION %s()`, function, tenant, target, name, function)
	ddl := func(statement string) error {
		bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		tx, err := setup.Begin(bounded)
		if err != nil {
			return err
		}
		defer tx.Rollback(bounded)
		if _, err = tx.Exec(bounded, "SET LOCAL ROLE schema_owner"); err != nil {
			return err
		}
		if _, err = tx.Exec(bounded, statement); err != nil {
			return err
		}
		return tx.Commit(bounded)
	}
	if err := ddl(install); err != nil {
		t.Fatal(err)
	}
	removed := false
	remove := func() {
		if removed {
			return
		}
		if err := ddl(fmt.Sprintf("DROP TRIGGER %s ON audit.records; DROP FUNCTION %s()", name, function)); err != nil {
			t.Error(err)
			return
		}
		removed = true
	}
	defer remove()
	revision := mfaTenantInt(t, ctx, setup, tenant, `SELECT account_revision FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, tenant, target)
	audits := mfaTenantIntWithPool(t, ctx, pool, tenant, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND entity_id=$2 AND event_type='identity.admin.update'`, tenant, target)
	path := "/api/v1/admin/users/" + id
	body := map[string]any{"displayName": "Audit Rollback Fixture", "expectedRevision": revision}
	nativeAdminWriteProof(t, ctx, pool, client, tenant, admin, secret, http.MethodPatch, path, body)
	key := uuid.NewString()
	code, _, _ := client.do(t, http.MethodPatch, path, body, admin, key)
	if code != 500 {
		t.Fatalf("injected audit failure status=%d want500", code)
	}
	if got := mfaTenantInt(t, ctx, setup, tenant, `SELECT account_revision FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, tenant, target); got != revision {
		t.Fatal("audit failure committed account mutation")
	}
	if got := mfaTenantIntWithPool(t, ctx, pool, tenant, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND entity_id=$2 AND event_type='identity.admin.update'`, tenant, target); got != audits {
		t.Fatal("failed transaction left business audit")
	}
	remove()
	if !removed {
		t.Fatal("task audit fault cleanup failed")
	}
	code, first, _ := client.do(t, http.MethodPatch, path, body, admin, key)
	if code != 200 {
		t.Fatalf("same proof and idempotency key after rollback=%d want200", code)
	}
	code, replay, _ := client.do(t, http.MethodPatch, path, body, admin, key)
	admin.stepUpChallenge = ""
	if code != 200 || !reflect.DeepEqual(first["data"], replay["data"]) {
		t.Fatal("committed native edit did not replay its exact result")
	}
	if got := mfaTenantInt(t, ctx, setup, tenant, `SELECT account_revision FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, tenant, target); got != revision+1 {
		t.Fatal("retry/replay changed revision more than once")
	}
	if got := mfaTenantIntWithPool(t, ctx, pool, tenant, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND entity_id=$2 AND event_type='identity.admin.update'`, tenant, target); got != audits+1 {
		t.Fatal("retry/replay did not leave exactly one business audit")
	}
	t.Log("native audit failure rolled back account/audit/idempotency; same proof/key retry and replay committed exactly once")
}

func assertNativeAdministratorRace(t *testing.T, ctx context.Context, setup, pool *pgxpool.Pool, client mfaClient, tenant uuid.UUID, admin *mfaSession, secret string) {
	t.Helper()
	if nativeActiveAdministratorCount(t, ctx, setup, tenant) != 1 {
		t.Fatal("race requires this fixture tenant's sole initial administrator")
	}
	login := "race.admin." + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	initial := "race-admin-initial-password"
	body := map[string]any{"loginName": login, "displayName": "Concurrent Administrator Fixture", "initialPassword": initial}
	nativeAdminWriteProof(t, ctx, pool, client, tenant, admin, secret, http.MethodPost, "/api/v1/admin/users", body)
	code, payload, _ := client.do(t, http.MethodPost, "/api/v1/admin/users", body, admin)
	if code != 201 {
		t.Fatalf("race target create=%d", code)
	}
	account := mfaData(t, payload)
	target := uuid.MustParse(account["userId"].(string))
	body = map[string]any{"userId": target.String(), "role": "platform_admin", "scope": map[string]any{"clusterUids": []string{}, "namespaces": []string{}}, "expectedAuthorizationVersion": account["revocation"].(map[string]any)["authorizationVersion"], "reason": "independent concurrent administrator fixture"}
	nativeAdminWriteProof(t, ctx, pool, client, tenant, admin, secret, http.MethodPost, "/api/v1/admin/role-bindings", body)
	code, _, _ = client.do(t, http.MethodPost, "/api/v1/admin/role-bindings", body, admin)
	if code != 201 || nativeActiveAdministratorCount(t, ctx, setup, tenant) != 2 {
		t.Fatal("race requires two independently granted administrators")
	}
	type attempt struct {
		path    string
		body    map[string]any
		session mfaSession
	}
	attempts := []attempt{}
	for _, id := range []uuid.UUID{admin.userID, target} {
		revision := mfaTenantInt(t, ctx, setup, tenant, `SELECT account_revision FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, tenant, id)
		path := "/api/v1/admin/users/" + id.String()
		body := map[string]any{"status": "disabled", "expectedRevision": revision}
		nativeAdminWriteProof(t, ctx, pool, client, tenant, admin, secret, http.MethodPatch, path, body)
		attempts = append(attempts, attempt{path, body, *admin})
	}
	// Hold the existing tenant advisory lock until both real HTTP writes have
	// passed middleware and are waiting inside admin_lock_accounts.
	guard, err := setup.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Rollback(context.WithoutCancel(ctx))
	if _, err = guard.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('identity-admin:'||$1::text,0))`, tenant.String()); err != nil {
		t.Fatal(err)
	}
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, a := range attempts {
		wg.Add(1)
		go func(a attempt) {
			defer wg.Done()
			code, _, _ := client.do(t, http.MethodPatch, a.path, a.body, &a.session)
			results <- code
		}(a)
	}
	waiting := 0
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		err = setup.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid=(((hashtextextended('identity-admin:'||$1::text,0)>>32)&4294967295)::oid) AND objid=((hashtextextended('identity-admin:'||$1::text,0)&4294967295)::oid)`, tenant.String()).Scan(&waiting)
		if err != nil || waiting == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := guard.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(results)
	if err != nil || waiting != 2 {
		t.Fatalf("both HTTP writes did not reach the tenant lock: waiting%d error%v", waiting, err)
	}
	success, denied := 0, 0
	for code := range results {
		switch code {
		case 200:
			success++
		case 401, 403, 409:
			denied++
		default:
			t.Fatalf("concurrent administrator write=%d", code)
		}
	}
	if success != 1 || denied != 1 || nativeActiveAdministratorCount(t, ctx, setup, tenant) != 1 {
		t.Fatal("concurrent demotions failed to preserve exactly one active administrator")
	}
	active := mfaTenantInt(t, ctx, setup, tenant, `SELECT count(*) FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2 AND status='active'`, tenant, admin.userID)
	survivor := *admin
	if active == 0 {
		survivor, _ = mfaLoginWithCookie(t, client, login, initial)
		code, _, _ = client.do(t, http.MethodPost, "/api/v1/auth/password:change", map[string]any{"currentPassword": initial, "newPassword": "race-admin-rotated-password", "expectedCredentialVersion": survivor.credential}, &survivor)
		if code != 201 {
			t.Fatalf("surviving administrator first password=%d", code)
		}
		survivor, _ = mfaLoginWithCookie(t, client, login, "race-admin-rotated-password")
	}
	code, _, _ = client.do(t, http.MethodGet, "/api/v1/admin/users", nil, &survivor)
	if code != 200 {
		t.Fatalf("surviving administrator cannot read native users=%d", code)
	}
	t.Log("two real HTTP demotions waited on the tenant lock; exactly one committed, one denied, surviving administrator logged in and read users")
}
