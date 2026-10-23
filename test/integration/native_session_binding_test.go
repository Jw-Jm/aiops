package integration

// P2-02/R1 session-bound authorization integration test.
//
// The shared authorization layer must prove the exact trusted server session
// inside the caller's write transaction. A request that already passed the
// native middleware may reach its write after the session was revoked, the
// password was changed, the account was disabled or the session expired. This
// test drives the real API duty against a task-owned instance and asserts that
// the write transaction is rejected and leaves no residue.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

type bindingSession struct {
	sessionID           uuid.UUID
	userID              uuid.UUID
	tenantID            uuid.UUID
	credentialVersion   int
	authorizationVer    int
	sessionVersion      int
	authorizationVerRow int
}

func TestNativeSessionBindingRejectsLateWrites(t *testing.T) {
	if os.Getenv(nativeIdentityRunEnv) != "1" {
		t.Skipf("%s=1 is required; a default skip is not a database pass", nativeIdentityRunEnv)
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
	tenantA, _ := nativeIdentityCreateTenants(t, ctx, adminDB)
	bootstrapPool := nativeIdentityNewLoginPool(t, ctx, adminDB, adminDSN, "bootstrap", "migration_role", "")
	defer bootstrapPool.Close()
	apiPool := nativeIdentityNewLoginPool(t, ctx, bootstrapDB, adminDSN, "api", "api_runtime_role", "api_runtime_role")
	defer apiPool.Close()

	accounts, err := identity.NewRepository(bootstrapPool)
	if err != nil {
		t.Fatal(err)
	}
	loginName := "binding.admin." + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := accounts.CreateFirstAdministrator(ctx, identity.FirstAdministratorInput{
		TenantID: tenantA, LoginName: loginName,
		DisplayName: "Native Binding Administrator", Password: "binding-initial-password",
	}); err != nil {
		t.Fatalf("create first administrator: %v", err)
	}
	sessions, err := identity.NewSessionRepository(apiPool)
	if err != nil {
		t.Fatal(err)
	}
	nativeHandlers, err := httpapi.NewNativeAuthHandlers(sessions, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	nativeAuth, err := auth.NewNativeAuthenticator(sessions, tenantA, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := observability.NewRuntime(ctx, "native-session-binding-integration")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(ctx)
	handler, err := httpapi.NewFoundationHandlerWithNativeAuth(apiPool, nativeAuth,
		configregistry.Ed25519TrustStore{}, runtime, nil, nativeHandlers)
	if err != nil {
		t.Fatal(err)
	}

	// A full session: the first administrator rotates its password, then signs
	// in again. The session row is the trusted input of every check below.
	first := bindingLogin(t, handler, loginName, "binding-initial-password")
	change := fmt.Sprintf(`{"currentPassword":"binding-initial-password","newPassword":"binding-rotated-password","expectedCredentialVersion":%d}`, first.credentialVersion)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password:change", strings.NewReader(change))
	request.Host = "ops.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.AddCookie(first.cookie)
	request.Header.Set(auth.NativeCSRFHeader, first.csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("password change status = %d, want 201; body=%s", response.Code, response.Body.String())
	}
	// The second login owns the full business session used by every check.
	bindingLogin(t, handler, loginName, "binding-rotated-password")

	var sessionRow struct {
		SessionID                  uuid.UUID
		UserID                     uuid.UUID
		IssuedCredentialVersion    int
		IssuedAuthorizationVersion int
		IssuedSessionVersion       int
	}
	if err := persistence.WithTenantTx(ctx, bootstrapPool, tenantA, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT session_id, user_id, issued_credential_version,
			       issued_authorization_version, issued_session_version
			FROM identity.sessions
			WHERE tenant_id=$1 AND user_id=(SELECT user_id FROM identity.accounts WHERE login_name=$2)
			ORDER BY issued_at DESC LIMIT 1
		`, tenantA, loginName).Scan(&sessionRow.SessionID, &sessionRow.UserID,
			&sessionRow.IssuedCredentialVersion, &sessionRow.IssuedAuthorizationVersion,
			&sessionRow.IssuedSessionVersion)
	}); err != nil {
		t.Fatalf("read the trusted session row: %v", err)
	}

	// authorizeAndWrite runs one real write transaction with the API duty: it
	// proves the session inside the transaction and only then appends an audit
	// record, which is the observable write residue.
	authorizeAndWrite := func(t *testing.T, label string) (bool, int) {
		t.Helper()
		authorized := false
		residue := 0
		err := persistence.WithTenantTx(ctx, apiPool, tenantA, func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT identity.authorize_native_session_role($1,$2,$3,$4,$5,$6,$7)`,
				tenantA, sessionRow.UserID, sessionRow.SessionID, "platform_admin",
				sessionRow.IssuedAuthorizationVersion, sessionRow.IssuedCredentialVersion,
				sessionRow.IssuedSessionVersion).Scan(&authorized); err != nil {
				return err
			}
			if !authorized {
				return nil
			}
			record := `{"label":"` + label + `"}`
			digest := sha256.Sum256([]byte(record))
			if _, err := tx.Exec(ctx, `
				SELECT audit.append_record_v2($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)
			`, tenantA, uuid.Must(uuid.NewV7()), "identity.binding.probe", "native_account",
				sessionRow.UserID, "integration.binding.probe",
				record, digest[:], time.Now().UTC()); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: write transaction failed: %v", label, err)
		}
		residue = mfaTenantIntWithPool(t, ctx, apiPool, tenantA, `
			SELECT count(*) FROM audit.records
			WHERE tenant_id=$1 AND event_type='identity.binding.probe'
		`, tenantA)
		return authorized, residue
	}

	// 1) The live session is authorized and the write commits.
	authorized, residue := authorizeAndWrite(t, "live")
	if !authorized {
		t.Fatalf("the live session was not authorized")
	}
	if residue != 1 {
		t.Fatalf("the live write left %d rows, want 1", residue)
	}

	// 2) A revoked session is rejected and leaves no new write.
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.sessions SET revoked_at=clock_timestamp(), revoked_reason='integration'
		WHERE tenant_id=$1 AND session_id=$2
	`, tenantA, sessionRow.SessionID)
	authorized, residue = authorizeAndWrite(t, "revoked")
	if authorized {
		t.Fatalf("a revoked session was still authorized")
	}
	if residue != 1 {
		t.Fatalf("the rejected write left %d rows, want the previous 1", residue)
	}
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.sessions SET revoked_at=NULL, revoked_reason=NULL
		WHERE tenant_id=$1 AND session_id=$2
	`, tenantA, sessionRow.SessionID)

	// 3) A changed password rotates the credential version; the old session is
	// no longer authorized.
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.accounts SET credential_version=credential_version+1
		WHERE tenant_id=$1 AND user_id=$2
	`, tenantA, sessionRow.UserID)
	authorized, residue = authorizeAndWrite(t, "credential-rotated")
	if authorized {
		t.Fatalf("a session with a rotated credential version was still authorized")
	}
	if residue != 1 {
		t.Fatalf("the rejected write left %d rows, want the previous 1", residue)
	}
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.accounts SET credential_version=credential_version-1
		WHERE tenant_id=$1 AND user_id=$2
	`, tenantA, sessionRow.UserID)

	// 4) A disabled account is rejected.
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.accounts SET status='disabled'
		WHERE tenant_id=$1 AND user_id=$2
	`, tenantA, sessionRow.UserID)
	authorized, residue = authorizeAndWrite(t, "disabled")
	if authorized {
		t.Fatalf("a disabled account was still authorized")
	}
	if residue != 1 {
		t.Fatalf("the rejected write left %d rows, want the previous 1", residue)
	}
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.accounts SET status='active'
		WHERE tenant_id=$1 AND user_id=$2
	`, tenantA, sessionRow.UserID)

	// 5) An expired session is rejected on both windows.
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.sessions
		SET issued_at=clock_timestamp()-interval '2 hours',
		    last_seen_at=clock_timestamp()-interval '90 minutes',
		    idle_expires_at=clock_timestamp()-interval '1 hour'
		WHERE tenant_id=$1 AND session_id=$2
	`, tenantA, sessionRow.SessionID)
	authorized, residue = authorizeAndWrite(t, "idle-expired")
	if authorized {
		t.Fatalf("an expired session was still authorized")
	}
	if residue != 1 {
		t.Fatalf("the rejected write left %d rows, want the previous 1", residue)
	}
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.sessions
		SET issued_at=clock_timestamp()-interval '2 minutes',
		    last_seen_at=clock_timestamp()-interval '1 minute',
		    idle_expires_at=clock_timestamp()+interval '10 minutes'
		WHERE tenant_id=$1 AND session_id=$2
	`, tenantA, sessionRow.SessionID)

	// 6) A first-password-only account owns no business authority.
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.accounts SET must_change_password=true, password_state='change_required'
		WHERE tenant_id=$1 AND user_id=$2
	`, tenantA, sessionRow.UserID)
	authorized, residue = authorizeAndWrite(t, "first-password-only")
	if authorized {
		t.Fatalf("a first-password-only account was still authorized")
	}
	if residue != 1 {
		t.Fatalf("the rejected write left %d rows, want the previous 1", residue)
	}
	bindingMutate(ctx, bootstrapPool, tenantA, `
		UPDATE identity.accounts SET must_change_password=false, password_state='active'
		WHERE tenant_id=$1 AND user_id=$2
	`, tenantA, sessionRow.UserID)

	// 7) The concurrent case: an authorization that already holds the shared
	// lock linearizes before a concurrent revocation, and the revocation waits
	// for it. The write may commit, but only before the revocation, and the
	// next authorization must fail.
	var wg sync.WaitGroup
	revoked := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		revoked <- bindingMutate(ctx, bootstrapPool, tenantA, `
			UPDATE identity.sessions SET revoked_at=clock_timestamp(), revoked_reason='integration-concurrent'
			WHERE tenant_id=$1 AND session_id=$2
		`, tenantA, sessionRow.SessionID)
	}()
	concurrentAuthorized := false
	if err := persistence.WithTenantTx(ctx, apiPool, tenantA, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT identity.authorize_native_session_role($1,$2,$3,$4,$5,$6,$7)`,
			tenantA, sessionRow.UserID, sessionRow.SessionID, "platform_admin",
			sessionRow.IssuedAuthorizationVersion, sessionRow.IssuedCredentialVersion,
			sessionRow.IssuedSessionVersion).Scan(&concurrentAuthorized); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("concurrent authorization failed: %v", err)
	}
	if !concurrentAuthorized {
		t.Fatalf("the authorization that linearized first was rejected")
	}
	wg.Wait()
	if err := <-revoked; err != nil {
		t.Fatalf("concurrent revocation failed: %v", err)
	}
	authorized, _ = authorizeAndWrite(t, "after-concurrent-revocation")
	if authorized {
		t.Fatalf("a session revoked concurrently was still authorized afterwards")
	}
}

func bindingMutate(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, query string, args ...any) error {
	return persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE migration_role"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, query, args...)
		return err
	})
}

type bindingLoginResult struct {
	cookie            *http.Cookie
	csrf              string
	credentialVersion int
}

func bindingLogin(t *testing.T, handler http.Handler, loginName, password string) bindingLoginResult {
	t.Helper()
	body := fmt.Sprintf(`{"loginName":%q,"password":%q}`, loginName, password)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	request.Host = "ops.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("login %s status = %d, want 201; body=%s", loginName, response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			CSRF struct {
				Token string `json:"token"`
			} `json:"csrf"`
			Revocation struct {
				CredentialVersion int `json:"credentialVersion"`
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
	return bindingLoginResult{
		cookie: sessionCookie, csrf: payload.Data.CSRF.Token,
		credentialVersion: payload.Data.Revocation.CredentialVersion,
	}
}
