package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"golang.org/x/net/html"
	"ops-platform/internal/app"
	"ops-platform/internal/auth"
	"ops-platform/internal/integrations/keycloak"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
)

func TestKeycloakAuthorizationCodePKCEAndStepUp(t *testing.T) {
	issuer := os.Getenv("SP03_KEYCLOAK_TEST_ISSUER")
	adminPassword := os.Getenv("SP03_KEYCLOAK_TEST_ADMIN_PASSWORD")
	if issuer == "" || adminPassword == "" || os.Getenv("SP03_TEST_DATABASE_URL") == "" {
		t.Skip("isolated Keycloak and PostgreSQL test environment variables are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	adminToken, serverURL := isolatedKeycloakAdminToken(t, ctx, issuer, adminPassword)
	callbackQuery := make(chan url.Values, 2)
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbackQuery <- r.URL.Query()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()
	clientID, clientSecret := configureIsolatedKeycloakClient(t, ctx, serverURL, adminToken, callbackServer.URL+"/callback")
	tenantID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c59873a0")
	username, password := createIsolatedKeycloakUser(t, ctx, serverURL, adminToken, tenantID)
	var otpSecret string

	client, err := keycloak.NewClient(ctx, keycloak.Config{
		IssuerURL: issuer, ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: callbackServer.URL + "/callback", StepUpACR: auth.StepUpACRLevel2,
	})
	if err != nil {
		t.Fatalf("initialize isolated Keycloak OIDC client: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Jar: jar}

	login := func(stepUp bool) keycloak.TokenSet {
		t.Helper()
		otpAttempt := 0
		flow, err := client.Begin(stepUp)
		if err != nil {
			t.Fatalf("begin Keycloak login: %v", err)
		}
		response, err := browser.Get(flow.AuthorizationURL)
		if err != nil {
			t.Fatalf("open isolated Keycloak authorization endpoint: %v", err)
		}
		for attempt := 0; attempt < 5; attempt++ {
			if response.Request.URL.Host == callbackServer.Listener.Addr().String() && response.Request.URL.Path == "/callback" {
				response.Body.Close()
				select {
				case values := <-callbackQuery:
					tokens, err := client.Exchange(ctx, values.Get("code"), values.Get("state"), flow)
					if err != nil {
						t.Fatalf("verify live Keycloak code exchange: %v", err)
					}
					return tokens
				case <-ctx.Done():
					t.Fatal("Keycloak callback was not received before timeout")
				}
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
			response.Body.Close()
			if readErr != nil {
				t.Fatalf("read Keycloak login form: %v", readErr)
			}
			form, found := findLoginForm(body)
			setupOTP := false
			if !found {
				var setupFound bool
				form, otpSecret, setupFound = findOTPSetupForm(body)
				if !setupFound {
					t.Fatalf("Keycloak returned no recognized login form (HTTP %d, page: %s)", response.StatusCode, keycloakPageSummary(body))
				}
				setupOTP = true
			}
			if setupOTP {
				form.fields.Set("userLabel", "SP03 integration test")
				form.fields.Set("totp", currentTOTP(t, otpSecret, time.Now()))
			} else if form.fields.Has("username") {
				form.fields.Set("username", username)
				form.fields.Set("password", password)
			} else if form.fields.Has("otp") {
				if strings.Contains(keycloakPageSummary(body), "Invalid authenticator code") {
					otpAttempt++
				}
				otpWindow := []time.Duration{0, -30 * time.Second, 30 * time.Second}
				if otpAttempt >= len(otpWindow) {
					t.Fatal("isolated Keycloak rejected OTPs for the current, previous, and next TOTP windows")
				}
				form.fields.Set("otp", currentTOTP(t, otpSecret, time.Now().Add(otpWindow[otpAttempt])))
			} else {
				t.Fatal("Keycloak login form did not request password or OTP")
			}
			formRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, form.action, strings.NewReader(form.fields.Encode()))
			if err != nil {
				t.Fatal(err)
			}
			formRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response, err = browser.Do(formRequest)
			if err != nil {
				t.Fatalf("submit Keycloak login form: %v", err)
			}
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		response.Body.Close()
		t.Fatalf("Keycloak login did not reach the registered callback (HTTP %d, page: %s)", response.StatusCode, keycloakPageSummary(body))
		return keycloak.TokenSet{}
	}

	initial := login(false)
	if initial.Subject == "" || initial.TenantID != tenantID.String() || initial.SID == "" || initial.ACR == "" {
		t.Fatalf("live Keycloak login omitted verified identity claims: subject=%q tenant=%q sid_set=%v acr=%q", initial.Subject, initial.TenantID, initial.SID != "", initial.ACR)
	}
	waitForNextTOTPPeriod(t, ctx)
	stepUp := login(true)
	if stepUp.ACR != auth.StepUpACRLevel2 || stepUp.AuthTime.IsZero() || time.Since(stepUp.AuthTime) > 5*time.Minute || stepUp.Subject != initial.Subject || stepUp.TenantID != tenantID.String() || stepUp.SID == "" {
		t.Fatalf("live Keycloak reauthentication did not satisfy step-up identity: acr=%q auth_time_set=%v subject_match=%v tenant_match=%v sid_set=%v", stepUp.ACR, !stepUp.AuthTime.IsZero(), stepUp.Subject == initial.Subject, stepUp.TenantID == tenantID.String(), stepUp.SID != "")
	}
	verifyPersistedKeycloakStepUp(t, ctx, stepUp, tenantID)
}

func waitForNextTOTPPeriod(t *testing.T, ctx context.Context) {
	t.Helper()
	period := 30 * time.Second
	remaining := period - time.Duration(time.Now().UnixNano()%int64(period)) + 150*time.Millisecond
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatalf("waiting for an unused Keycloak TOTP period: %v", ctx.Err())
	}
}

type loginForm struct {
	action string
	fields url.Values
}

func findOTPSetupForm(body []byte) (loginForm, string, bool) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return loginForm{}, "", false
	}
	var result loginForm
	secret := ""
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "form" && result.action == "" {
			action := attribute(node, "action")
			fields := url.Values{}
			hasTOTP := false
			var inputs func(*html.Node)
			inputs = func(child *html.Node) {
				if child.Type == html.ElementNode && child.Data == "input" {
					name, value := attribute(child, "name"), attribute(child, "value")
					if name != "" {
						fields.Set(name, value)
					}
					if name == "totp" {
						hasTOTP = true
					}
				}
				for childChild := child.FirstChild; childChild != nil; childChild = childChild.NextSibling {
					inputs(childChild)
				}
			}
			inputs(node)
			if hasTOTP && fields.Get("totpSecret") != "" && action != "" {
				result = loginForm{action: action, fields: fields}
				secret = fields.Get("totpSecret")
				return
			}
		}
		for child := node.FirstChild; child != nil && result.action == ""; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return result, secret, result.action != ""
}

func findLoginForm(body []byte) (loginForm, bool) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return loginForm{}, false
	}
	var result loginForm
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "form" && result.action == "" {
			action := attribute(node, "action")
			fields := url.Values{}
			hasUsername, hasPassword, hasOTP := false, false, false
			var inputs func(*html.Node)
			inputs = func(child *html.Node) {
				if child.Type == html.ElementNode && child.Data == "input" {
					name, value := attribute(child, "name"), attribute(child, "value")
					if name != "" {
						fields.Set(name, value)
					}
					switch name {
					case "username":
						hasUsername = true
					case "password":
						hasPassword = true
					case "otp":
						hasOTP = true
					}
				}
				for childChild := child.FirstChild; childChild != nil; childChild = childChild.NextSibling {
					inputs(childChild)
				}
			}
			inputs(node)
			if hasOTP || hasUsername && hasPassword {
				result = loginForm{action: action, fields: fields}
				return
			}
		}
		for child := node.FirstChild; child != nil && result.action == ""; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return result, result.action != ""
}

