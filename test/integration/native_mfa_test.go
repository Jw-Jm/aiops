package integration

// P2-03 native MFA, step-up and recovery integration test.
//
// This test is opt-in and task-owned. It uses the guarded native identity
// environment helpers, applies the current forward-only migrations on a
// task-owned instance, creates a first administrator plus a second native
// account, and exercises the real native HTTP handler chain: enrolment,
// password+TOTP step-up, confirmation, recovery-code consumption and an
// administrator MFA reset. A default skip is not a database pass.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

type mfaSession struct {
	stepUpChallenge string
	cookie          *http.Cookie
	csrf            string
	sessionRef      string
	subject         string
	userID          uuid.UUID
	accountRevision int
	credential      int
	sessionVersion  int
	authorizationV  int
}

type mfaClient struct {
	handler  http.Handler
	sessions *identity.SessionRepository
}

func (c mfaClient) do(t *testing.T, method, path string, body any, session *mfaSession) (int, map[string]any, string) {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(encoded))
	} else {
		reader = strings.NewReader("")
	}
	request := httptest.NewRequest(method, path, reader)
	request.Host = "ops.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	if session != nil {
		if session.stepUpChallenge != "" {
			request.Header.Set("X-Ops-Step-Up-Challenge", session.stepUpChallenge)
		}

		request.AddCookie(session.cookie)
		request.Header.Set(auth.NativeCSRFHeader, session.csrf)
	}
	response := httptest.NewRecorder()
	c.handler.ServeHTTP(response, request)
	decoded := map[string]any{}
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s returned a non-JSON body: %s", method, path, response.Body.String())
		}
	}
	return response.Code, decoded, response.Body.String()
}

func mfaData(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("response has no data object: %v", payload)
	}
	return data
}

