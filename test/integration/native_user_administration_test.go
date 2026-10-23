package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/bundle"
	"ops-platform/internal/identity"
	"ops-platform/internal/persistence"
	"strings"
	"testing"
	"time"
)

// This helper is called only by the guarded, task-owned MFA integration test.
// It waits for real TOTP windows; it never rewinds a factor counter or bypasses
// password/second-factor verification to make an administrative write pass.
func assertNativeUserAdministration(t *testing.T, ctx context.Context, bootstrapPool, pool *pgxpool.Pool, client mfaClient, tenant uuid.UUID, admin *mfaSession, secret string) {
	t.Helper()

	sessions, err := identity.NewSessionRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	profileList := func(session mfaSession) error {
		active, err := sessions.FindActiveSession(ctx, identity.DigestToken(session.cookie.Value))
		if err != nil {
			return err
		}
		actor := auth.RequestContext{TenantID: tenant, Subject: active.Subject, Roles: []auth.Role{auth.PlatformAdmin}, TokenExpiresAt: time.Now().Add(time.Hour)}
		nativeCtx := auth.WithNativeSession(ctx, auth.NativeSessionContext{Session: active})
		_, err = (action.Service{Pool: pool}).ListProfiles(nativeCtx, actor)
		return err
	}
	if err := profileList(*admin); err != nil {
		t.Fatalf("native administrator profile list: %v", err)
	}
	readAccount := func(id string) map[string]any {
		status, payload, raw := client.do(t, http.MethodGet, "/api/v1/admin/users?limit=100", nil, admin)
		if status != 200 {
			t.Fatalf("native account list=%d %s", status, raw)
		}
		for _, item := range payload["data"].([]any) {
			view := item.(map[string]any)
			if view["userId"] == id {
				return view
			}
		}
		t.Fatalf("account %s missing", id)
		return nil
	}
	proof := func(method, path string, body map[string]any) {
		copied := map[string]any{}
		for key, value := range body {
			copied[key] = value
		}
		if password, ok := copied["initialPassword"].(string); ok {
			mac := hmac.New(sha256.New, []byte(admin.csrf))
			mac.Write([]byte(password))
			copied["initialPassword"] = "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
		}
		raw, _ := json.Marshal(copied)
		canonical, err := bundle.CanonicalizeJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		target := map[string]any{"kind": "administrative_write", "write": map[string]any{"method": method, "path": path, "bodyDigest": "sha256:" + identity.DigestHex(identity.DigestSHA256(canonical))}}
		status, payload, message := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{"operation": "user_administration", "target": target, "expectedSessionVersion": admin.sessionVersion}, admin)
		if status != 201 {
			t.Fatalf("native user challenge=%d %s", status, message)
		}
		id := mfaData(t, payload)["challengeId"].(string)
		status, _, message = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+id+":verify", map[string]any{"method": "password", "credential": "mfa-rotated-password", "expectedChallengeRevision": 1}, admin)
		if status != 200 {
			t.Fatalf("native user password=%d %s", status, message)
		}
		code := nextNativeAdminTOTP(t, ctx, pool, tenant, admin, secret)
		status, payload, message = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+id+":verify", map[string]any{"method": "totp", "credential": code, "expectedChallengeRevision": 2}, admin)
		if status != 200 || mfaData(t, payload)["verified"] != true {
			t.Fatalf("native user TOTP=%d %s", status, message)
		}
		admin.stepUpChallenge = id
	}
	write := func(method, path string, body map[string]any, want int) map[string]any {
		proof(method, path, body)
		status, result, message := client.do(t, method, path, body, admin)
		admin.stepUpChallenge = ""
		if status != want {
			t.Fatalf("native user %s %s=%d want%d %s", method, path, status, want, message)
		}
		if want == http.StatusConflict {
			if details, ok := result["details"].(map[string]any); ok {
				return details
			}
			return result
		}
		return mfaData(t, result)
	}
	// The first administrator is still the only active native administrator.
	me := readAccount(admin.userID.String())
	conflict := write(http.MethodPatch, "/api/v1/admin/users/"+admin.userID.String(), map[string]any{"status": "disabled", "expectedRevision": me["revision"]}, 409)
	if conflict["code"] != "last_active_administrator" || readAccount(admin.userID.String())["status"] != "active" {
		t.Fatal("last administrator was not protected")
	}
	login := "managed." + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	initial := "managed-initial-password"
	createBody := map[string]any{"loginName": login, "displayName": "Managed Fixture", "initialPassword": initial}
	proof(http.MethodPost, "/api/v1/admin/users", createBody)
	tampered := map[string]any{"loginName": login, "displayName": "Managed Fixture", "initialPassword": "different-initial-password"}
	if status, _, _ := client.do(t, http.MethodPost, "/api/v1/admin/users", tampered, admin); status != 403 {
		t.Fatalf("changed initial password borrowed proof=%d", status)
	}
	status, created, message := client.do(t, http.MethodPost, "/api/v1/admin/users", createBody, admin)
	admin.stepUpChallenge = ""
	if status != 201 {
		t.Fatalf("managed creation=%d %s", status, message)
	}
	account := mfaData(t, created)
	id := account["userId"].(string)
	path := "/api/v1/admin/users/" + id
	if strings.Contains(message, initial) || strings.Contains(message, "argon2id") || len(account["roleGrants"].([]any)) != 0 || account["mustChangePassword"] != true {
		t.Fatal("account creation leaked credentials or inherited authority")
	}

	// Isolated history fixture: a legacy admin binding must never become a native role.
	legacy := "legacy:reviewed:" + uuid.NewString()
	if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name) VALUES($1,$2,$3,'platform_admin')`, tenant, uuid.New(), legacy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	inventory := identity.SubjectInventory{SchemaVersion: "native-subject-inventory/v1", TenantID: tenant, ReviewedBy: "isolated integration fixture reviewer", Entries: []identity.SubjectInventoryEntry{{LegacySubject: legacy, UserID: uuid.MustParse(id)}}}
	rawInventory, _ := json.Marshal(inventory)
	runtimeRepository, _ := identity.NewRepository(pool)
	if _, err := runtimeRepository.ImportSubjectInventory(ctx, rawInventory); err == nil {
		t.Fatal("API login imported reviewed inventory")
	}
	migrationRepository, _ := identity.NewRepository(bootstrapPool)
	receipt, err := migrationRepository.ImportSubjectInventory(ctx, rawInventory)
	if err != nil || !receipt.Imported {
		t.Fatalf("controlled inventory import: %v", err)
	}
	repeated, err := migrationRepository.ImportSubjectInventory(ctx, rawInventory)
	if err != nil || repeated.Imported || repeated.EvidenceDigest != receipt.EvidenceDigest {
		t.Fatalf("inventory replay: %v", err)
	}
	conflicting := inventory
	conflicting.Entries = []identity.SubjectInventoryEntry{{LegacySubject: legacy, UserID: admin.userID}}
	badInventory, _ := json.Marshal(conflicting)
	if _, err := migrationRepository.ImportSubjectInventory(ctx, badInventory); err == nil {
		t.Fatal("reviewed inventory target silently reassigned")
	}
	mappedSession, _ := mfaLoginWithCookie(t, client, login, initial)
	mappingBody := map[string]any{"legacySubject": legacy, "userId": id, "basis": "verified_inventory", "evidenceDigest": "sha256:" + strings.Repeat("0", 64), "expectedUserRevision": account["revision"]}
	write(http.MethodPost, "/api/v1/admin/subject-mappings", mappingBody, 409)
	// A missing inventory must not mutate the account or copy legacy roles.
	if readAccount(id)["revision"] != account["revision"] {
		t.Fatal("rejected mapping mutated target")
	}
	mappingBody["evidenceDigest"] = receipt.EvidenceDigest
	mapped := write(http.MethodPost, "/api/v1/admin/subject-mappings", mappingBody, 201)
	if mapped["legacySubject"] != legacy || mapped["userId"] != id {
		t.Fatal("mapping response lost exact ownership")
	}
	afterMapping := readAccount(id)
	if len(afterMapping["roleGrants"].([]any)) != 0 || afterMapping["subject"] != account["subject"] {
		t.Fatal("mapping inherited a role or rewrote native principal")
	}
	if status, _, _ := client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &mappedSession); status != 401 {
		t.Fatalf("mapping retained stale target session=%d", status)
	}
	status, mappingList, rawList := client.do(t, http.MethodGet, "/api/v1/admin/subject-mappings?userId="+id, nil, admin)
	if status != 200 || len(mappingList["data"].([]any)) != 1 {
		t.Fatalf("mapping persistence=%d %s", status, rawList)
	}
	target, _ := mfaLoginWithCookie(t, client, login, initial)
	status, session, _ := client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &target)
	if status != 200 || mfaData(t, session)["mode"] != "first_password_only" {
		t.Fatal("created account bypassed first password")
	}
	if status, _, _ := client.do(t, http.MethodPost, "/api/v1/auth/password:change", map[string]any{"currentPassword": initial, "newPassword": "managed-rotated-password", "expectedCredentialVersion": target.credential}, &target); status != http.StatusCreated {
		t.Fatalf("managed first password=%d", status)
	}
	target, _ = mfaLoginWithCookie(t, client, login, "managed-rotated-password")
	cluster, uid := uuid.New(), "managed-cluster-"+uuid.NewString()
	if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'managed fixture; not qualified source')`, tenant, cluster, uid)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	account = readAccount(id)
	version := account["revocation"].(map[string]any)["authorizationVersion"]
	write(http.MethodPost, "/api/v1/admin/role-bindings", map[string]any{"userId": id, "role": "operator", "scope": map[string]any{"clusterUids": []string{uid}, "namespaces": []string{"team-a"}}, "expectedAuthorizationVersion": version, "reason": "isolated test operator grant"}, 201)
	if status, _, _ := client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &target); status != 401 {
		t.Fatalf("grant left old session alive=%d", status)
	}
	account = readAccount(id)
	version = account["revocation"].(map[string]any)["authorizationVersion"]
	write(http.MethodPost, "/api/v1/admin/role-bindings", map[string]any{"userId": id, "role": "platform_admin", "scope": map[string]any{"clusterUids": []string{}, "namespaces": []string{}}, "expectedAuthorizationVersion": version, "reason": "isolated independent admin grant"}, 201)
	profileAdmin, _ := mfaLoginWithCookie(t, client, login, "managed-rotated-password")
	if err := profileList(profileAdmin); err != nil {
		t.Fatalf("independently granted profile reader: %v", err)
	}
	account = readAccount(id)
	grants := account["roleGrants"].([]any)
	if len(grants) != 2 {
		t.Fatal("granting administrator replaced the independent operator")
	}
	// Each administrator has its own authorization version, not one tenant version.
	if account["administratorState"].(map[string]any)["activeAdministrators"] != float64(2) || readAccount(admin.userID.String())["administratorState"].(map[string]any)["activeAdministrators"] != float64(2) {
		t.Fatal("administrator count depended on the viewed account's authorization version")
	}
	var adminBinding string
	for _, item := range grants {
		g := item.(map[string]any)
		if g["role"] == "platform_admin" {
			adminBinding = g["bindingId"].(string)
		}
	}
	write(http.MethodPatch, "/api/v1/admin/role-bindings/"+adminBinding+"/status", map[string]any{"userId": id, "expectedRevision": account["revocation"].(map[string]any)["authorizationVersion"], "status": "disabled", "reason": "isolated admin grant revocation"}, 200)
	if err := profileList(profileAdmin); err == nil {
		t.Fatal("revoked profile administrator still read profiles")
	}
	account = readAccount(id)
	if len(account["roleGrants"].([]any)) != 1 {
		t.Fatal("revoking admin removed independent operator")
	}
	write(http.MethodPatch, path, map[string]any{"displayName": "Edited Fixture", "expectedRevision": account["revision"]}, 200)
	account = readAccount(id)
	reset := write(http.MethodPost, path+"/password:reset", map[string]any{"initialPassword": "managed-reset-password", "expectedRevision": account["revision"], "expectedCredentialVersion": account["revocation"].(map[string]any)["credentialVersion"]}, 200)
	if reset["passwordState"] != "change_required" || reset["auditRecordId"] == nil {
		t.Fatal("password reset omitted forced change or audit")
	}
	target, _ = mfaLoginWithCookie(t, client, login, "managed-reset-password")
	account = readAccount(id)
	write(http.MethodPost, path+"/sessions:revoke", map[string]any{"expectedSessionVersion": account["revocation"].(map[string]any)["sessionVersion"], "reason": "isolated session revocation"}, 200)
	if status, _, _ := client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &target); status != 401 {
		t.Fatalf("revoked target session=%d", status)
	}
	account = readAccount(id)
	write(http.MethodPatch, path, map[string]any{"status": "disabled", "expectedRevision": account["revision"]}, 200)
	if status, _, _ := client.do(t, http.MethodPost, "/api/v1/auth/login", map[string]any{"loginName": login, "password": "managed-reset-password"}, nil); status != 401 {
		t.Fatalf("disabled target login=%d", status)
	}
	account = readAccount(id)
	write(http.MethodPatch, path, map[string]any{"status": "active", "expectedRevision": account["revision"]}, 200)
	if account = readAccount(id); account["status"] != "active" || len(account["roleGrants"].([]any)) != 1 {
		t.Fatal("restore changed independent grant scope")
	}
	// A stale active admin grant is not a current administrator. Updating the
	// independent operator role must not promote that stale admin row.
	staleBinding := uuid.New()
	if err := persistence.WithTenantTx(ctx, bootstrapPool, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) SELECT tenant_id,user_id,$3,'platform_admin','{}',authorization_version-1,'active' FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, tenant, uuid.MustParse(id), staleBinding)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	account = readAccount(id)
	if len(account["roleGrants"].([]any)) != 1 {
		t.Fatal("stale administrator was already effective")
	}
	write(http.MethodPost, "/api/v1/admin/role-bindings", map[string]any{"userId": id, "role": "operator", "scope": map[string]any{"clusterUids": []string{uid}, "namespaces": []string{"team-a"}}, "expectedAuthorizationVersion": account["revocation"].(map[string]any)["authorizationVersion"], "reason": "isolated current operator replacement"}, 201)
	account = readAccount(id)
	if len(account["roleGrants"].([]any)) != 1 || account["administratorState"].(map[string]any)["isAdministrator"] != false {
		t.Fatal("changing operator revived a stale administrator grant")
	}

}

func nextNativeAdminTOTP(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, admin *mfaSession, secret string) string {
	t.Helper()
	last := mfaTenantIntWithPool(t, ctx, pool, tenant, `SELECT COALESCE(out_last_used_step,0) FROM identity.mfa_enrollment_secret($1,$2,(SELECT out_enrollment_id FROM identity.mfa_state($1,$2)),'confirmed')`, tenant, admin.userID)
	step := max(int64(last)+1, identity.TOTPStep(time.Now().UTC()))
	if step > identity.TOTPStep(time.Now().UTC())+1 {
		t.Log("waiting for the next real administrator TOTP window")
	}
	for step > identity.TOTPStep(time.Now().UTC())+1 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	return mfaCodeAt(t, secret, step)
}
