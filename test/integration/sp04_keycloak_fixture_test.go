package integration

import (
	"context"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"ops-platform/internal/auth"
	"ops-platform/internal/integrations/keycloak"
	"os"
	"strings"
	"testing"
	"time"
)

func sp04KeycloakToken(t *testing.T, ctx context.Context, tenantID uuid.UUID) keycloak.TokenSet {
	issuer := os.Getenv("SP03_KEYCLOAK_TEST_ISSUER")
	adminPassword := os.Getenv("SP03_KEYCLOAK_TEST_ADMIN_PASSWORD")
	adminToken, serverURL := isolatedKeycloakAdminToken(t, ctx, issuer, adminPassword)
	callbackQuery := make(chan url.Values, 2)
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbackQuery <- r.URL.Query()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()
	clientID, clientSecret := configureIsolatedKeycloakClient(t, ctx, serverURL, adminToken, callbackServer.URL+"/callback")
	// tenantID is supplied by the owned SP04 database.
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

	return initial
}