func mfaLoginWithCookie(t *testing.T, client mfaClient, loginName, password string) (mfaSession, *http.Cookie) {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"loginName": loginName, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(encoded)))
	request.Host = "ops.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	client.handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("login %s status = %d, want 201; body=%s", loginName, response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			SessionRef      string `json:"sessionRef"`
			Subject         string `json:"subject"`
			AccountRevision int    `json:"accountRevision"`
			CSRF            struct {
				Token string `json:"token"`
			} `json:"csrf"`
			Revocation struct {
				CredentialVersion    int `json:"credentialVersion"`
				SessionVersion       int `json:"sessionVersion"`
				AuthorizationVersion int `json:"authorizationVersion"`
			} `json:"revocation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == auth.NativeSessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatalf("login did not issue a session cookie")
	}
	active, err := client.sessions.FindActiveSession(context.Background(), identity.DigestToken(sessionCookie.Value))
	if err != nil {
		t.Fatal("read actual issued session:", err)
	}
	if active.Subject != payload.Data.Subject {
		t.Fatal("issued subject differs from the persisted session")
	}
	session := mfaSession{
		cookie:          sessionCookie,
		csrf:            payload.Data.CSRF.Token,
		sessionRef:      payload.Data.SessionRef,
		subject:         payload.Data.Subject,
		userID:          active.UserID,
		accountRevision: payload.Data.AccountRevision,
		credential:      payload.Data.Revocation.CredentialVersion,
		sessionVersion:  payload.Data.Revocation.SessionVersion,
		authorizationV:  payload.Data.Revocation.AuthorizationVersion,
	}
	return session, sessionCookie
}

func mfaSecretFromURI(t *testing.T, uri string) string {
	t.Helper()
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("otpauth uri is not a URL: %v", err)
	}
	if !strings.HasPrefix(parsed.Scheme, "otpauth") {
		t.Fatalf("otpauth uri scheme = %q", parsed.Scheme)
	}
	secret := parsed.Query().Get("secret")
	if len(secret) < 16 {
		t.Fatalf("otpauth uri has no usable secret")
	}
	return secret
}

func mfaCurrentCode(t *testing.T, secret string) string {
	t.Helper()
	return mfaCodeAt(t, secret, identity.TOTPStep(time.Now().UTC()))
}

// mfaCodeAt returns the code of one time step. The server accepts the current
// step and one neighbour in each direction, so a fixture can pick the next step
// when the previous one was already consumed by an anti-replay fence.
func mfaCodeAt(t *testing.T, secret string, step int64) string {
	t.Helper()
	code, err := identity.TOTPCodeAt(secret, step)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestNativeMFAEnrollmentStepUpAndReset(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	adminDB, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	if _, err := nativeIdentityProbeDatabase(ctx, adminDB); err != nil {
		t.Fatalf("task-owned database guard failed before mutation: %v", err)
	}
	bootstrapDB, err := sql.Open("pgx", bootstrapDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrapDB.Close()
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
	defer migrator.Close()
	if err := goose.UpContext(ctx, migrator, migrationDir); err != nil {
		t.Fatalf("apply native identity migrations: %v", err)
	}
	var schemaVersion int
	if err := adminDB.QueryRowContext(ctx, `SELECT max(version_id) FROM public.goose_db_version WHERE is_applied`).Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	t.Logf("schema version after the forward chain = %d", schemaVersion)
	if schemaVersion < 51 {
		t.Fatalf("schema version = %d, want at least 51", schemaVersion)
	}

	tenantA, _ := nativeIdentityCreateTenants(t, ctx, adminDB)
	bootstrapPool := nativeIdentityNewLoginPool(t, ctx, adminDB, adminDSN, "bootstrap", "migration_role", "")
	defer bootstrapPool.Close()
	apiPool := nativeIdentityNewLoginPool(t, ctx, bootstrapDB, adminDSN, "api", "api_runtime_role", "api_runtime_role")
	defer apiPool.Close()
	workerPool := nativeIdentityNewLoginPool(t, ctx, bootstrapDB, adminDSN, "worker", "worker_runtime_role", "worker_runtime_role")
	defer workerPool.Close()

	accounts, err := identity.NewRepository(bootstrapPool)
	if err != nil {
		t.Fatal(err)
	}
	adminLogin := "mfa.admin." + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	_, err = accounts.CreateFirstAdministrator(ctx, identity.FirstAdministratorInput{
		TenantID: tenantA, LoginName: adminLogin,
		DisplayName: "Native MFA Administrator", Password: "mfa-initial-password",
	})
	if err != nil {
		t.Fatalf("create first administrator: %v", err)
	}
	// A second native account in the same tenant is the administrator reset
	// target. It is created by the administrator path that P2-04 will expose;
	// here the fixture inserts it with the bootstrap duty.
	targetLogin := "mfa.target." + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	targetUserID := uuid.Must(uuid.NewV7())
	targetPassword := "mfa-target-password"
	targetHash, err := identity.HashPassword(targetPassword)
	if err != nil {
		t.Fatal(err)
	}
	err = persistence.WithTenantTx(ctx, bootstrapPool, tenantA, func(tx pgx.Tx) error {
		// The fixture uses the controlled migration/bootstrap duty, exactly like
		// the first-administrator bootstrap does.
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.accounts
				(tenant_id, user_id, login_name, display_name, subject, status,
				 password_state, must_change_password, account_revision,
				 credential_version, authorization_version, session_version)
			VALUES ($1, $2, $3, 'MFA Reset Target', $4, 'active', 'active', false, 1, 1, 1, 1)
		`, tenantA, targetUserID, targetLogin, "user:"+targetUserID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO identity.credentials
				(tenant_id, user_id, credential_version, password_hash, status)
			VALUES ($1, $2, 1, $3, 'active')
		`, tenantA, targetUserID, targetHash)
		return err
	})
	if err != nil {
		t.Fatalf("create the reset target account: %v", err)
	}

	sessions, err := identity.NewSessionRepository(apiPool)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "mfa-secret.key")
	if err := os.WriteFile(keyFile, []byte(mfaTestKeyBase64), 0o600); err != nil {
		t.Fatal(err)
	}
	sealer, err := identity.NewFileSecretSealer(keyFile)
	if err != nil {
		t.Fatalf("create the task key backend: %v", err)
	}
	mfaRepository, err := identity.NewMFARepository(sessions, sealer)
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
	runtime, err := observability.NewRuntime(ctx, "native-mfa-integration")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(ctx)
	handler, err := httpapi.NewFoundationHandlerWithNativeMFA(apiPool, nativeAuth,
		configregistry.Ed25519TrustStore{}, runtime, nil, nativeHandlers, nativeMFA)
	if err != nil {
		t.Fatal(err)
	}
	client := mfaClient{handler: handler, sessions: sessions}

	// The first administrator must rotate its password before it owns a
	// business session.
	first, _ := mfaLoginWithCookie(t, client, adminLogin, "mfa-initial-password")
	code, _, body := client.do(t, http.MethodPost, "/api/v1/auth/password:change", map[string]any{
		"currentPassword": "mfa-initial-password", "newPassword": "mfa-rotated-password",
		"expectedCredentialVersion": first.credential,
	}, &first)
	if code != http.StatusCreated {
		t.Fatalf("password change status = %d, want 201; body=%s", code, body)
	}
	admin, _ := mfaLoginWithCookie(t, client, adminLogin, "mfa-rotated-password")
	if admin.accountRevision < 1 {
		t.Fatalf("session view has no account revision: %+v", admin)
	}

	// 1) Enrolment returns the provisioning URI exactly once and never again.
	code, payload, body := client.do(t, http.MethodPost, "/api/v1/auth/mfa/enrollments", map[string]any{
		"method": "totp", "expectedCredentialVersion": admin.credential,
	}, &admin)
	if code != http.StatusCreated {
		t.Fatalf("enrolment status = %d, want 201; body=%s", code, body)
	}
	enrollmentData := mfaData(t, payload)
	enrollmentID, _ := enrollmentData["enrollmentId"].(string)
	otpauthURI, _ := enrollmentData["otpauthUri"].(string)
	if enrollmentID == "" || otpauthURI == "" {
		t.Fatalf("enrolment response is incomplete: %v", enrollmentData)
	}
	secret := mfaSecretFromURI(t, otpauthURI)

	code, payload, body = client.do(t, http.MethodGet, "/api/v1/auth/mfa/enrollments/"+enrollmentID, nil, &admin)
	if code != http.StatusOK {
		t.Fatalf("enrolment view status = %d, want 200; body=%s", code, body)
	}
	view := mfaData(t, payload)
	if view["state"] != "pending_confirmation" {
		t.Fatalf("enrolment state = %v, want pending_confirmation", view["state"])
	}
	if strings.Contains(body, "otpauth") || strings.Contains(body, "secret") {
		t.Fatalf("enrolment view leaked the provisioning secret: %s", body)
	}

	// 2) A challenge alone never grants anything.
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation":              "mfa_enrollment",
		"target":                 map[string]any{"kind": "mfa_enrollment", "enrollmentId": enrollmentID, "revision": 1},
		"expectedSessionVersion": admin.sessionVersion,
	}, &admin)
	if code != http.StatusCreated {
		t.Fatalf("challenge creation status = %d, want 201; body=%s", code, body)
	}
	challenge := mfaData(t, payload)
	challengeID, _ := challenge["challengeId"].(string)
	challengeRevision := int(challenge["revision"].(float64))

	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{
		"method": "password", "credential": "mfa-rotated-password",
		"expectedChallengeRevision": challengeRevision,
	}, &admin)
	if code != http.StatusOK {
		t.Fatalf("password verification status = %d, want 200; body=%s", code, body)
	}
	result := mfaData(t, payload)
	if result["verified"] != false || result["acr"] != "urn:ops:loa:1" {
		t.Fatalf("a single factor must stay LoA1 and unverified: %v", result)
	}
	if result["stepUpSession"] != nil {
		t.Fatalf("a single factor minted a step-up session: %v", result)
	}
	challengeRevision++

	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{
		"method": "totp", "credential": mfaCurrentCode(t, secret),
		"expectedChallengeRevision": challengeRevision,
	}, &admin)
	if code != http.StatusOK {
		t.Fatalf("totp verification status = %d, want 200; body=%s", code, body)
	}
	result = mfaData(t, payload)
	if result["verified"] != true || result["acr"] != "urn:ops:loa:2" {
		t.Fatalf("password+TOTP must reach LoA2: %v", result)
	}
	stepUp, ok := result["stepUpSession"].(map[string]any)
	if !ok || stepUp["sessionRef"] == nil {
		t.Fatalf("verified challenge did not mint a step-up session: %v", result)
	}

	// 3) The confirmation requires the bound step-up and issues the codes once.
	confirmedCode := mfaCurrentCode(t, secret)
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/mfa/enrollments/"+enrollmentID+":verify", map[string]any{
		"totpCode": confirmedCode, "expectedEnrollmentRevision": 1,
		"stepUp": map[string]any{"challengeId": challengeID, "sessionRef": admin.sessionRef},
	}, &admin)
	if code != http.StatusOK {
		t.Fatalf("enrolment confirmation status = %d, want 200; body=%s", code, body)
	}
	confirmed := mfaData(t, payload)
	if confirmed["state"] != "confirmed" {
		t.Fatalf("enrolment state after confirmation = %v", confirmed["state"])
	}
	codes, _ := confirmed["recoveryCodes"].(map[string]any)["codes"].([]any)
	if len(codes) != 10 {
		t.Fatalf("recovery code count = %d, want 10", len(codes))
	}
	recoveryCode, _ := codes[0].(string)

	// 4) The status view reports the confirmed factor and the code count.
	code, payload, body = client.do(t, http.MethodGet, "/api/v1/auth/mfa/status", nil, &admin)
	if code != http.StatusOK {
		t.Fatalf("mfa status status = %d, want 200; body=%s", code, body)
	}
	status := mfaData(t, payload)
	if status["state"] != "confirmed" || status["recoveryCodesRemaining"].(float64) != 10 {
		t.Fatalf("mfa status = %v", status)
	}

	// 5) The session projection reports LoA2 while the step-up proof is alive.
	code, payload, body = client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &admin)
	if code != http.StatusOK {
		t.Fatalf("session status = %d, want 200; body=%s", code, body)
	}
	view = mfaData(t, payload)
	if view["acr"] != "urn:ops:loa:2" {
		t.Fatalf("session acr = %v, want LoA2 after a verified step-up", view["acr"])
	}

	// A genuine minted proof must fail in a late transaction after logout.
	oldAdmin := admin
	oldSessionID := uuid.MustParse(strings.TrimPrefix(admin.sessionRef, "session:"))
	oldProofRef := uuid.MustParse(strings.TrimPrefix(stepUp["sessionRef"].(string), "stepup:"))
	targetJSON, _ := json.Marshal(map[string]any{"kind": "mfa_enrollment", "enrollmentId": enrollmentID, "revision": 1})
	oldDigest := identity.StepUpBindingDigest(admin.sessionRef, "mfa_enrollment", targetJSON, admin.sessionVersion, admin.credential, admin.authorizationV)
	if status, _, _ := client.do(t, http.MethodPost, "/api/v1/auth/logout", map[string]any{"expectedSessionVersion": admin.sessionVersion, "reason": "test late mfa proof"}, &admin); status != 200 {
		t.Fatalf("logout=%d", status)
	}
	var accepted bool
	err = persistence.WithTenantTx(ctx, apiPool, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT identity.step_up_recheck($1,$2,$3,$4,$5,$6,$7,$8,$9,600)`, tenantA, oldAdmin.userID, oldSessionID, oldProofRef, "mfa_enrollment", oldDigest, oldAdmin.authorizationV, oldAdmin.credential, oldAdmin.sessionVersion).Scan(&accepted)
	})
	if err != nil || accepted {
		t.Fatalf("revoked parent session recheck accepted=%v err=%v", accepted, err)
	}
	admin, _ = mfaLoginWithCookie(t, client, adminLogin, "mfa-rotated-password")

	// Replay the exact consumed code, even when a slow environment crosses a time window.
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation":              "user_administration",
		"target":                 map[string]any{"kind": "user", "userId": admin.userID.String(), "revision": admin.accountRevision},
		"expectedSessionVersion": admin.sessionVersion,
	}, &admin)
	if code != http.StatusCreated {
		t.Fatalf("second challenge status = %d, want 201; body=%s", code, body)
	}
	second := mfaData(t, payload)
	secondID, _ := second["challengeId"].(string)
	secondRevision := int(second["revision"].(float64))
	replayed := confirmedCode
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+secondID+":verify", map[string]any{
		"method": "password", "credential": "mfa-rotated-password", "expectedChallengeRevision": secondRevision,
	}, &admin)
	if code != http.StatusOK {
		t.Fatalf("second password verification status = %d, want 200; body=%s", code, body)
	}
	secondRevision++
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+secondID+":verify", map[string]any{
		"method": "totp", "credential": replayed, "expectedChallengeRevision": secondRevision,
	}, &admin)
	if code != http.StatusOK {
		t.Fatalf("replayed totp status = %d, want 200; body=%s", code, body)
	}
	result = mfaData(t, payload)
	if result["verified"] != false {
		t.Fatalf("a replayed TOTP code was accepted: %v", result)
	}

	// 7) Recovery consumes one code, stays LoA1 and revokes the sessions.
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation":              "recovery_code_regenerate",
		"target":                 map[string]any{"kind": "user", "userId": admin.userID.String(), "revision": admin.accountRevision},
		"expectedSessionVersion": admin.sessionVersion,
	}, &admin)
	if code != http.StatusCreated {
		t.Fatalf("recovery challenge status = %d, want 201; body=%s", code, body)
	}
	recoveryChallenge := mfaData(t, payload)
	recoveryID, _ := recoveryChallenge["challengeId"].(string)
	recoveryRevision := int(recoveryChallenge["revision"].(float64))
	for attempt := 0; attempt < 2; attempt++ {
		status, failed, _ := client.do(t, http.MethodPost, "/api/v1/auth/mfa/recovery-codes:verify", map[string]any{"challengeId": recoveryID, "recoveryCode": "AAAA-AAAA", "expectedChallengeRevision": recoveryRevision}, &admin)
		if status != http.StatusOK || mfaData(t, failed)["verified"] != false {
			t.Fatal("wrong recovery code did not return a failed verification")
		}
		recoveryRevision = int(mfaData(t, failed)["challengeRevision"].(float64))
	}
	if failures := mfaTenantInt(t, ctx, bootstrapPool, tenantA, `SELECT recovery_failures FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, tenantA, admin.userID); failures != 2 {
		t.Fatalf("persisted recovery failures=%d, want2", failures)
	}
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/mfa/recovery-codes:verify", map[string]any{
		"challengeId": recoveryID, "recoveryCode": recoveryCode, "expectedChallengeRevision": recoveryRevision,
	}, &admin)
	if code != http.StatusOK {
		t.Fatalf("recovery status = %d, want 200; body=%s", code, body)
	}
	recovery := mfaData(t, payload)
	if recovery["verified"] != true {
		t.Fatalf("recovery code was not accepted: %v", recovery)
	}
	if recovery["acr"] != "urn:ops:loa:1" {
		t.Fatalf("recovery must stay LoA1: %v", recovery["acr"])
	}
	if recovery["mustReenrollMfa"] != true {
		t.Fatalf("recovery must force re-enrolment: %v", recovery)
	}
	if recovery["sessionsRevoked"].(float64) < 1 {
		t.Fatalf("recovery revoked no session: %v", recovery)
	}
	// The revoked session may not keep using the API.
	code, _, _ = client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &admin)
	if code != http.StatusUnauthorized {
		t.Fatalf("session after recovery status = %d, want 401", code)
	}

	// 8) The administrator resets the target account with a target-bound
	// step-up, a version bump, revoked sessions and one audit record.
	admin, _ = mfaLoginWithCookie(t, client, adminLogin, "mfa-rotated-password")
	code, payload, _ = client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &admin)
	if code != 200 || mfaData(t, payload)["mustReenrollMfa"] != true {
		t.Fatal("recovery setup requirement did not survive a new login")
	}
	if grants := mfaData(t, payload)["authorization"].(map[string]any)["grants"].([]any); len(grants) != 0 {
		t.Fatal("recovery-only session still has business grants")
	}
	if status, _, _ := client.do(t, http.MethodGet, "/api/v1/admin/clusters", nil, &admin); status != 403 {
		t.Fatalf("recovery-only business status=%d", status)
	}
	adminSecret, _ := mfaEnrolFactor(t, client, &admin, "mfa-rotated-password")
	// A real source mutation is authorized only for the exact canonical body,
	// path and operation verified by password+TOTP in this session.
	sourceID := uuid.Must(uuid.NewV7())
	err = persistence.WithTenantTx(ctx, apiPool, tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,source_type,instance_key,auth_ref) VALUES($1,$2,'victorialogs',$3,'openbao://isolated/mfa-contract')`, tenantA, sourceID, "mfa-contract-"+sourceID.String())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := "/api/v1/admin/source-registrations/" + sourceID.String()
	writeBody := map[string]any{"expectedRevision": 1, "status": "disabled"}
	writeJSON, _ := json.Marshal(writeBody)
	writeTarget := map[string]any{"kind": "source_registration", "registrationId": sourceID.String(), "revision": 1, "write": map[string]any{"method": "PATCH", "path": sourcePath, "bodyDigest": "sha256:" + identity.DigestHex(identity.DigestSHA256(writeJSON))}}
	if status, _, _ := client.do(t, http.MethodPatch, sourcePath, writeBody, &admin); status != 403 {
		t.Fatalf("write without proof=%d", status)
	}
	configStatus, configChallenge, _ := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{"operation": "source_administration", "target": writeTarget, "expectedSessionVersion": admin.sessionVersion}, &admin)
	if configStatus != 201 {
		t.Fatalf("source challenge=%d", configStatus)
	}
	configID := mfaData(t, configChallenge)["challengeId"].(string)
	for index, method := range []string{"password", "totp"} {
		credential := "mfa-rotated-password"
		if method == "totp" {
			credential = mfaCurrentCode(t, adminSecret)
		}
		status, verified, _ := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+configID+":verify", map[string]any{"method": method, "credential": credential, "expectedChallengeRevision": index + 1}, &admin)
		if status != 200 {
			t.Fatalf("source proof %s=%d", method, status)
		}
		if method == "totp" && mfaData(t, verified)["verified"] != true {
			t.Fatal("source proof did not verify")
		}
	}
	admin.stepUpChallenge = configID
	if status, _, _ := client.do(t, http.MethodPatch, sourcePath, map[string]any{"expectedRevision": 1, "status": "active"}, &admin); status != 403 {
		t.Fatalf("changed body borrowed proof=%d", status)
	}
	if status, _, body := client.do(t, http.MethodPatch, sourcePath, writeBody, &admin); status != 200 {
		t.Fatalf("exact source write=%d %s", status, body)
	}
	if revision := mfaTenantIntWithPool(t, ctx, apiPool, tenantA, `SELECT revision FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2`, tenantA, sourceID); revision != 2 {
		t.Fatalf("source write not persisted: revision%d", revision)
	}
	admin.stepUpChallenge = ""
	mfaAdministrativeDraftProof(t, client, &admin, adminSecret)
	assertNativeUserAdministration(t, ctx, bootstrapPool, apiPool, client, tenantA, &admin, adminSecret)
	// The target account owns a session and a confirmed factor before the
	// reset, so the reset has something real to revoke and remove.
	target, _ := mfaLoginWithCookie(t, client, targetLogin, targetPassword)
	targetSecret, targetEnrollment := mfaEnrolFactor(t, client, &target, targetPassword)
	if targetEnrollment == "" {
		t.Fatalf("the reset target could not enrol a factor")
	}
	// Replacement keeps the confirmed old factor until a proof from that
	// factor and a code from the new factor commit together.
	statusCode, pending, _ := client.do(t, http.MethodPost, "/api/v1/auth/mfa/enrollments", map[string]any{"method": "totp", "expectedCredentialVersion": target.credential}, &target)
	if statusCode != 201 {
		t.Fatal("replacement enrollment was not created")
	}
	pendingData := mfaData(t, pending)
	replacementID := pendingData["enrollmentId"].(string)
	replacementSecret := mfaSecretFromURI(t, pendingData["otpauthUri"].(string))
	if state := mfaTenantString(t, ctx, apiPool, tenantA, `SELECT out_state FROM identity.mfa_state($1,$2)`, tenantA, target.userID); state != "confirmed" {
		t.Fatal("replacement prematurely removed confirmed factor")
	}
	statusCode, replacementChallenge, _ := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{"operation": "mfa_enrollment", "target": map[string]any{"kind": "mfa_enrollment", "enrollmentId": replacementID, "revision": 1}, "expectedSessionVersion": target.sessionVersion}, &target)
	if statusCode != 201 {
		t.Fatal("replacement challenge creation failed")
	}
	replacementChallengeID := mfaData(t, replacementChallenge)["challengeId"].(string)
	verifyReplacement := func(method, credential string, revision int) map[string]any {
		status, result, _ := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+replacementChallengeID+":verify", map[string]any{"method": method, "credential": credential, "expectedChallengeRevision": revision}, &target)
		if status != 200 {
			t.Fatalf("replacement %s status=%d", method, status)
		}
		return mfaData(t, result)
	}
	verifyReplacement("password", targetPassword, 1)
	if verifyReplacement("totp", mfaCurrentCode(t, replacementSecret), 2)["verified"] != false {
		t.Fatal("new factor bypassed existing MFA")
	}
	if verifyReplacement("totp", mfaCodeAt(t, targetSecret, identity.TOTPStep(time.Now().UTC())+1), 3)["verified"] != true {
		t.Fatal("old factor could not authorize replacement")
	}
	if status, _, _ := client.do(t, http.MethodPost, "/api/v1/auth/mfa/enrollments/"+replacementID+":verify", map[string]any{"totpCode": mfaCurrentCode(t, replacementSecret), "expectedEnrollmentRevision": 1, "stepUp": map[string]any{"challengeId": replacementChallengeID, "sessionRef": target.sessionRef}}, &target); status != 200 {
		t.Fatalf("replacement confirmation=%d", status)
	}
	for attempt := 0; attempt < 5; attempt++ {
		status, created, _ := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{"operation": "recovery_code_regenerate", "target": map[string]any{"kind": "user", "userId": target.userID.String(), "revision": target.accountRevision}, "expectedSessionVersion": target.sessionVersion}, &target)
		if status != 201 {
			t.Fatalf("budget challenge status=%d", status)
		}
		data := mfaData(t, created)
		status, failed, _ := client.do(t, http.MethodPost, "/api/v1/auth/mfa/recovery-codes:verify", map[string]any{"challengeId": data["challengeId"], "recoveryCode": "AAAA-AAAA", "expectedChallengeRevision": data["revision"]}, &target)
		if status != 200 || mfaData(t, failed)["verified"] != false {
			t.Fatal("bad recovery should consume a failure")
		}
	}
	if failures := mfaTenantInt(t, ctx, bootstrapPool, tenantA, `SELECT recovery_failures FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, tenantA, target.userID); failures != 5 {
		t.Fatalf("new challenges bypassed recovery budget: %d", failures)
	}
	statusCode, newChallenge, _ := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{"operation": "recovery_code_regenerate", "target": map[string]any{"kind": "user", "userId": target.userID.String(), "revision": target.accountRevision}, "expectedSessionVersion": target.sessionVersion}, &target)
	if statusCode != 201 {
		t.Fatal("create budget boundary challenge")
	}
	boundary := mfaData(t, newChallenge)
	if status, _, _ := client.do(t, http.MethodPost, "/api/v1/auth/mfa/recovery-codes:verify", map[string]any{"challengeId": boundary["challengeId"], "recoveryCode": "AAAA-AAAA", "expectedChallengeRevision": boundary["revision"]}, &target); status != 409 {
		t.Fatalf("exhausted budget returned%d, want409", status)
	}
	assertNativeGraphScopes(t, ctx, bootstrapPool, apiPool, workerPool, sessions, client, tenantA, &target, targetLogin, targetPassword, replacementSecret, admin)
	var targetAccountRevision, targetCredentialVersion int
	if err := persistence.WithTenantTx(ctx, bootstrapPool, tenantA, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT account_revision, credential_version FROM identity.accounts
			WHERE tenant_id=$1 AND user_id=$2
		`, tenantA, targetUserID).Scan(&targetAccountRevision, &targetCredentialVersion)
	}); err != nil {
		t.Fatal(err)
	}
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation": "mfa_reset",
		"target": map[string]any{
			"kind": "user", "userId": targetUserID.String(), "revision": targetAccountRevision,
		},
		"expectedSessionVersion": admin.sessionVersion,
	}, &admin)
	if code != http.StatusCreated {
		t.Fatalf("reset challenge status = %d, want 201; body=%s", code, body)
	}
	resetChallenge := mfaData(t, payload)
	resetID, _ := resetChallenge["challengeId"].(string)
	resetRevision := int(resetChallenge["revision"].(float64))
	code, _, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+resetID+":verify", map[string]any{
		"method": "password", "credential": "mfa-rotated-password", "expectedChallengeRevision": resetRevision,
	}, &admin)
	if code != http.StatusOK {
		t.Fatalf("reset password verification status = %d, want 200; body=%s", code, body)
	}
	resetRevision++
	// The confirmation already consumed the current time step, so the reset
	// challenge uses the next one: a replayed code must never open a second
	// high-risk window.
	nextStepCode := nextNativeAdminTOTP(t, ctx, apiPool, tenantA, &admin, adminSecret)
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+resetID+":verify", map[string]any{
		"method": "totp", "credential": nextStepCode,
		"expectedChallengeRevision": resetRevision,
	}, &admin)
	if code != http.StatusOK {
		t.Fatalf("reset totp verification status = %d, want 200; body=%s", code, body)
	}
	if mfaData(t, payload)["verified"] != true {
		t.Fatalf("the reset step-up was not verified: %s", body)
	}
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/admin/users/"+targetUserID.String()+"/mfa:reset", map[string]any{
		"userId": targetUserID.String(), "confirmLoginName": targetLogin,
		"reason":                    "integration reset of a lost second factor",
		"expectedUserRevision":      targetAccountRevision,
		"expectedCredentialVersion": targetCredentialVersion,
		"stepUp": map[string]any{
			"challengeId": resetID, "sessionRef": admin.sessionRef,
		},
	}, &admin)
	if code != http.StatusOK {
		t.Fatalf("administrator reset status = %d, want 200; body=%s", code, body)
	}
	reset := mfaData(t, payload)
	if reset["mfaState"] != "reset_required" {
		t.Fatalf("reset state = %v", reset["mfaState"])
	}
	if reset["sessionsRevoked"].(float64) < 1 {
		t.Fatalf("reset revoked no session: %v", reset)
	}
	// The revoked target session may not keep using the API.
	code, _, _ = client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &target)
	if code != http.StatusUnauthorized {
		t.Fatalf("target session after the administrator reset status = %d, want 401", code)
	}
	recheck, _ := reset["writeRecheck"].(map[string]any)
	if recheck == nil || recheck["result"] != "passed" {
		t.Fatalf("reset did not record a passing write recheck: %v", reset)
	}
	newCredentialVersion := mfaTenantInt(t, ctx, bootstrapPool, tenantA, `
		SELECT credential_version FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2
	`, tenantA, targetUserID)
	if newCredentialVersion != targetCredentialVersion+1 {
		t.Fatalf("target credential version = %d, want %d", newCredentialVersion, targetCredentialVersion+1)
	}
	resetAudit := mfaTenantIntWithPool(t, ctx, apiPool, tenantA, `
		SELECT count(*) FROM audit.records
		WHERE tenant_id=$1 AND event_type='identity.mfa.reset_by_administrator' AND entity_id=$2
	`, tenantA, targetUserID.String())
	if resetAudit != 1 {
		t.Fatalf("administrator reset audit count = %d, want 1", resetAudit)
	}
	// The target account keeps its password but loses every second factor.
	// The target keeps its password but loses every second factor. The state is
	// read through the granted function, because the API duty has no direct
	// table grant on the MFA tables.
	targetState := mfaTenantString(t, ctx, apiPool, tenantA, `
		SELECT out_state FROM identity.mfa_state($1, $2)
	`, tenantA, targetUserID)
	if targetState != "reset_required" {
		t.Fatalf("the reset target kept a second factor: state=%q", targetState)
	}
}

