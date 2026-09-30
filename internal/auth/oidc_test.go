package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type testRoleBindingSource struct {
	mu       sync.Mutex
	tenantID uuid.UUID
	subject  string
	bindings []RoleBinding
}

func (s *testRoleBindingSource) LoadRoleBindings(_ context.Context, tenantID uuid.UUID, subject string) ([]RoleBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenantID, s.subject = tenantID, subject
	return append([]RoleBinding(nil), s.bindings...), nil
}

type testOIDCKey struct {
	kid string
	key *rsa.PrivateKey
}

func TestOIDCRejectsForgedTenantAndWrongAudienceAndRefreshesKeys(t *testing.T) {
	oldKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.RWMutex
	currentKey := testOIDCKey{kid: "key-old", key: oldKey}
	issuer := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/realms/ops/.well-known/openid-configuration":
			_, _ = w.Write([]byte(`{"issuer":"` + issuer + `","authorization_endpoint":"` + issuer + `/protocol/openid-connect/auth","token_endpoint":"` + issuer + `/protocol/openid-connect/token","jwks_uri":"` + issuer + `/certs","response_types_supported":["code"],"subject_types_supported":["public"],"id_token_signing_alg_values_supported":["RS256"]}`))
		case "/realms/ops/certs":
			w.Header().Set("Cache-Control", "no-store")
			mu.RLock()
			key := currentKey
			mu.RUnlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{testJWK(key)}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL + "/realms/ops"
	tenantID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987340")
	clusterID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987341")
	bindings := &testRoleBindingSource{bindings: []RoleBinding{{Role: Operator, ClusterScopes: []uuid.UUID{clusterID}, NamespaceScopes: []NamespaceScope{{ClusterID: clusterID, Namespace: "production"}}}}}
	authenticator, err := NewOIDCAuthenticator(context.Background(), issuer, "ops-api", bindings)
	if err != nil {
		t.Fatalf("initialize OIDC provider: %v", err)
	}
	var reached int
	handler := authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, ok := RequestContextFromContext(r.Context())
		if !ok || request.RequestID != "req-oidc-test" || !containsTenantID(request.TenantIDs, request.TenantID) || len(request.TenantIDs) != 2 || request.Subject != "user-a" || !HasRole(r.Context(), Operator) {
			http.Error(w, "authenticated context is incomplete", http.StatusInternalServerError)
			return
		}
		reached++
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func(rawToken, tenantHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/role-bindings", nil)
		req.Header.Set("Authorization", "Bearer "+rawToken)
		req.Header.Set("X-Request-ID", "req-oidc-test")
		if tenantHeader != "" {
			req.Header.Set("X-Tenant-ID", tenantHeader)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	now := time.Now().UTC()
	validClaims := func(audience string) map[string]any {
		secondTenant := "018f0f2b-91c2-7d42-a8dc-f719c5987342"
		return map[string]any{
			"iss": issuer, "sub": "user-a", "aud": audience, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
			"auth_time": now.Unix(), "tenant_id": tenantID.String(), "tenant_ids": []string{tenantID.String(), secondTenant}, "sid": "sid-a", "acr": "urn:ops:loa:2",
		}
	}
	oldToken := testSignToken(t, testOIDCKey{kid: "key-old", key: oldKey}, validClaims("ops-api"))
	if _, verifyErr := authenticator.verifier.Verify(context.Background(), oldToken); verifyErr != nil {
		t.Fatalf("verify test OIDC token: %v", verifyErr)
	}
	good := request(oldToken, "")
	if good.Code != http.StatusNoContent || reached != 1 {
		t.Fatalf("valid OIDC token was rejected: status=%d body=%s", good.Code, good.Body.String())
	}
	if bindings.tenantID != tenantID || bindings.subject != "user-a" {
		t.Fatalf("role binding lookup did not use verified identity: tenant=%s subject=%q", bindings.tenantID, bindings.subject)
	}
	forged := request(oldToken, "018f0f2b-91c2-7d42-a8dc-f719c5987343")
	if forged.Code != http.StatusForbidden || reached != 1 {
		t.Fatalf("forged tenant header was accepted: status=%d reached=%d", forged.Code, reached)
	}
	allowedSelection := request(oldToken, "018f0f2b-91c2-7d42-a8dc-f719c5987342")
	if allowedSelection.Code != http.StatusNoContent || reached != 2 || bindings.tenantID.String() != "018f0f2b-91c2-7d42-a8dc-f719c5987342" {
		t.Fatalf("signed tenant membership could not be selected: status=%d reached=%d tenant=%s", allowedSelection.Code, reached, bindings.tenantID)
	}
	wrongAudience := request(testSignToken(t, testOIDCKey{kid: "key-old", key: oldKey}, validClaims("other-client")), "")
	if wrongAudience.Code != http.StatusUnauthorized || reached != 2 {
		t.Fatalf("token with wrong audience was accepted: status=%d reached=%d", wrongAudience.Code, reached)
	}
	var errorEnvelope struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(wrongAudience.Body.Bytes(), &errorEnvelope); err != nil || errorEnvelope.RequestID != "req-oidc-test" || wrongAudience.Header().Get("X-Request-ID") != "req-oidc-test" {
		t.Fatalf("OIDC error did not return its request id: body=%s header=%q err=%v", wrongAudience.Body.String(), wrongAudience.Header().Get("X-Request-ID"), err)
	}
	mu.Lock()
	currentKey = testOIDCKey{kid: "key-new", key: newKey}
	mu.Unlock()
	newToken := testSignToken(t, testOIDCKey{kid: "key-new", key: newKey}, validClaims("ops-api"))
	rotated := request(newToken, tenantID.String())
	if rotated.Code != http.StatusNoContent || reached != 3 {
		t.Fatalf("token signed by rotated key was rejected: status=%d body=%s", rotated.Code, rotated.Body.String())
	}
}

func testJWK(value testOIDCKey) map[string]string {
	exponent := big.NewInt(int64(value.key.PublicKey.E)).Bytes()
	return map[string]string{
		"kty": "RSA", "kid": value.kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(value.key.PublicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(exponent),
	}
}

func testSignToken(t *testing.T, signingKey testOIDCKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": signingKey.kid, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, signingKey.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestTraceParentRejectsInvalidValues(t *testing.T) {
	if got := ValidTraceParent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"); got == "" {
		t.Fatal("valid traceparent rejected")
	}
	for _, value := range []string{"", "not-a-trace", "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "00-00000000000000000000000000000000-00f067aa0ba902b7-01"} {
		if got := ValidTraceParent(value); got != "" {
			t.Errorf("invalid traceparent %q accepted as %q", value, got)
		}
	}
	if !strings.Contains(ValidTraceParent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"), "-") {
		t.Fatal("traceparent was not preserved")
	}
}
