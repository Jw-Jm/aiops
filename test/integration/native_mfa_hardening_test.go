package integration

// MR-014 / MR-020 real PostgreSQL regression.
//
// The suite is opt-in and task-owned, exactly like native_mfa_test.go: it uses
// the guarded native identity environment helpers, applies the forward-only
// migrations on a task-owned instance, and drives the real HTTP handler chain
// with real Argon2id hashes, real sessions and the real step-up budget rows.
//
// What it proves:
//   * an administrator MFA reset keeps the current password hash at the new
//     credential version, so the original password still signs in and lands on
//     the MFA setup gate; re-enrolment restores the existing grants; a stale or
//     cross-tenant reset is refused; two concurrent resets bump the version once;
//     a pre-reset session and a disabled account stay rejected;
//   * step-up failures accumulate per account and factor across challenges,
//     sessions and handler instances; a sixth attempt after five failures is
//     rate limited; the success path is blocked while cooling down; a fully
//     verified password+TOTP step-up clears the budget; a replayed code is still
//     rejected; the recovery-code lock stays independent.
//
// It does not replace the remote-only measurements (API RSS under a login burst)
// and it does not run in a default `go test` invocation.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/identity"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
)

type nativeMFAHardeningFixture struct {
	ctx                context.Context
	client             mfaClient
	secondClient       mfaClient
	bootstrapPool      *pgxpool.Pool
	apiPool            *pgxpool.Pool
	adminDB            *sql.DB
	tenantA            uuid.UUID
	tenantB            uuid.UUID
	admin              mfaSession
	adminLogin         string
	adminPassword      string
	adminSecret        string
	sealerA            *observingSealer
	sealerB            *observingSealer
	workerPool         *pgxpool.Pool
	internalSigningKey ed25519.PrivateKey
	internalPublicKey  ed25519.PublicKey
}