func keycloakPageSummary(body []byte) string {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "unparseable HTML"
	}
	var values []string
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style") {
			return
		}
		if node.Type == html.ElementNode && attribute(node, "id") == "kc-totp-secret-key" {
			return
		}
		if node.Type == html.TextNode {
			value := strings.Join(strings.Fields(node.Data), " ")
			if value != "" {
				values = append(values, value)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	result := strings.Join(values, " ")
	if len(result) > 240 {
		result = result[:240]
	}
	return result
}

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func isolatedKeycloakAdminToken(t *testing.T, ctx context.Context, issuer, password string) (string, string) {
	t.Helper()
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		t.Fatal("isolated Keycloak issuer URL is invalid")
	}
	serverURL := parsed.Scheme + "://" + parsed.Host
	adminUsername := os.Getenv("SP03_KEYCLOAK_TEST_ADMIN_USERNAME")
	if adminUsername == "" {
		adminUsername = "sp03admin"
	}
	values := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {adminUsername}, "password": {password}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/realms/master/protocol/openid-connect/token", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request isolated Keycloak bootstrap token: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("isolated Keycloak bootstrap token returned HTTP %d", response.StatusCode)
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.AccessToken == "" {
		t.Fatalf("isolated Keycloak bootstrap token response is invalid: %v", err)
	}
	return result.AccessToken, serverURL
}

