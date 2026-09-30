package keycloak

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPKCELoginFlowAndStepUpClaims(t *testing.T) {
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer, nonce, receivedVerifier string
	acr := "urn:ops:loa:2"
	includeAuthTime := true
	var tokenRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/realms/ops/.well-known/openid-configuration":
			_, _ = w.Write([]byte("{\"issuer\":\"" + issuer + "\",\"authorization_endpoint\":\"" + issuer + "/auth\",\"token_endpoint\":\"" + issuer + "/token\",\"jwks_uri\":\"" + issuer + "/certs\",\"response_types_supported\":[\"code\"],\"subject_types_supported\":[\"public\"],\"id_token_signing_alg_values_supported\":[\"RS256\"]}"))
		case "/realms/ops/certs":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{clientTestJWK(signingKey)}})
		case "/realms/ops/token":
			tokenRequests++
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token exchange: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			receivedVerifier = r.Form.Get("code_verifier")
			now := time.Now().UTC()
			claims := map[string]any{
				"iss": issuer, "sub": "user-a", "aud": "ops-web", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
				"nonce": nonce, "tenant_id": "018f0f2b-91c2-7d42-a8dc-f719c5987350",
				"tenant_ids": []string{"018f0f2b-91c2-7d42-a8dc-f719c5987350", "018f0f2b-91c2-7d42-a8dc-f719c5987351"},
				"sid":        "kc-session-a", "acr": acr,
			}
			if includeAuthTime {
				claims["auth_time"] = now.Unix()
			}
			rawIDToken := clientTestToken(t, signingKey, claims)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "opaque-test-access-token", "token_type": "Bearer", "expires_in": 60, "id_token": rawIDToken,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL + "/realms/ops"
	client, err := NewClient(t.Context(), Config{
		IssuerURL: issuer, ClientID: "ops-web", RedirectURL: server.URL + "/callback", StepUpACR: "urn:ops:loa:2",
	})
	if err != nil {
		t.Fatalf("initialize Keycloak client: %v", err)
	}
	flow, err := client.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(flow.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	nonce = query.Get("nonce")
	if query.Get("state") != flow.State || query.Get("nonce") != flow.Nonce ||
		query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") != ProofKeyChallenge(flow.CodeVerifier) ||
		query.Get("acr_values") != "urn:ops:loa:2" || query.Get("max_age") != "0" || query.Get("prompt") != "login" {
		t.Fatalf("PKCE or step-up parameters are missing from authorization request: %v", query)
	}
	if _, err := client.Exchange(t.Context(), "authorization-code", "wrong-state", flow); err == nil || tokenRequests != 0 {
		t.Fatalf("callback state mismatch was not rejected before exchange: err=%v token_requests=%d", err, tokenRequests)
	}
	tokens, err := client.Exchange(t.Context(), "authorization-code", flow.State, flow)
	if err != nil {
		t.Fatalf("exchange and verify PKCE callback: %v", err)
	}
	if tokens.Subject != "user-a" || tokens.SID != "kc-session-a" || tokens.ACR != "urn:ops:loa:2" || tokens.TenantID != "018f0f2b-91c2-7d42-a8dc-f719c5987350" ||
		len(tokens.TenantIDs) != 2 || tokens.AccessToken != "opaque-test-access-token" || receivedVerifier != flow.CodeVerifier || tokenRequests != 1 {
		t.Fatalf("PKCE callback claims or exchange binding are incorrect: subject=%s sid=%s acr=%s tenant=%s requests=%d", tokens.Subject, tokens.SID, tokens.ACR, tokens.TenantID, tokenRequests)
	}

	acr = "urn:ops:loa:1"
	wrongACRFlow, err := client.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	wrongACRURL, err := url.Parse(wrongACRFlow.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	nonce = wrongACRURL.Query().Get("nonce")
	if _, err := client.Exchange(t.Context(), "authorization-code", wrongACRFlow.State, wrongACRFlow); err == nil {
		t.Fatal("step-up callback with a lower ACR was accepted")
	}

	acr = "urn:ops:loa:2"
	includeAuthTime = false
	missingAuthTimeFlow, err := client.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	missingAuthTimeURL, err := url.Parse(missingAuthTimeFlow.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	nonce = missingAuthTimeURL.Query().Get("nonce")
	if _, err := client.Exchange(t.Context(), "authorization-code", missingAuthTimeFlow.State, missingAuthTimeFlow); err == nil {
		t.Fatal("step-up callback without auth_time was accepted")
	}
}

func clientTestJWK(signingKey *rsa.PrivateKey) map[string]string {
	return map[string]string{
		"kty": "RSA", "kid": "client-key", "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(signingKey.PublicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(signingKey.PublicKey.E)).Bytes()),
	}
}

func clientTestToken(t *testing.T, signingKey *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "client-key", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, signingKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestProofKeyChallengeDoesNotExposeVerifier(t *testing.T) {
	verifier := "a-verifier-that-must-never-be-sent-as-the-challenge"
	challenge := ProofKeyChallenge(verifier)
	if challenge == verifier || len(challenge) != 43 || strings.Contains(challenge, "=") {
		t.Fatalf("invalid S256 challenge %q", challenge)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil || len(decoded) != sha256.Size {
		t.Fatalf("challenge is not a SHA-256 base64url value: %v", err)
	}
}