func newNativeMFAHardeningFixture(t *testing.T) *nativeMFAHardeningFixture {
	t.Helper()
	if os.Getenv(nativeIdentityRunEnv) != "1" {
		t.Skipf("%s=1 is required; a default skip is not an HTTP/database pass", nativeIdentityRunEnv)
	}
	if os.Getenv(nativeIdentityTaskEnv) != nativeIdentityTask {
		t.Fatalf("%s must be exactly %q", nativeIdentityTaskEnv, nativeIdentityTask)
	}
	port := os.Getenv(nativeIdentityPortEnv)
	adminDSN, err := nativeIdentityValidateAdminDSN(os.Getenv(nativeIdentityDSNEnv), port)
	if err != nil {
		t.Fatalf("task-owned DSN guard rejected before mutation: %v", err)
	}
	bootstrapDSN, err := nativeIdentityValidateBootstrapDSN(os.Getenv(nativeIdentityBootstrapEnv), port)
	if err != nil {
		t.Fatalf("instance bootstrap DSN guard failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	t.Cleanup(cancel)
	adminDB, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	if _, err := nativeIdentityProbeDatabase(ctx, adminDB); err != nil {
		t.Fatalf("task-owned database guard failed before mutation: %v", err)
	}
	bootstrapDB, err := sql.Open("pgx", bootstrapDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bootstrapDB.Close() })
	migrationDir := "../../migrations"
	goose.SetDialect("postgres")
	if err := goose.UpToContext(ctx, bootstrapDB, migrationDir, 1); err != nil {
		t.Fatalf("apply role bootstrap: %v", err)
	}
	if _, err := bootstrapDB.ExecContext(ctx, `
		GRANT CREATE ON DATABASE ui_v12_native_identity TO schema_owner;
		GRANT schema_owner TO ui_v12_native_owner WITH ADMIN OPTION;
		GRANT migration_role TO ui_v12_native_owner WITH ADMIN OPTION;
	`); err != nil {
		t.Fatalf("grant controlled setup duties: %v", err)
	}
	migrator := newNativeIdentityMigrator(t, ctx, bootstrapDB, adminDSN, migrationDir)
	t.Cleanup(func() { _ = migrator.Close() })
	if err := goose.UpContext(ctx, migrator, migrationDir); err != nil {
		t.Fatalf("apply native identity migrations: %v", err)
	}
	var schemaVersion int
	if err := adminDB.QueryRowContext(ctx, `SELECT max(version_id) FROM public.goose_db_version WHERE is_applied`).Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion < 65 {
		t.Fatalf("schema version = %d, want at least 65 for the MFA hardening migrations", schemaVersion)
	}

	tenantA, tenantB := nativeIdentityCreateTenants(t, ctx, adminDB)
	bootstrapPool := nativeIdentityNewLoginPool(t, ctx, adminDB, adminDSN, "bootstrap", "migration_role", "")
	t.Cleanup(bootstrapPool.Close)
	apiPool := nativeIdentityNewLoginPool(t, ctx, bootstrapDB, adminDSN, "api", "api_runtime_role", "api_runtime_role")
	t.Cleanup(apiPool.Close)
	workerPool := nativeIdentityNewLoginPool(t, ctx, bootstrapDB, adminDSN, "worker", "worker_runtime_role", "worker_runtime_role")
	t.Cleanup(workerPool.Close)
	internalPublic, internalPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	accounts, err := identity.NewRepository(bootstrapPool)
	if err != nil {
		t.Fatal(err)
	}
	adminLogin := "mfa.hard." + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := accounts.CreateFirstAdministrator(ctx, identity.FirstAdministratorInput{
		TenantID: tenantA, LoginName: adminLogin,
		DisplayName: "MFA Hardening Administrator", Password: "mfa-hardening-initial-password",
	}); err != nil {
		t.Fatalf("create first administrator: %v", err)
	}

	sessions, err := identity.NewSessionRepository(apiPool)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "mfa-hardening.key")
	if err := os.WriteFile(keyFile, []byte(mfaTestKeyBase64), 0o600); err != nil {
		t.Fatal(err)
	}
	fileSealer, err := identity.NewFileSecretSealer(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	// Each handler instance gets its own observer over the real sealer, so a test
	// can tell which instance reached the TOTP comparison boundary.
	sealerA := newObservingSealer(fileSealer)
	sealerB := newObservingSealer(fileSealer)
	// A failed assertion may leave a request blocked at the comparison boundary;
	// release both gates before the pools close so cleanups cannot wait on a held
	// transaction.
	t.Cleanup(sealerA.release)
	t.Cleanup(sealerB.release)
	mfaRepository, err := identity.NewMFARepository(sessions, sealerA)
	if err != nil {
		t.Fatal(err)
	}
	nativeHandlers, err := httpapi.NewNativeAuthHandlers(sessions, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	nativeHandlers.MFA = mfaRepository
	nativeMFA, err := httpapi.NewNativeMFAHandlers(mfaRepository, sessions, tenantA, apiPool)
	if err != nil {
		t.Fatal(err)
	}
	nativeAuth, err := auth.NewNativeAuthenticator(sessions, tenantA, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := observability.NewRuntime(ctx, "native-mfa-hardening")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	handler, err := httpapi.NewFoundationHandlerWithNativeMFA(apiPool, nativeAuth,
		configregistry.Ed25519TrustStore{}, runtime, nil, nativeHandlers, nativeMFA)
	if err != nil {
		t.Fatal(err)
	}
	// A second handler over the same database and role stands in for a second API
	// instance: the failure budget must be shared through PostgreSQL, not memory.
	secondRepository, err := identity.NewMFARepository(sessions, sealerB)
	if err != nil {
		t.Fatal(err)
	}
	secondMFA, err := httpapi.NewNativeMFAHandlers(secondRepository, sessions, tenantA, apiPool)
	if err != nil {
		t.Fatal(err)
	}
	secondHandler, err := httpapi.NewFoundationHandlerWithNativeMFA(apiPool, nativeAuth,
		configregistry.Ed25519TrustStore{}, runtime, nil, nativeHandlers, secondMFA)
	if err != nil {
		t.Fatal(err)
	}

	fixture := &nativeMFAHardeningFixture{
		ctx: ctx, client: mfaClient{handler: handler, sessions: sessions},
		secondClient:  mfaClient{handler: secondHandler, sessions: sessions},
		bootstrapPool: bootstrapPool, apiPool: apiPool, adminDB: adminDB,
		sealerA: sealerA, sealerB: sealerB,
		workerPool: workerPool, internalSigningKey: internalPrivate, internalPublicKey: internalPublic,
		tenantA: tenantA, tenantB: tenantB,
		adminLogin: adminLogin, adminPassword: "mfa-hardening-admin-password",
	}
	first, _ := mfaLoginWithCookie(t, fixture.client, adminLogin, "mfa-hardening-initial-password")
	status, _, body := fixture.client.do(t, http.MethodPost, "/api/v1/auth/password:change", map[string]any{
		"currentPassword": "mfa-hardening-initial-password", "newPassword": fixture.adminPassword,
		"expectedCredentialVersion": first.credential,
	}, &first)
	if status != http.StatusCreated {
		t.Fatalf("administrator password change status = %d; body=%s", status, body)
	}
	admin, _ := mfaLoginWithCookie(t, fixture.client, adminLogin, fixture.adminPassword)
	adminSecret, _ := mfaEnrolFactor(t, fixture.client, &admin, fixture.adminPassword)
	admin, _ = mfaLoginWithCookie(t, fixture.client, adminLogin, fixture.adminPassword)
	fixture.admin = admin
	fixture.adminSecret = adminSecret
	return fixture
}

// nativeMFAHardeningAccount inserts one active native account with a known
// password and one operator grant through the controlled bootstrap duty.
func nativeMFAHardeningAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, loginName, password string) uuid.UUID {
	t.Helper()
	userID := uuid.Must(uuid.NewV7())
	hash, err := identity.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	err = persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.accounts
				(tenant_id, user_id, login_name, display_name, subject, status,
				 password_state, must_change_password, account_revision,
				 credential_version, authorization_version, session_version)
			VALUES ($1, $2, $3, 'MFA Hardening Target', $4, 'active', 'active', false, 1, 1, 1, 1)
		`, tenantID, userID, loginName, "user:"+userID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.credentials
				(tenant_id, user_id, credential_version, password_hash, status)
			VALUES ($1, $2, 1, $3, 'active')
		`, tenantID, userID, hash); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO identity.role_grants
				(tenant_id, user_id, binding_id, role, scope, authorization_version, status)
			VALUES ($1, $2, $3, 'operator', '{}'::jsonb, 1, 'active')
		`, tenantID, userID, uuid.Must(uuid.NewV7()))
		return err
	})
	if err != nil {
		t.Fatalf("create native account %s: %v", loginName, err)
	}
	return userID
}

func nativeMFAHardeningVersions(t *testing.T, f *nativeMFAHardeningFixture, tenantID, userID uuid.UUID) (int, int) {
	t.Helper()
	var revision, credential int
	err := persistence.WithTenantTx(f.ctx, f.bootstrapPool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(f.ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		return tx.QueryRow(f.ctx, `
			SELECT account_revision, credential_version FROM identity.accounts
			WHERE tenant_id=$1 AND user_id=$2
		`, tenantID, userID).Scan(&revision, &credential)
	})
	if err != nil {
		t.Fatal(err)
	}
	return revision, credential
}

func nativeMFAHardeningString(t *testing.T, f *nativeMFAHardeningFixture, tenantID uuid.UUID, query string, args ...any) string {
	t.Helper()
	var value string
	err := persistence.WithTenantTx(f.ctx, f.bootstrapPool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(f.ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		return tx.QueryRow(f.ctx, query, args...).Scan(&value)
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// nativeMFAHardeningResetProof mints one target-bound mfa_reset step-up.
func nativeMFAHardeningResetProof(t *testing.T, f *nativeMFAHardeningFixture, client mfaClient, session *mfaSession, target uuid.UUID, revision int) string {
	t.Helper()
	status, payload, body := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation": "mfa_reset",
		"target": map[string]any{
			"kind": "user", "userId": target.String(), "revision": revision,
		},
		"expectedSessionVersion": session.sessionVersion,
	}, session)
	if status != http.StatusCreated {
		t.Fatalf("mfa reset challenge status = %d; body=%s", status, body)
	}
	data := mfaData(t, payload)
	challengeID := data["challengeId"].(string)
	revision = int(data["revision"].(float64))
	status, _, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{
		"method": "password", "credential": f.adminPassword, "expectedChallengeRevision": revision,
	}, session)
	if status != http.StatusOK {
		t.Fatalf("mfa reset password status = %d; body=%s", status, body)
	}
	revision++
	status, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{
		"method": "totp", "credential": nextNativeAdminTOTP(t, f.ctx, f.apiPool, f.tenantA, session, f.adminSecret),
		"expectedChallengeRevision": revision,
	}, session)
	if status != http.StatusOK || mfaData(t, payload)["verified"] != true {
		t.Fatalf("mfa reset totp status = %d; body=%s", status, body)
	}
	return challengeID
}

func nativeMFAHardeningResetRequest(t *testing.T, f *nativeMFAHardeningFixture, session *mfaSession, target uuid.UUID, loginName string, revision, credential int, challengeID string) (int, map[string]any, string) {
	t.Helper()
	return f.client.do(t, http.MethodPost, "/api/v1/admin/users/"+target.String()+"/mfa:reset", map[string]any{
		"userId": target.String(), "confirmLoginName": loginName,
		"reason":                    "integration reset of a lost second factor",
		"expectedUserRevision":      revision,
		"expectedCredentialVersion": credential,
		"stepUp": map[string]any{
			"challengeId": challengeID, "sessionRef": session.sessionRef,
		},
	}, session)
}

func nativeMFAHardeningWrongCode(t *testing.T, secret string) string {
	t.Helper()
	now := identity.TOTPStep(time.Now().UTC())
	valid := map[string]bool{}
	for step := now - 1; step <= now+1; step++ {
		code, err := identity.TOTPCodeAt(secret, step)
		if err != nil {
			t.Fatal(err)
		}
		valid[code] = true
	}
	for _, candidate := range []string{"000000", "111111", "222222", "333333", "444444"} {
		if !valid[candidate] {
			return candidate
		}
	}
	t.Fatal("no wrong TOTP candidate is available")
	return ""
}

// nativeMFAHardeningChallengeFor creates one self-targeted challenge for the
// given operation and returns its id and revision.
func nativeMFAHardeningChallengeFor(t *testing.T, client mfaClient, session *mfaSession, operation string) (string, int) {
	t.Helper()
	status, payload, body := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation": operation,
		"target": map[string]any{
			"kind": "user", "userId": session.userID.String(), "revision": session.accountRevision,
		},
		"expectedSessionVersion": session.sessionVersion,
	}, session)
	if status != http.StatusCreated {
		t.Fatalf("%s challenge status = %d; body=%s", operation, status, body)
	}
	data := mfaData(t, payload)
	return data["challengeId"].(string), int(data["revision"].(float64))
}

func nativeMFAHardeningChallenge(t *testing.T, client mfaClient, session *mfaSession) (string, int) {
	t.Helper()
	return nativeMFAHardeningChallengeFor(t, client, session, "user_administration")
}

func nativeMFAHardeningVerify(t *testing.T, client mfaClient, session *mfaSession, challengeID string, revision int, method, credential string) (int, map[string]any, string) {
	t.Helper()
	return client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{
		"method": method, "credential": credential, "expectedChallengeRevision": revision,
	}, session)
}

// mfaAttemptLogin performs a raw login and returns the status.
func mfaAttemptLogin(t *testing.T, client mfaClient, loginName, password string) (int, map[string]any, string) {
	t.Helper()
	return client.do(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"loginName": loginName, "password": password,
	}, nil)
}

// TestNativeMFAResetPreservesThePasswordVersion is the MR-014 regression.
func TestNativeMFAResetPreservesThePasswordVersion(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	targetLogin := "mfa.hard.target." + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	targetPassword := "mfa-hardening-target-password"
	targetUserID := nativeMFAHardeningAccount(t, f.ctx, f.bootstrapPool, f.tenantA, targetLogin, targetPassword)
	foreignLogin := "mfa.hard.foreign." + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	foreignUserID := nativeMFAHardeningAccount(t, f.ctx, f.bootstrapPool, f.tenantB, foreignLogin, targetPassword)

	admin := f.admin
	target, _ := mfaLoginWithCookie(t, f.client, targetLogin, targetPassword)
	targetSecret, _ := mfaEnrolFactor(t, f.client, &target, targetPassword)
	if targetSecret == "" {
		t.Fatal("target enrolment returned no secret")
	}
	oldSession := target

	revision, credential := nativeMFAHardeningVersions(t, f, f.tenantA, targetUserID)
	oldHash := nativeMFAHardeningString(t, f, f.tenantA, `
		SELECT password_hash FROM identity.credentials
		WHERE tenant_id=$1 AND user_id=$2 AND credential_version=$3
	`, f.tenantA, targetUserID, credential)
	resetChallenge := nativeMFAHardeningResetProof(t, f, f.client, &admin, targetUserID, revision)
	status, payload, body := nativeMFAHardeningResetRequest(t, f, &admin, targetUserID, targetLogin, revision, credential, resetChallenge)
	if status != http.StatusOK {
		t.Fatalf("administrator reset status = %d; body=%s", status, body)
	}
	reset := mfaData(t, payload)
	if reset["mfaState"] != "reset_required" {
		t.Fatalf("reset state = %v", reset["mfaState"])
	}
	if int(reset["credentialVersion"].(float64)) != credential+1 {
		t.Fatalf("reset credential version = %v, want %d", reset["credentialVersion"], credential+1)
	}
	if status, _, _ := f.client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &oldSession); status != http.StatusUnauthorized {
		t.Fatalf("pre-reset session status = %d, want 401", status)
	}

	if active := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT count(*) FROM identity.credentials
		WHERE tenant_id=$1 AND user_id=$2 AND credential_version=$3 AND status='active'
	`, f.tenantA, targetUserID, credential+1); active != 1 {
		t.Fatalf("active credentials at the new version = %d, want 1", active)
	}
	if retired := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT count(*) FROM identity.credentials
		WHERE tenant_id=$1 AND user_id=$2 AND credential_version=$3 AND status='retired'
	`, f.tenantA, targetUserID, credential); retired != 1 {
		t.Fatalf("old credential version retired count = %d, want 1", retired)
	}
	if preserved := nativeMFAHardeningString(t, f, f.tenantA, `
		SELECT password_hash FROM identity.credentials
		WHERE tenant_id=$1 AND user_id=$2 AND credential_version=$3
	`, f.tenantA, targetUserID, credential+1); preserved != oldHash {
		t.Fatal("the reset did not preserve the exact password hash")
	}

	// The unchanged password still signs in, but only into the MFA setup gate.
	gated, _ := mfaLoginWithCookie(t, f.client, targetLogin, targetPassword)
	status, payload, body = f.client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &gated)
	if status != http.StatusOK {
		t.Fatalf("session after reset status = %d; body=%s", status, body)
	}
	view := mfaData(t, payload)
	if view["mustReenrollMfa"] != true {
		t.Fatalf("session after reset is not gated: %v", view["mustReenrollMfa"])
	}
	if grants := view["authorization"].(map[string]any)["grants"].([]any); len(grants) != 0 {
		t.Fatalf("the reset session kept %d grants", len(grants))
	}
	if status, _, _ := f.client.do(t, http.MethodGet, "/api/v1/admin/clusters", nil, &gated); status != http.StatusForbidden {
		t.Fatalf("business access before re-enrolment = %d, want 403", status)
	}
	if status, _, _ := mfaAttemptLogin(t, f.client, targetLogin, "mfa-hardening-wrong-password"); status != http.StatusUnauthorized {
		t.Fatalf("wrong password after reset = %d, want 401", status)
	}

	// Re-enrolment lifts the gate and restores the pre-existing authorization.
	if _, enrolled := mfaEnrolFactor(t, f.client, &gated, targetPassword); enrolled == "" {
		t.Fatal("re-enrolment after reset failed")
	}
	status, payload, body = f.client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &gated)
	if status != http.StatusOK {
		t.Fatalf("session after re-enrolment status = %d; body=%s", status, body)
	}
	view = mfaData(t, payload)
	if view["mustReenrollMfa"] != false {
		t.Fatalf("re-enrolment did not clear the setup gate: %v", view["mustReenrollMfa"])
	}
	if grants := view["authorization"].(map[string]any)["grants"].([]any); len(grants) == 0 {
		t.Fatal("re-enrolment did not restore the existing grants")
	}

	// A password change after the first reset must become the version the next
	// reset preserves: the pre-change password may never come back.
	rotatedPassword := "mfa-hardening-target-rotated"
	status, _, body = f.client.do(t, http.MethodPost, "/api/v1/auth/password:change", map[string]any{
		"currentPassword": targetPassword, "newPassword": rotatedPassword,
		"expectedCredentialVersion": gated.credential,
	}, &gated)
	if status != http.StatusCreated {
		t.Fatalf("target password change status = %d; body=%s", status, body)
	}
	if status, _, _ := mfaAttemptLogin(t, f.client, targetLogin, targetPassword); status != http.StatusUnauthorized {
		t.Fatalf("pre-change password after a change = %d, want 401", status)
	}

	// A repeated reset keeps the same invariant for the rotated hash.
	revision, credential = nativeMFAHardeningVersions(t, f, f.tenantA, targetUserID)
	secondChallenge := nativeMFAHardeningResetProof(t, f, f.client, &admin, targetUserID, revision)
	status, _, body = nativeMFAHardeningResetRequest(t, f, &admin, targetUserID, targetLogin, revision, credential, secondChallenge)
	if status != http.StatusOK {
		t.Fatalf("repeated reset status = %d; body=%s", status, body)
	}
	rotated, _ := mfaLoginWithCookie(t, f.client, targetLogin, rotatedPassword)
	if rotated.credential != credential+1 {
		t.Fatalf("rotated credential version = %d, want %d", rotated.credential, credential+1)
	}
	if status, _, _ := mfaAttemptLogin(t, f.client, targetLogin, targetPassword); status != http.StatusUnauthorized {
		t.Fatalf("the pre-change password was revived by the reset = %d", status)
	}

	// Two resets with the same expected versions: exactly one may win.
	revision, credential = nativeMFAHardeningVersions(t, f, f.tenantA, targetUserID)
	firstProof := nativeMFAHardeningResetProof(t, f, f.client, &admin, targetUserID, revision)
	secondProof := nativeMFAHardeningResetProof(t, f, f.secondClient, &admin, targetUserID, revision)
	var wait sync.WaitGroup
	codes := make(chan int, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		status, _, _ := nativeMFAHardeningResetRequest(t, f, &admin, targetUserID, targetLogin, revision, credential, firstProof)
		codes <- status
	}()
	go func() {
		defer wait.Done()
		status, _, _ := nativeMFAHardeningResetRequest(t, f, &admin, targetUserID, targetLogin, revision, credential, secondProof)
		codes <- status
	}()
	wait.Wait()
	close(codes)
	accepted, stale := 0, 0
	for code := range codes {
		switch code {
		case http.StatusOK:
			accepted++
		case http.StatusConflict:
			stale++
		default:
			t.Fatalf("concurrent reset returned %d, want 200 or 409", code)
		}
	}
	if accepted != 1 || stale != 1 {
		t.Fatalf("concurrent resets accepted=%d stale=%d, want 1/1", accepted, stale)
	}
	_, afterConcurrent := nativeMFAHardeningVersions(t, f, f.tenantA, targetUserID)
	if afterConcurrent != credential+1 {
		t.Fatalf("credential version after concurrent resets = %d, want %d", afterConcurrent, credential+1)
	}

	// Cross tenant: an administrator of tenant A cannot reset a tenant B account.
	foreignRevision, _ := nativeMFAHardeningVersions(t, f, f.tenantB, foreignUserID)
	foreignChallenge := nativeMFAHardeningResetProof(t, f, f.client, &admin, foreignUserID, foreignRevision)
	if status, _, _ := nativeMFAHardeningResetRequest(t, f, &admin, foreignUserID, foreignLogin, foreignRevision, 1, foreignChallenge); status != http.StatusConflict {
		t.Fatalf("cross-tenant reset = %d, want 409", status)
	}

	// A disabled account cannot use the preserved password.
	disableErr := persistence.WithTenantTx(f.ctx, f.bootstrapPool, f.tenantA, func(tx pgx.Tx) error {
		if _, err := tx.Exec(f.ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		_, err := tx.Exec(f.ctx, `
			UPDATE identity.accounts SET status='disabled', session_version=session_version+1,
				account_revision=account_revision+1
			WHERE tenant_id=$1 AND user_id=$2
		`, f.tenantA, targetUserID)
		return err
	})
	if disableErr != nil {
		t.Fatal(disableErr)
	}
	if status, _, _ := mfaAttemptLogin(t, f.client, targetLogin, rotatedPassword); status != http.StatusUnauthorized {
		t.Fatalf("disabled account login = %d, want 401", status)
	}
}

// TestNativeStepUpFailureBudgetSurvivesChallenges is the MR-020 regression.
func TestNativeStepUpFailureBudgetSurvivesChallenges(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	password := "mfa-hardening-budget-password"

	totpLogin := "mfa.hard.totp." + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	totpUserID := nativeMFAHardeningAccount(t, f.ctx, f.bootstrapPool, f.tenantA, totpLogin, password)
	firstSession, _ := mfaLoginWithCookie(t, f.client, totpLogin, password)
	secondSession, _ := mfaLoginWithCookie(t, f.client, totpLogin, password)
	totpSecret, _ := mfaEnrolFactor(t, f.client, &firstSession, password)
	if totpSecret == "" {
		t.Fatal("no confirmed factor for the budget account")
	}

	// A challenge created before the lock must be refused at verification time.
	// It uses a different operation so later creations cannot revoke it.
	spareID, spareRevision := nativeMFAHardeningChallengeFor(t, f.secondClient, &secondSession, "mfa_reset")
	wrong := nativeMFAHardeningWrongCode(t, totpSecret)
	expectedRemaining := []float64{4, 3, 2, 1, 0}
	for attempt := 0; attempt < 5; attempt++ {
		client := f.client
		session := &firstSession
		if attempt%2 == 1 {
			client = f.secondClient
			session = &secondSession
		}
		challengeID, revision := nativeMFAHardeningChallenge(t, client, session)
		status, payload, body := nativeMFAHardeningVerify(t, client, session, challengeID, revision, "totp", wrong)
		if status != http.StatusOK {
			t.Fatalf("failure %d status = %d; body=%s", attempt+1, status, body)
		}
		result := mfaData(t, payload)
		if result["verified"] != false {
			t.Fatalf("failure %d was accepted", attempt+1)
		}
		if result["remainingAttempts"] != expectedRemaining[attempt] {
			t.Fatalf("failure %d remainingAttempts = %v, want %v", attempt+1,
				result["remainingAttempts"], expectedRemaining[attempt])
		}
	}
	if status, _, body := nativeMFAHardeningVerify(t, f.secondClient, &secondSession, spareID, spareRevision, "totp", wrong); status != http.StatusTooManyRequests {
		t.Fatalf("verification on a cooling-down account = %d, want 429; body=%s", status, body)
	}
	if status, _, body := nativeMFAHardeningChallengeBudget(t, f.client, &firstSession); status != http.StatusTooManyRequests {
		t.Fatalf("challenge creation on a cooling-down account = %d, want 429; body=%s", status, body)
	}
	if stored := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT count(*) FROM identity.step_up_failures
		WHERE tenant_id=$1 AND user_id=$2 AND factor='totp'
		  AND failed_count=5 AND locked_until > clock_timestamp()
	`, f.tenantA, totpUserID); stored != 1 {
		t.Fatalf("persisted locked TOTP budget rows = %d, want 1", stored)
	}

	// Recovery codes keep their own lock and remain reachable.
	status, payload, body := f.client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation": "recovery_code_regenerate",
		"target": map[string]any{
			"kind": "user", "userId": firstSession.userID.String(), "revision": firstSession.accountRevision,
		},
		"expectedSessionVersion": firstSession.sessionVersion,
	}, &firstSession)
	if status != http.StatusCreated {
		t.Fatalf("recovery challenge while the factor budget is locked = %d; body=%s", status, body)
	}
	recovery := mfaData(t, payload)
	status, payload, body = f.client.do(t, http.MethodPost, "/api/v1/auth/mfa/recovery-codes:verify", map[string]any{
		"challengeId": recovery["challengeId"], "recoveryCode": "AAAA-AAAA",
		"expectedChallengeRevision": recovery["revision"],
	}, &firstSession)
	if status != http.StatusOK || mfaData(t, payload)["verified"] != false {
		t.Fatalf("recovery verification status = %d; body=%s", status, body)
	}

	// A fully verified password+TOTP step-up clears the budget.
	clearLogin := "mfa.hard.clear." + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	clearUserID := nativeMFAHardeningAccount(t, f.ctx, f.bootstrapPool, f.tenantA, clearLogin, password)
	clearSession, _ := mfaLoginWithCookie(t, f.client, clearLogin, password)
	clearSecret, _ := mfaEnrolFactor(t, f.client, &clearSession, password)
	if clearSecret == "" {
		t.Fatal("no confirmed factor for the clearing account")
	}
	clearID, clearRevision := nativeMFAHardeningChallenge(t, f.client, &clearSession)
	status, _, body = nativeMFAHardeningVerify(t, f.client, &clearSession, clearID, clearRevision, "password", password)
	if status != http.StatusOK {
		t.Fatalf("correct password status = %d; body=%s", status, body)
	}
	clearRevision++
	for attempt := 0; attempt < 4; attempt++ {
		status, payload, body = nativeMFAHardeningVerify(t, f.client, &clearSession, clearID, clearRevision, "totp", nativeMFAHardeningWrongCode(t, clearSecret))
		if status != http.StatusOK {
			t.Fatalf("clearing failure %d status = %d; body=%s", attempt+1, status, body)
		}
		clearRevision = int(mfaData(t, payload)["revision"].(float64))
	}
	successCode := mfaCurrentCode(t, clearSecret)
	status, payload, body = nativeMFAHardeningVerify(t, f.client, &clearSession, clearID, clearRevision, "totp", successCode)
	if status != http.StatusOK || mfaData(t, payload)["verified"] != true {
		t.Fatalf("successful second factor status = %d; body=%s", status, body)
	}
	if remaining := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT count(*) FROM identity.step_up_failures WHERE tenant_id=$1 AND user_id=$2
	`, f.tenantA, clearUserID); remaining != 0 {
		t.Fatalf("success did not clear the budget: %d rows", remaining)
	}
	// Replaying the consumed time step is still rejected and counts as a failure.
	replayID, replayRevision := nativeMFAHardeningChallenge(t, f.client, &clearSession)
	status, _, body = nativeMFAHardeningVerify(t, f.client, &clearSession, replayID, replayRevision, "password", password)
	if status != http.StatusOK {
		t.Fatalf("replay password status = %d; body=%s", status, body)
	}
	replayRevision++
	status, payload, body = nativeMFAHardeningVerify(t, f.client, &clearSession, replayID, replayRevision, "totp", successCode)
	if status != http.StatusOK || mfaData(t, payload)["verified"] != false {
		t.Fatalf("replayed code status = %d; body=%s", status, body)
	}

	// Concurrent failures from distinct sessions and two handler instances must
	// all be counted. Distinct sessions are required because one pending challenge
	// per session and operation is the frozen rule.
	concurrentLogin := "mfa.hard.race." + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	concurrentUserID := nativeMFAHardeningAccount(t, f.ctx, f.bootstrapPool, f.tenantA, concurrentLogin, password)
	raceSessions := make([]mfaSession, 0, 5)
	for index := 0; index < 5; index++ {
		session, _ := mfaLoginWithCookie(t, f.client, concurrentLogin, password)
		raceSessions = append(raceSessions, session)
	}
	raceSecret, _ := mfaEnrolFactor(t, f.client, &raceSessions[0], password)
	if raceSecret == "" {
		t.Fatal("no confirmed factor for the concurrency account")
	}
	raceWrong := nativeMFAHardeningWrongCode(t, raceSecret)
	type raceAttempt struct {
		client    mfaClient
		session   *mfaSession
		challenge string
		revision  int
	}
	attempts := make([]raceAttempt, 0, len(raceSessions))
	for index := range raceSessions {
		client := f.client
		if index%2 == 1 {
			client = f.secondClient
		}
		id, revision := nativeMFAHardeningChallenge(t, client, &raceSessions[index])
		attempts = append(attempts, raceAttempt{
			client: client, session: &raceSessions[index], challenge: id, revision: revision,
		})
	}
	raceCodes := make(chan int, len(attempts))
	var raceWait sync.WaitGroup
	raceStart := make(chan struct{})
	raceWait.Add(len(attempts))
	for _, attempt := range attempts {
		attempt := attempt
		go func() {
			defer raceWait.Done()
			<-raceStart
			status, _, _ := nativeMFAHardeningVerify(t, attempt.client, attempt.session,
				attempt.challenge, attempt.revision, "totp", raceWrong)
			raceCodes <- status
		}()
	}
	close(raceStart)
	raceWait.Wait()
	close(raceCodes)
	for code := range raceCodes {
		if code != http.StatusOK {
			t.Fatalf("concurrent failure status = %d, want 200", code)
		}
	}
	if stored := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT failed_count FROM identity.step_up_failures
		WHERE tenant_id=$1 AND user_id=$2 AND factor='totp'
	`, f.tenantA, concurrentUserID); stored != 5 {
		t.Fatalf("concurrent failed_count = %d, want 5 (no lost update)", stored)
	}
	if status, _, _ := nativeMFAHardeningChallengeBudget(t, f.client, &raceSessions[0]); status != http.StatusTooManyRequests {
		t.Fatalf("sixth concurrent attempt creation = %d, want 429", status)
	}

	// The password factor has its own budget and blocks the success path too.
	passwordLogin := "mfa.hard.pass." + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	passwordUserID := nativeMFAHardeningAccount(t, f.ctx, f.bootstrapPool, f.tenantA, passwordLogin, password)
	passwordSession, _ := mfaLoginWithCookie(t, f.client, passwordLogin, password)
	if _, enrolled := mfaEnrolFactor(t, f.client, &passwordSession, password); enrolled == "" {
		t.Fatal("password budget account enrolment failed")
	}
	// A different operation so the five later creations cannot revoke it.
	preLockedID, preLockedRevision := nativeMFAHardeningChallengeFor(t, f.client, &passwordSession, "mfa_reset")
	for attempt := 0; attempt < 5; attempt++ {
		id, revision := nativeMFAHardeningChallenge(t, f.client, &passwordSession)
		status, _, body := nativeMFAHardeningVerify(t, f.client, &passwordSession, id, revision, "password", "mfa-hardening-wrong-password")
		if status != http.StatusOK {
			t.Fatalf("wrong password %d status = %d; body=%s", attempt+1, status, body)
		}
	}
	if stored := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT failed_count FROM identity.step_up_failures
		WHERE tenant_id=$1 AND user_id=$2 AND factor='password'
	`, f.tenantA, passwordUserID); stored != 5 {
		t.Fatalf("persisted password failed_count = %d, want 5", stored)
	}
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &passwordSession, preLockedID, preLockedRevision, "password", password); status != http.StatusTooManyRequests {
		t.Fatalf("correct password while cooling down = %d, want 429; body=%s", status, body)
	}
}

// nativeMFAHardeningChallengeBudget reports the creation status so the caller can
// assert the bounded refusal.
func nativeMFAHardeningChallengeBudget(t *testing.T, client mfaClient, session *mfaSession) (int, map[string]any, string) {
	t.Helper()
	return client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation": "user_administration",
		"target": map[string]any{
			"kind": "user", "userId": session.userID.String(), "revision": session.accountRevision,
		},
		"expectedSessionVersion": session.sessionVersion,
	}, session)
}