// mfaTenantInt runs one read inside a tenant-scoped transaction with the
// controlled migration duty, because the identity tables keep FORCE RLS.
func mfaTenantInt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, query string, args ...any) int {
	t.Helper()
	var value int
	err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		return tx.QueryRow(ctx, query, args...).Scan(&value)
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// mfaTenantIntWithPool runs one read inside a tenant-scoped transaction with
// the API runtime duty, for tables the API role may read but the migration duty
// may not.
func mfaTenantIntWithPool(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, query string, args ...any) int {
	t.Helper()
	var value int
	err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, query, args...).Scan(&value)
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// mfaTenantString runs one read inside a tenant-scoped transaction with the API
// runtime duty and returns a text column.
func mfaTenantString(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, query string, args ...any) string {
	t.Helper()
	var value string
	err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, query, args...).Scan(&value)
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// mfaEnrolFactor runs the whole enrolment flow for the signed-in account and
// returns the TOTP secret plus the confirmed enrolment id.
func mfaEnrolFactor(t *testing.T, client mfaClient, session *mfaSession, password string) (string, string) {
	t.Helper()
	code, payload, body := client.do(t, http.MethodPost, "/api/v1/auth/mfa/enrollments", map[string]any{
		"method": "totp", "expectedCredentialVersion": session.credential,
	}, session)
	if code != http.StatusCreated {
		t.Fatalf("enrolment status = %d, want 201; body=%s", code, body)
	}
	enrollment := mfaData(t, payload)
	enrollmentID, _ := enrollment["enrollmentId"].(string)
	uri, _ := enrollment["otpauthUri"].(string)
	secret := mfaSecretFromURI(t, uri)
	code, payload, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation":              "mfa_enrollment",
		"target":                 map[string]any{"kind": "mfa_enrollment", "enrollmentId": enrollmentID, "revision": 1},
		"expectedSessionVersion": session.sessionVersion,
	}, session)
	if code != http.StatusCreated {
		t.Fatalf("challenge status = %d, want 201; body=%s", code, body)
	}
	challenge := mfaData(t, payload)
	challengeID, _ := challenge["challengeId"].(string)
	revision := int(challenge["revision"].(float64))
	code, _, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{
		"method": "password", "credential": password, "expectedChallengeRevision": revision,
	}, session)
	if code != http.StatusOK {
		t.Fatalf("password verification status = %d, want 200; body=%s", code, body)
	}
	revision++
	code, _, body = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{
		"method": "totp", "credential": mfaCodeAt(t, secret, identity.TOTPStep(time.Now().UTC())-1), "expectedChallengeRevision": revision,
	}, session)
	if code != http.StatusOK {
		t.Fatalf("totp verification status = %d, want 200; body=%s", code, body)
	}
	code, _, body = client.do(t, http.MethodPost, "/api/v1/auth/mfa/enrollments/"+enrollmentID+":verify", map[string]any{
		"totpCode": mfaCodeAt(t, secret, identity.TOTPStep(time.Now().UTC())-1), "expectedEnrollmentRevision": 1,
		"stepUp": map[string]any{"challengeId": challengeID, "sessionRef": session.sessionRef},
	}, session)
	if code != http.StatusOK {
		t.Fatalf("enrolment confirmation status = %d, want 200; body=%s", code, body)
	}
	return secret, enrollmentID
}

// mfaTestKeyBase64 is a task-scoped test key for the isolated instance. It is
// only a fixture: production keys come from the secret backend.
const mfaTestKeyBase64 = "bWZhLXRlc3Qta2V5LTMyLWJ5dGVzLTAxMjM0NTY3ODk="