func configureIsolatedKeycloakClient(t *testing.T, ctx context.Context, serverURL, adminToken, redirectURL string) (string, string) {
	t.Helper()
	var clients []struct {
		ID string `json:"id"`
	}
	keycloakAdminJSON(t, ctx, serverURL, adminToken, http.MethodGet, "/admin/realms/ops/clients?clientId=ops-web", nil, &clients)
	if len(clients) != 1 || clients[0].ID == "" {
		t.Fatal("isolated ops-web client is missing")
	}
	var client map[string]any
	path := "/admin/realms/ops/clients/" + url.PathEscape(clients[0].ID)
	keycloakAdminJSON(t, ctx, serverURL, adminToken, http.MethodGet, path, nil, &client)
	client["redirectUris"] = []string{redirectURL}
	parsed, _ := url.Parse(redirectURL)
	client["webOrigins"] = []string{parsed.Scheme + "://" + parsed.Host}
	keycloakAdminJSON(t, ctx, serverURL, adminToken, http.MethodPut, path, client, nil)
	var secret struct {
		Value string `json:"value"`
	}
	keycloakAdminJSON(t, ctx, serverURL, adminToken, http.MethodGet, path+"/client-secret", nil, &secret)
	if secret.Value == "" {
		t.Fatal("isolated ops-web client secret is missing")
	}
	return "ops-web", secret.Value
}

func createIsolatedKeycloakUser(t *testing.T, ctx context.Context, serverURL, adminToken string, tenantID uuid.UUID) (string, string) {
	t.Helper()
	var passwordBytes [24]byte
	if _, err := rand.Read(passwordBytes[:]); err != nil {
		t.Fatal(err)
	}
	password := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(passwordBytes[:])
	username := "sp03-live-" + strings.ReplaceAll(uuid.Must(uuid.NewV7()).String(), "-", "")
	user := map[string]any{
		"username": username, "enabled": true, "emailVerified": true,
		"email": username + "@example.invalid", "firstName": "SP03", "lastName": "Integration",
		"attributes":      map[string]any{"tenant_id": []string{tenantID.String()}, "tenant_ids": []string{tenantID.String()}},
		"requiredActions": []string{"CONFIGURE_TOTP"},
		"credentials":     []any{map[string]any{"type": "password", "value": password, "temporary": false}},
	}
	keycloakAdminJSON(t, ctx, serverURL, adminToken, http.MethodPost, "/admin/realms/ops/users", user, nil)
	var users []struct {
		ID string `json:"id"`
	}
	keycloakAdminJSON(t, ctx, serverURL, adminToken, http.MethodGet, "/admin/realms/ops/users?username="+url.QueryEscape(username)+"&exact=true", nil, &users)
	if len(users) != 1 || users[0].ID == "" {
		t.Fatal("isolated Keycloak test user was not created")
	}
	return username, password
}

