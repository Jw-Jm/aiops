package integration

// P2-02 native HTTP session integration test.
//
// This test is opt-in and task-owned. It uses the guarded native identity
// environment helpers, applies the current forward-only migrations, creates a
// first administrator, and exercises the real native HTTP handler chain:
// login, session projection, first password rotation, stale cookie rejection,
// logout and repeated logout. A default skip is not a database pass.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/identity"
	"ops-platform/internal/observability"
)

func TestNativeHTTPSessionFlow(t *testing.T) {
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
	bootstrapDSN, err := nativeIdentityValidateBootstrapDSN(
		os.Getenv(nativeIdentityBootstrapEnv), port,
	)
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

	tenantA, tenantB := nativeIdentityCreateTenants(t, ctx, adminDB)
	bootstrapPool := nativeIdentityNewLoginPool(t, ctx, adminDB, adminDSN,
		"bootstrap", "migration_role", "")
	defer bootstrapPool.Close()
	apiPool := nativeIdentityNewLoginPool(t, ctx, bootstrapDB, adminDSN,
		"api", "api_runtime_role", "api_runtime_role")
	defer apiPool.Close()

	accounts, err := identity.NewRepository(bootstrapPool)
	if err != nil {
		t.Fatal(err)
	}
	loginName := "http.admin." + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	firstAdmin, err := accounts.CreateFirstAdministrator(ctx, identity.FirstAdministratorInput{
		TenantID: tenantA, LoginName: loginName,
		DisplayName: "Native HTTP Administrator", Password: "native-http-password",
	})
	if err != nil {
		t.Fatalf("create first administrator: %v", err)
	}
	// Use a persisted principal different from the bootstrap prefix to catch fabricated audit subjects.
	firstAdmin.Subject = "native:" + firstAdmin.UserID.String()
	if _, err := bootstrapDB.ExecContext(ctx, `UPDATE identity.accounts SET subject=$3 WHERE tenant_id=$1 AND user_id=$2`, tenantA, firstAdmin.UserID, firstAdmin.Subject); err != nil {
		t.Fatal(err)
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
	runtime, err := observability.NewRuntime(ctx, "native-foundation-integration")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(ctx)
	handler, err := httpapi.NewFoundationHandlerWithNativeAuth(apiPool, nativeAuth,
		configregistry.Ed25519TrustStore{}, runtime, nil, nativeHandlers)
	if err != nil {
		t.Fatal(err)
	}

	loginBody := `{"loginName":"` + loginName + `","password":"wrong-http-password"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	request.Host = "ops.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", response.Code)
	}

	loginBody = `{"loginName":"` + loginName + `","password":"native-http-password"}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	request.Host = "ops.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("login status = %d, want 201; body=%s", response.Code, response.Body.String())
	}
	if account, err := sessions.FindLoginAccount(ctx, loginName); err != nil || account.FailedCount != 0 {
		t.Fatalf("successful login did not clear unlocked failures: count=%d err=%v", account.FailedCount, err)
	}
	cookies := response.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == auth.NativeSessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || !sessionCookie.Secure ||
		sessionCookie.Path != "/" || sessionCookie.Domain != "" {
		t.Fatalf("session cookie flags invalid: %+v", sessionCookie)
	}
	var loginResponse struct {
		RequestID string `json:"requestId"`
		Data      struct {
			Mode               string `json:"mode"`
			MustChangePassword bool   `json:"mustChangePassword"`
			CSRF               struct {
				Token string `json:"token"`
			} `json:"csrf"`
			Revocation struct {
				CredentialVersion int `json:"credentialVersion"`
				SessionVersion    int `json:"sessionVersion"`
			} `json:"revocation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &loginResponse); err != nil {
		t.Fatal(err)
	}
	if loginResponse.RequestID == "" || loginResponse.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatal("login envelope and response header request IDs differ")
	}
	t.Logf("tenant=%s user=%s loginRequestId=%s", tenantA, firstAdmin.UserID, loginResponse.RequestID)
	if loginResponse.Data.Mode != "first_password_only" || !loginResponse.Data.MustChangePassword {
		t.Fatalf("first administrator login mode = %+v", loginResponse.Data)
	}
	if len(loginResponse.Data.CSRF.Token) < 16 {
		t.Fatalf("login CSRF token is missing or too short")
	}
	var loginAudit int
	if err := bootstrapDB.QueryRow(`SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='identity.native_login.succeeded' AND subject=$2`, tenantA, firstAdmin.Subject).Scan(&loginAudit); err != nil || loginAudit != 1 {
		t.Fatalf("login audit count=%d err=%v, want 1", loginAudit, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.Host = "ops.example"
	request.AddCookie(sessionCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("session status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"grants":[]`) {
		t.Fatalf("first-password session exposed business grants: %s", response.Body.String())
	}
	var sessionView struct {
		Data struct {
			CSRF struct {
				Token string `json:"token"`
			} `json:"csrf"`
			Authorization struct {
				Grants []any `json:"grants"`
			} `json:"authorization"`
			AuthorizedClusters []any `json:"authorizedClusters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &sessionView); err != nil {
		t.Fatal(err)
	}
	if sessionView.Data.CSRF.Token != loginResponse.Data.CSRF.Token {
		t.Fatalf("restored CSRF token does not match the same server session")
	}
	if len(sessionView.Data.Authorization.Grants) != 0 || len(sessionView.Data.AuthorizedClusters) != 0 {
		t.Fatalf("first-password session returned business grants")
	}
	for _, denied := range []struct {
		method, path, body, origin, csrf string
	}{
		{http.MethodGet, "/api/v1/resources", "", "", ""},
		{http.MethodPost, "/api/v1/auth/password:change", `{}`, "", ""},
		{http.MethodPost, "/api/v1/auth/logout", `{}`, "https://other.example", loginResponse.Data.CSRF.Token},
	} {
		req := httptest.NewRequest(denied.method, denied.path, strings.NewReader(denied.body))
		req.Host = "ops.example"
		req.AddCookie(sessionCookie)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Origin", denied.origin)
		req.Header.Set(auth.NativeCSRFHeader, denied.csrf)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("first-password/CSRF/Origin boundary %s %s status=%d, want 403", denied.method, denied.path, rec.Code)
		}
	}

	wrongChange := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password:change", strings.NewReader(`{"currentPassword":"wrong-current-password","newPassword":"replacement-http-password","expectedCredentialVersion":1}`))
	wrongChange.Host = "ops.example"
	wrongChange.AddCookie(sessionCookie)
	wrongChange.Header.Set("Content-Type", "application/json")
	wrongChange.Header.Set("Sec-Fetch-Site", "same-origin")
	wrongChange.Header.Set(auth.NativeCSRFHeader, loginResponse.Data.CSRF.Token)
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrongChange)
	if wrongResponse.Code != http.StatusUnauthorized || !strings.Contains(wrongResponse.Body.String(), `"code":"CURRENT_PASSWORD_INVALID"`) {
		t.Fatal("wrong current password was confused with session expiry")
	}
	if _, err := sessions.FindActiveSession(ctx, identity.DigestToken(sessionCookie.Value)); err != nil {
		t.Fatal("wrong current password invalidated the issued session:", err)
	}
	changeBody := `{"currentPassword":"native-http-password","newPassword":"replacement-http-password","expectedCredentialVersion":1}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/password:change", strings.NewReader(changeBody))
	request.Host = "ops.example"
	request.AddCookie(sessionCookie)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set(auth.NativeCSRFHeader, loginResponse.Data.CSRF.Token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("password change status = %d, want 201; body=%s", response.Code, response.Body.String())
	}
	var passwordResponse struct {
		Data struct {
			CSRF struct {
				Token string `json:"token"`
			} `json:"csrf"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &passwordResponse); err != nil {
		t.Fatal(err)
	}
	rotatedCookie := response.Result().Cookies()
	var rotatedSession *http.Cookie
	for _, cookie := range rotatedCookie {
		if cookie.Name == auth.NativeSessionCookieName {
			rotatedSession = cookie
		}
	}
	if rotatedSession == nil || rotatedSession.Value == sessionCookie.Value {
		t.Fatalf("password change did not rotate cookie")
	}
	var passwordAudit int
	if err := bootstrapDB.QueryRow(`SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='identity.native_password.changed' AND subject=$2`, tenantA, firstAdmin.Subject).Scan(&passwordAudit); err != nil || passwordAudit != 1 {
		t.Fatalf("password audit did not preserve the actual principal: count=%d err=%v", passwordAudit, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.Host = "ops.example"
	request.AddCookie(sessionCookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie after rotation status = %d, want 401", response.Code)
	}

	// P2-04: the shared authorization layer must accept the native grant from
	// identity.role_grants and must not require a copied legacy binding. The
	// same session still must not inherit operator authority.
	for _, probe := range []struct {
		path string
		want int
	}{
		{"/api/v1/admin/clusters", http.StatusOK},
		{"/api/v1/overview?clusterUid=integration-unregistered-cluster", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, probe.path, nil)
		req.Host = "ops.example"
		req.AddCookie(rotatedSession)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != probe.want {
			t.Fatalf("native administrator probe %s status=%d, want %d; body=%s",
				probe.path, rec.Code, probe.want, rec.Body.String())
		}
	}
	var legacyBindings int
	if err := bootstrapDB.QueryRow(`SELECT count(*) FROM platform.role_bindings WHERE tenant_id=$1`, tenantA).Scan(&legacyBindings); err != nil || legacyBindings != 0 {
		t.Fatalf("native administrator depended on a legacy role binding: count=%d err=%v", legacyBindings, err)
	}

	logoutBody := `{"expectedSessionVersion":2,"reason":"integration_test_logout"}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(logoutBody))
	request.Host = "ops.example"
	request.AddCookie(rotatedSession)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set(auth.NativeCSRFHeader, passwordResponse.Data.CSRF.Token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var logoutResponse struct {
		Data identity.SessionRevocation `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &logoutResponse); err != nil {
		t.Fatal(err)
	}
	if logoutResponse.Data.SessionVersion != 3 {
		t.Fatalf("logout session version = %d, want 3", logoutResponse.Data.SessionVersion)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.Host = "ops.example"
	request.AddCookie(rotatedSession)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("session after logout status = %d, want 401", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(logoutBody))
	request.Host = "ops.example"
	request.AddCookie(rotatedSession)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set(auth.NativeCSRFHeader, passwordResponse.Data.CSRF.Token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("repeated logout status=%d, want 401", response.Code)
	}
	var logoutAudits int
	if err := bootstrapDB.QueryRow(`SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='identity.native_session.revoked'`, tenantA).Scan(&logoutAudits); err != nil || logoutAudits != 1 {
		t.Fatalf("logout audit count=%d err=%v, want 1", logoutAudits, err)
	}
	// Repository boundaries are exercised against the same real API duty and
	// tenant transaction. These do not replace the HTTP chain above.
	nowBoundary := time.Now().UTC().Truncate(time.Microsecond)
	boundaryInput := identity.CreateSessionInput{
		TenantID: tenantA, UserID: firstAdmin.UserID, SessionID: uuid.Must(uuid.NewV7()),
		Mode: "full", TokenDigest: identity.DigestToken(loginName + ".boundary-session-token"),
		CSRFDigest:        identity.DigestToken(identity.DeriveCSRFToken(loginName + ".boundary-session-token")),
		CredentialVersion: 2, AuthorizationVersion: 1, SessionVersion: logoutResponse.Data.SessionVersion,
		IdleExpiresAt: nowBoundary.Add(time.Minute), AbsoluteExpiresAt: nowBoundary.Add(time.Hour),
	}
	if err := sessions.CreateSession(ctx, boundaryInput); err != nil {
		t.Fatalf("create boundary session: %v", err)
	}
	t.Run("registered_scopes_keep_roles_and_tenants_separate", func(t *testing.T) {
		operatorCluster, adminCluster, foreignCluster := uuid.New(), uuid.New(), uuid.New()
		operatorUID, adminUID, foreignUID := "http-operator-uid", "http-admin-uid", "http-foreign-uid"
		if _, err := bootstrapDB.Exec(`INSERT INTO platform.cluster_registrations
			(tenant_id,cluster_id,cluster_uid,display_name) VALUES
			($1,$2,$3,'Same name'),($1,$4,$5,'Same name'),($6,$7,$8,'Same name')`,
			tenantA, operatorCluster, operatorUID, adminCluster, adminUID, tenantB, foreignCluster, foreignUID); err != nil {
			t.Fatal(err)
		}
		operatorBinding := uuid.New()
		operatorScope, _ := json.Marshal(identity.SessionScope{ClusterUIDs: []string{operatorUID}, Namespaces: []string{"allowed", "second"}})
		adminScope, _ := json.Marshal(identity.SessionScope{ClusterUIDs: []string{adminUID}, Namespaces: []string{"admin-only"}})
		if _, err := bootstrapDB.Exec(`UPDATE identity.role_grants SET scope=$3
			WHERE tenant_id=$1 AND user_id=$2 AND role='platform_admin'`, tenantA, firstAdmin.UserID, string(adminScope)); err != nil {
			t.Fatal(err)
		}
		if _, err := bootstrapDB.Exec(`INSERT INTO identity.role_grants
			(tenant_id,user_id,binding_id,role,scope,authorization_version,status)
			VALUES ($1,$2,$3,'operator',$4,1,'active')`, tenantA, firstAdmin.UserID, operatorBinding, string(operatorScope)); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := bootstrapDB.Exec(`UPDATE identity.role_grants SET status='revoked',
				revoked_at=clock_timestamp(),revoked_reason='isolated test complete' WHERE binding_id=$1`, operatorBinding); err != nil {
				t.Error(err)
			}
		}()
		if _, err := sessions.ResolveClusterIDs(ctx, tenantA, []string{foreignUID}); !errors.Is(err, identity.ErrSessionInvalid) {
			t.Fatalf("foreign tenant cluster resolved: %v", err)
		}
		token := loginName + ".isolated-scope-session-token"
		input := boundaryInput
		input.SessionID, input.TokenDigest = uuid.New(), identity.DigestToken(token)
		input.CSRFDigest = identity.DigestToken(identity.DeriveCSRFToken(token))
		material, err := sessions.CreateSessionWithAudit(ctx, input, audit.Entry{
			TenantID: tenantA, RecordID: uuid.New(), EventType: "identity.native_login.succeeded",
			EntityKind: "native_session", EntityID: input.SessionID, Subject: "user:" + firstAdmin.UserID.String(),
			Payload: map[string]any{"mode": "full"},
		})
		if err != nil || len(material.Clusters) != 1 || material.Clusters[0].ClusterID != operatorCluster || len(material.Session.Grants) != 2 {
			t.Fatalf("role-specific registered projection: clusters=%v grants=%d err=%v", material.Clusters, len(material.Session.Grants), err)
		}
		for _, scope := range []struct {
			cluster   uuid.UUID
			namespace string
			status    int
		}{{operatorCluster, "allowed", 204}, {operatorCluster, "second", 204}, {operatorCluster, "other", 403}, {adminCluster, "admin-only", 403}} {
			req := httptest.NewRequest(http.MethodGet, "/scope", nil)
			req.AddCookie(&http.Cookie{Name: auth.NativeSessionCookieName, Value: token})
			rec := httptest.NewRecorder()
			nativeAuth.Middleware(auth.RequireOperatorScope(scope.cluster, scope.namespace)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))).ServeHTTP(rec, req)
			if rec.Code != scope.status {
				t.Fatalf("scope %s/%s status=%d, want %d", scope.cluster, scope.namespace, rec.Code, scope.status)
			}
		}
	})
	t.Run("audit_failure_rolls_back_password_and_session_rotation", func(t *testing.T) {
		var existingAuditID uuid.UUID
		if err := bootstrapDB.QueryRow(`SELECT record_id FROM audit.records WHERE tenant_id=$1 ORDER BY tenant_seq LIMIT 1`, tenantA).Scan(&existingAuditID); err != nil {
			t.Fatal(err)
		}
		newHash, err := identity.HashPassword("audit-rollback-password")
		if err != nil {
			t.Fatal(err)
		}
		newDigest := identity.DigestToken(loginName + ".audit-rollback-session")
		_, err = sessions.ChangeOwnPasswordWithAudit(ctx, identity.ChangeOwnPasswordInput{
			TenantID: tenantA, UserID: firstAdmin.UserID, OldSessionID: boundaryInput.SessionID, NewSessionID: uuid.New(),
			NewTokenDigest: newDigest, NewCSRFDigest: identity.DigestToken(identity.DeriveCSRFToken(loginName + ".audit-rollback-session")),
			ExpectedCredentialVersion: 2, ExpectedAuthorizationVersion: 1, ExpectedSessionVersion: boundaryInput.SessionVersion,
			NewPasswordHash: newHash, IdleExpiresAt: nowBoundary.Add(time.Minute), AbsoluteExpiresAt: nowBoundary.Add(time.Hour),
		}, audit.Entry{
			TenantID: tenantA, RecordID: existingAuditID, EventType: "identity.native_password.changed",
			EntityKind: "native_account", EntityID: firstAdmin.UserID, Subject: "user:" + firstAdmin.UserID.String(),
			Payload: map[string]any{"authMethods": []string{"password"}},
		})
		if err == nil {
			t.Fatal("duplicate audit record did not reject the transaction")
		}
		old, err := sessions.FindActiveSession(ctx, boundaryInput.TokenDigest)
		if err != nil || old.CredentialVersion != 2 || old.SessionVersion != boundaryInput.SessionVersion {
			t.Fatalf("audit failure revoked the old session: %v", err)
		}
		if valid, err := identity.VerifyPassword("replacement-http-password", old.PasswordHash); err != nil || !valid {
			t.Fatalf("audit failure changed the password: %v", err)
		}
		if _, err := sessions.FindActiveSession(ctx, newDigest); !errors.Is(err, identity.ErrSessionNotFound) {
			t.Fatalf("audit failure retained rotated session: %v", err)
		}
	})
	t.Run("absolute_expiry_caps_touch_and_rejects_password_change", func(t *testing.T) {
		input := boundaryInput
		input.SessionID, input.TokenDigest = uuid.New(), identity.DigestToken(loginName+".expiry-session")
		input.CSRFDigest = identity.DigestToken(identity.DeriveCSRFToken(loginName + ".expiry-session"))
		input.IdleExpiresAt, input.AbsoluteExpiresAt = time.Now().Add(10*time.Second), time.Now().Add(15*time.Second)
		if err := sessions.CreateSession(ctx, input); err != nil {
			t.Fatal(err)
		}
		idle, err := sessions.TouchSession(ctx, input.TokenDigest, time.Minute)
		if err != nil || !idle.Before(input.AbsoluteExpiresAt) || idle.Before(input.IdleExpiresAt) {
			t.Fatalf("absolute expiry cap: idle=%s err=%v", idle, err)
		}
		if _, err := bootstrapDB.Exec(`UPDATE identity.sessions SET issued_at=clock_timestamp()-interval '2 minutes',
			last_seen_at=clock_timestamp()-interval '90 seconds',idle_expires_at=clock_timestamp()-interval '1 minute',
			absolute_expires_at=clock_timestamp()-interval '30 seconds' WHERE session_id=$1`, input.SessionID); err != nil {
			t.Fatal(err)
		}
		if _, err := sessions.FindActiveSession(ctx, input.TokenDigest); !errors.Is(err, identity.ErrSessionNotFound) {
			t.Fatalf("expired session remained active: %v", err)
		}
		if _, err := sessions.TouchSession(ctx, input.TokenDigest, time.Minute); !errors.Is(err, identity.ErrSessionNotFound) {
			t.Fatalf("expired session touch accepted: %v", err)
		}
		current, err := sessions.FindActiveSession(ctx, boundaryInput.TokenDigest)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sessions.ChangeOwnPassword(ctx, identity.ChangeOwnPasswordInput{
			TenantID: tenantA, UserID: firstAdmin.UserID, OldSessionID: input.SessionID, NewSessionID: uuid.New(),
			NewTokenDigest: identity.DigestToken(loginName + ".expired-change"), NewCSRFDigest: identity.DigestToken(loginName + ".expired-change-csrf"),
			ExpectedCredentialVersion: 2, ExpectedAuthorizationVersion: 1, ExpectedSessionVersion: input.SessionVersion,
			NewPasswordHash: current.PasswordHash, IdleExpiresAt: nowBoundary.Add(time.Minute), AbsoluteExpiresAt: nowBoundary.Add(time.Hour),
		}); !errors.Is(err, identity.ErrSessionVersion) {
			t.Fatalf("expired session password change accepted: %v", err)
		}
	})
	if _, err := bootstrapDB.Exec(`UPDATE identity.accounts SET session_version = session_version + 1 WHERE tenant_id=$1 AND user_id=$2`, tenantA, firstAdmin.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.FindActiveSession(ctx, boundaryInput.TokenDigest); !errors.Is(err, identity.ErrSessionNotFound) {
		t.Fatalf("active lookup accepted stale issued_session_version: %v", err)
	}

	if _, err := bootstrapDB.Exec(`UPDATE identity.login_entry_attempts SET attempt_count=0, window_started_at=clock_timestamp(), locked_until=NULL`); err != nil {
		t.Fatal(err)
	}
	var allowedCount atomic.Int32
	var entryWG sync.WaitGroup
	for range 20 {
		entryWG.Add(1)
		go func() {
			defer entryWG.Done()
			allowed, _, err := sessions.AdmitLoginAttempt(ctx, nowBoundary, 3, time.Hour, time.Minute)
			if err != nil {
				t.Errorf("admit login attempt: %v", err)
				return
			}
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	entryWG.Wait()
	if got := allowedCount.Load(); got != 3 {
		t.Fatalf("concurrent entry admission allowed=%d, want 3", got)
	}
	allowed, retryAt, err := sessions.AdmitLoginAttempt(ctx, nowBoundary.Add(2*time.Hour), 3, time.Hour, time.Minute)
	if err != nil || !allowed || retryAt != nil {
		t.Fatalf("expired entry lock survived window reset: allowed=%v retry=%v err=%v", allowed, retryAt, err)
	}
	if _, err := bootstrapDB.Exec(`UPDATE identity.login_entry_attempts SET attempt_count=0, window_started_at=clock_timestamp(), locked_until=NULL`); err != nil {
		t.Fatal(err)
	}

	var failureWG sync.WaitGroup
	if _, err := bootstrapDB.Exec(`DELETE FROM identity.password_attempts WHERE tenant_id=$1 AND user_id=$2`, tenantA, firstAdmin.UserID); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		failureWG.Add(1)
		go func() {
			defer failureWG.Done()
			if _, _, err := sessions.RecordKnownLoginFailure(ctx, tenantA, firstAdmin.UserID, nowBoundary, 1000, time.Minute); err != nil {
				t.Errorf("concurrent known failure: %v", err)
			}
		}()
	}
	failureWG.Wait()
	accountAfterFailures, err := sessions.FindLoginAccount(ctx, loginName)
	if err != nil || accountAfterFailures.FailedCount != 10 {
		t.Fatalf("concurrent known failures = %d err=%v, want 10", accountAfterFailures.FailedCount, err)
	}
	if _, lock, err := sessions.RecordKnownLoginFailure(ctx, tenantA, firstAdmin.UserID, nowBoundary, 1, time.Minute); err != nil || lock == nil {
		t.Fatalf("known failure threshold did not lock: %v", err)
	}
	var rejectedMessages []string
	for _, rejectedLogin := range []string{loginName, "unknown." + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]} {
		body, err := json.Marshal(map[string]string{"loginName": rejectedLogin, "password": "wrong-http-password"})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body)))
		req.Host = "ops.example"
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("locked/unknown login status=%d, want 401", rec.Code)
		}
		var result struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		rejectedMessages = append(rejectedMessages, result.Message)
	}
	if rejectedMessages[0] == "" || rejectedMessages[0] != rejectedMessages[1] {
		t.Fatal("locked and unknown accounts exposed different login errors")
	}
	if count, lock, err := sessions.RecordKnownLoginFailure(ctx, tenantA, firstAdmin.UserID, nowBoundary.Add(16*time.Minute), 1000, time.Minute); err != nil || count != 1 || lock != nil {
		t.Fatalf("expired account lock survived window reset: count=%d lock=%v err=%v", count, lock, err)
	}

	// A successful session write whose required cluster projection cannot be
	// resolved must roll back the whole transaction, including its audit row.
	if _, err := bootstrapDB.Exec(`
		INSERT INTO identity.role_grants
			(tenant_id,user_id,binding_id,role,scope,authorization_version,status)
		VALUES ($1,$2,$3,'operator','{"clusterUids":["does-not-exist"]}'::jsonb,1,'active')
	`, tenantA, firstAdmin.UserID, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatal(err)
	}
	rollbackInput := boundaryInput
	rollbackInput.SessionID = uuid.Must(uuid.NewV7())
	rollbackInput.TokenDigest = identity.DigestToken(loginName + ".projection-rollback-token")
	rollbackInput.CSRFDigest = identity.DigestToken(identity.DeriveCSRFToken(loginName + ".projection-rollback-token"))
	rollbackInput.SessionVersion = boundaryInput.SessionVersion + 1
	_, err = sessions.CreateSessionWithAudit(ctx, rollbackInput, audit.Entry{
		TenantID: tenantA, RecordID: uuid.Must(uuid.NewV7()),
		EventType: "identity.native_login.succeeded", EntityKind: "native_session",
		EntityID: rollbackInput.SessionID, Subject: "user:" + firstAdmin.UserID.String(),
		Payload: map[string]any{"mode": "full"},
	})
	if err == nil {
		t.Fatal("projection failure did not roll back session")
	}
	var rollbackRows int
	if err := bootstrapDB.QueryRow(`SELECT count(*) FROM identity.sessions WHERE token_digest=$1`, rollbackInput.TokenDigest).Scan(&rollbackRows); err != nil || rollbackRows != 0 {
		t.Fatalf("projection rollback left session rows=%d err=%v", rollbackRows, err)
	}
}