func keycloakAdminJSON(t *testing.T, ctx context.Context, serverURL, token string, method, path string, requestBody, responseBody any) {
	t.Helper()
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			t.Fatalf("encode isolated Keycloak admin request: %v", err)
		}
		body = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, serverURL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("isolated Keycloak admin request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		t.Fatalf("isolated Keycloak admin request %s %s returned HTTP %d", method, path, response.StatusCode)
	}
	if responseBody != nil {
		if err := json.NewDecoder(response.Body).Decode(responseBody); err != nil {
			t.Fatalf("decode isolated Keycloak admin response: %v", err)
		}
	}
}

func currentTOTP(t *testing.T, secret string, now time.Time) string {
	t.Helper()
	if secret == "" {
		t.Fatal("Keycloak did not provide the test-only OTP seed")
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write(counter[:])
	result := mac.Sum(nil)
	offset := result[len(result)-1] & 0x0f
	code := (uint32(result[offset])&0x7f)<<24 | uint32(result[offset+1])<<16 | uint32(result[offset+2])<<8 | uint32(result[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000)
}

func verifyPersistedKeycloakStepUp(t *testing.T, ctx context.Context, token keycloak.TokenSet, tenantID uuid.UUID) {
	t.Helper()
	dbctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(dbctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply isolated PostgreSQL role bootstrap: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, dbctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply isolated PostgreSQL schema: %v", err)
	}
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, 'kc-stepup-test', 'Keycloak Step Up Test')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name) VALUES ($1, $2, $3, 'platform_admin')`, tenantID, uuid.Must(uuid.NewV7()), token.Subject); err != nil {
		t.Fatal(err)
	}
	poolConfig := runtimePoolConfig(t, dbctx, db, dbURL, "api_runtime_role")

	pool, err := pgxpool.NewWithConfig(dbctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	request := auth.RequestContext{TenantID: tenantID, Subject: token.Subject, KeycloakSID: token.SID, ACR: token.ACR, AuthTime: token.AuthTime}
	sessionID := uuid.Must(uuid.NewV7())
	var session auth.StepUpSession
	err = persistence.WithTenantTx(dbctx, pool, tenantID, func(tx pgx.Tx) error {
		var err error
		session, err = auth.RecordStepUpSession(dbctx, tx, request, sessionID, []string{auth.StepUpACRLevel2})
		if err != nil {
			return err
		}
		_, err = auth.TouchCurrentStepUpSession(dbctx, tx, request, []string{auth.StepUpACRLevel2})
		return err
	})
	if err != nil {
		t.Fatalf("persist actual Keycloak step-up identity: %v", err)
	}
	if err := db.QueryRowContext(ctx, `UPDATE platform.step_up_sessions SET last_used_at = clock_timestamp() - interval '61 minutes' WHERE tenant_id = $1 AND session_id = $2 RETURNING session_id`, tenantID, session.SessionID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	err = persistence.WithTenantTx(dbctx, pool, tenantID, func(tx pgx.Tx) error {
		_, err := auth.TouchCurrentStepUpSession(dbctx, tx, request, []string{auth.StepUpACRLevel2})
		return err
	})
	if !errors.Is(err, auth.ErrStepUpInvalid) {
		t.Fatalf("idle-expired live Keycloak step-up session returned %v", err)
	}
	verifyFoundationAPI(t, dbctx, dbURL, token)
}

func verifyFoundationAPI(t *testing.T, ctx context.Context, dsn string, token keycloak.TokenSet) {
	t.Helper()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := "sp03_api_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, `CREATE ROLE "`+name+`" LOGIN; GRANT api_runtime_role TO "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, `DROP ROLE "`+name+`"`)
	u, _ := url.Parse(dsn)
	u.User = url.User(name)
	issuer := os.Getenv("SP03_KEYCLOAK_TEST_ISSUER")
	application, err := app.NewAPI(app.AppConfig{DatabaseURL: u.String(), OIDCIssuerURL: issuer, ProfilePath: isolatedRuntimeProfile(t, issuer, "", "")})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:1/v1/traces")
	t.Setenv("PLATFORM_TRACING_ENABLED", "true")
	runtime, _ := observability.NewRuntime(ctx, "sp03-review-api")
	var stdout bytes.Buffer
	runtime.Logger = observability.NewLogger(io.MultiWriter(os.Stdout, &stdout), slog.LevelInfo)
	defer runtime.Close(ctx)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serveCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- application.Serve(serveCtx, listener, runtime) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	endpoint := "http://" + listener.Addr().String()
	for _, path := range []string{"/api/v1/admin/tenants", "/api/v1/admin/clusters", "/api/v1/admin/source-registrations", "/api/v1/admin/policy-bundles", "/api/v1/admin/recipes", "/api/v1/admin/tools"} {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
		request.Header.Set("Authorization", "Bearer "+token.AccessToken)
		request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("foundation HTTP request failed")
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("foundation route %s returned %d", path, response.StatusCode)
		}
	}
	var previous string
	for attempt := 0; attempt < 2; attempt++ {
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/api/v1/auth/step-up-sessions", strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+token.AccessToken)
		request.Header.Set("Idempotency-Key", "review-step-up")
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("step-up HTTP request failed")
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusCreated {
			t.Fatalf("step-up API returned %d", response.StatusCode)
		}
		if attempt > 0 && string(body) != previous {
			t.Fatal("step-up response was not replayed exactly")
		}
		previous = string(body)
	}
	t.Log("actual APIApp served six management routes and replayed claim-bound step-up using real Keycloak JWT and distinct database login")
	flush, finish := context.WithTimeout(ctx, 4*time.Second)
	if err := runtime.Tracing.Flush(flush); err == nil {
		t.Error("disconnected OTLP endpoint unexpectedly succeeded")
	}
	finish()
	if !runtime.Metrics.Degraded() {
		t.Error("actual trace connection refusal did not report degradation")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/v1/admin/clusters", nil)
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("API stopped after trace disconnect")
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("API read failed after trace disconnect")
	}
	t.Log("actual OTLP connection refusal reported degradation; authenticated management read continued")
	if strings.Contains(stdout.String(), token.AccessToken) {
		t.Fatal("actual API stdout contains a user token")
	}
	if endpoint := os.Getenv("SP03_TEST_VICTORIA_LOGS_URL"); endpoint != "" {
		verifyCollectedAPIStdout(t, ctx, endpoint, stdout.Bytes())
	}
}

func verifyCollectedAPIStdout(t *testing.T, ctx context.Context, endpoint string, lines []byte) {
	t.Helper()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/insert/jsonline?_time_field=time&_msg_field=msg", bytes.NewReader(lines))
	request.Header.Set("Content-Type", "application/stream+json")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("collect actual API stdout")
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("VictoriaLogs rejected actual API stdout")
	}
	var first map[string]any
	if err := json.NewDecoder(bytes.NewReader(lines)).Decode(&first); err != nil {
		t.Fatal(err)
	}
	id, ok := first["request_id"].(string)
	if !ok {
		t.Fatal("API stdout correlation missing")
	}
	query := url.Values{"query": {"request_id:" + id}}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get(strings.TrimRight(endpoint, "/") + "/select/logsql/query?" + query.Encode())
		if err == nil {
			body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if err == nil && bytes.Contains(body, []byte(id)) && bytes.Contains(body, []byte("http request complete")) {
				t.Log("actual authenticated API stdout collected and queried in isolated VictoriaLogs")
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("actual API stdout was not queryable in VictoriaLogs")
}
