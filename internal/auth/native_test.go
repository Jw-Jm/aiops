package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/identity"
)

type fakeNativeSessions struct {
	session identity.ActiveSession
	calls   int
	touched bool
}

func (f *fakeNativeSessions) FindActiveSession(ctx context.Context, tokenDigest []byte) (identity.ActiveSession, error) {
	f.calls++
	if len(tokenDigest) != 32 {
		return identity.ActiveSession{}, identity.ErrSessionNotFound
	}
	return f.session, nil
}

func (f *fakeNativeSessions) TouchSession(ctx context.Context, tokenDigest []byte, idleTTL time.Duration) (time.Time, error) {
	f.touched = true
	return f.session.IdleExpiresAt.Add(idleTTL), nil
}

func (f *fakeNativeSessions) ResolveClusterIDs(ctx context.Context, tenantID uuid.UUID, clusterUIDs []string) (map[string]uuid.UUID, error) {
	result := make(map[string]uuid.UUID, len(clusterUIDs))
	for index, uid := range clusterUIDs {
		id, err := uuid.Parse(uid)
		if err != nil {
			return nil, identity.ErrSessionInvalid
		}
		result[uid] = id
		_ = index
	}
	return result, nil
}

func TestNativeMiddlewareUsesDigestCookieAndExactCSRF(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	token := "opaque-cookie-value"
	session := identity.ActiveSession{
		TenantID: tenantID, UserID: userID,
		SessionID: uuid.Must(uuid.NewV7()), Mode: "full",
		CSRFDigest: identity.DigestToken(identity.DeriveCSRFToken(token)), IssuedAt: time.Now().UTC(),
		IdleExpiresAt: time.Now().Add(time.Minute), AbsoluteExpiresAt: time.Now().Add(time.Hour),
		Subject: "user:test", LoginName: "test.user", DisplayName: "Test User",
		Status: "active", PasswordState: identity.PasswordActive,
		AccountRevision: 1, CredentialVersion: 1, AuthorizationVersion: 1,
		SessionVersion: 1, Grants: []identity.SessionGrant{{
			BindingID: uuid.Must(uuid.NewV7()), UserID: userID,
			TenantID: tenantID, Role: string(Operator),
			RawScope:             []byte(`{"clusterUids":["` + tenantID.String() + `"],"namespaces":[]}`),
			AuthorizationVersion: 1, GrantedAt: time.Now().UTC(),
		}},
	}
	store := &fakeNativeSessions{session: session}
	authenticator, err := NewNativeAuthenticator(store, tenantID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler := authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := NativeSessionFromContext(r.Context()); !ok {
			t.Error("native session context missing")
		}
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing cookie status = %d, want 401", response.Code)
	}
	if store.calls != 0 {
		t.Fatalf("lookup called before cookie check: %d", store.calls)
	}

	request.Header.Set("Cookie", NativeSessionCookieName+"="+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("valid GET status = %d, want 200", response.Code)
	}
	if !store.touched {
		t.Fatal("valid GET did not touch idle expiry")
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/policies", nil)
	request.Header.Set("Cookie", NativeSessionCookieName+"="+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403", response.Code)
	}

	request.Header.Set(NativeCSRFHeader, "wrong-csrf")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("wrong CSRF status = %d, want 403", response.Code)
	}

	request.Header.Set(NativeCSRFHeader, identity.DeriveCSRFToken(token))
	request.Header.Set("Origin", "http://"+request.Host)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("valid CSRF status = %d, want 200", response.Code)
	}
}

func TestNativeMiddlewareRestrictsFirstPasswordSession(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	token := "first-password-token"
	store := &fakeNativeSessions{session: identity.ActiveSession{
		TenantID: tenantID, UserID: uuid.Must(uuid.NewV7()),
		SessionID: uuid.Must(uuid.NewV7()), Mode: "first_password_only",
		CSRFDigest: identity.DigestToken(identity.DeriveCSRFToken(token)), IssuedAt: time.Now().UTC(),
		IdleExpiresAt:     time.Now().Add(time.Minute),
		AbsoluteExpiresAt: time.Now().Add(time.Hour),
		Subject:           "user:first", LoginName: "first.admin",
		DisplayName: "First Admin", Status: "active",
		PasswordState: identity.PasswordChangeRequired, MustChangePassword: true,
		AccountRevision: 1, CredentialVersion: 1, AuthorizationVersion: 1, SessionVersion: 1,
	}}
	authenticator, err := NewNativeAuthenticator(store, tenantID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler := authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	request.Header.Set("Cookie", NativeSessionCookieName+"="+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("business read on first-password session status = %d, want 403", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/password:change", nil)
	request.Host = "ops.example"
	request.Header.Set("Cookie", NativeSessionCookieName+"="+token)
	request.Header.Set("Origin", "http://ops.example")
	request.Header.Set(NativeCSRFHeader, identity.DeriveCSRFToken(token))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("password change on first-password session status = %d, want 200", response.Code)
	}
}

func TestNativeLoginRequiresSameOriginEvidence(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	store := &fakeNativeSessions{}
	authenticator, err := NewNativeAuthenticator(store, tenantID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler := authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.Host = "ops.example"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing origin evidence status = %d, want 403", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.Host = "ops.example"
	request.Header.Set("Origin", "https://evil.example")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.Host = "ops.example"
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin fetch metadata status = %d, want 200", response.Code)
	}
}

func TestSessionMaterialIsNotSerialized(t *testing.T) {
	session := identity.ActiveSession{
		CSRFDigest:   []byte("01234567890123456789012345678901"),
		PasswordHash: "argon2id$secret",
	}
	encoded, err := session.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if contains := string(session.CSRFDigest); len(contains) > 0 && bytesContains(encoded, session.CSRFDigest) {
		t.Fatalf("CSRF digest serialized: %s", text)
	}
	if bytesContains(encoded, []byte(session.PasswordHash)) {
		t.Fatalf("password hash serialized: %s", text)
	}
}

func bytesContains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

var _ = errors.New

func TestNativeMiddlewarePreservesRequestContextAndRejectsCrossOriginWrite(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	token := "context-token"
	session := identity.ActiveSession{
		TenantID: tenantID, UserID: uuid.Must(uuid.NewV7()),
		SessionID: uuid.Must(uuid.NewV7()), Mode: "full",
		CSRFDigest: identity.DigestToken(identity.DeriveCSRFToken(token)),
		IssuedAt:   time.Now().UTC(), IdleExpiresAt: time.Now().Add(time.Minute),
		AbsoluteExpiresAt: time.Now().Add(time.Hour), Subject: "user:context",
		Status: "active", AccountRevision: 1, CredentialVersion: 1,
		AuthorizationVersion: 1, SessionVersion: 1,
	}
	store := &fakeNativeSessions{session: session}
	authenticator, err := NewNativeAuthenticator(store, tenantID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	type contextKey string
	const key contextKey = "kept"
	handler := authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(key) != "value" {
			t.Error("request context values were dropped")
		}
		if r.Context().Err() != nil {
			if !errors.Is(r.Context().Err(), context.Canceled) {
				t.Errorf("request context cancellation changed: %v", r.Context().Err())
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	request = request.WithContext(context.WithValue(request.Context(), key, "value"))
	request.Header.Set("Cookie", NativeSessionCookieName+"="+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("context GET status = %d, want 200", response.Code)
	}
	canceledContext, cancel := context.WithCancel(context.WithValue(context.Background(), key, "value"))
	cancel()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil).WithContext(canceledContext)
	request.Header.Set("Cookie", NativeSessionCookieName+"="+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("canceled context GET status = %d, want 200", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.Host = "ops.example"
	request.Header.Set("Cookie", NativeSessionCookieName+"="+token)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set(NativeCSRFHeader, identity.DeriveCSRFToken(token))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin logout status = %d, want 403", response.Code)
	}
}

func TestNativeRequestContextKeepsEveryNamespaceForSingleCluster(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	clusterID := uuid.Must(uuid.NewV7())
	session := identity.ActiveSession{
		TenantID: tenantID, UserID: uuid.Must(uuid.NewV7()),
		SessionID: uuid.Must(uuid.NewV7()), Mode: "full",
		AuthorizationVersion: 1, SessionVersion: 1,
		Grants: []identity.SessionGrant{{
			BindingID: uuid.Must(uuid.NewV7()), UserID: uuid.Must(uuid.NewV7()),
			TenantID: tenantID, Role: string(Operator),
			ClusterUIDs:          []string{clusterID.String()},
			Namespaces:           []string{"default", "kube-system"},
			AuthorizationVersion: 1, GrantedAt: time.Now().UTC(),
		}},
	}
	store := &fakeNativeSessions{session: session}
	authenticator, err := NewNativeAuthenticator(store, tenantID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := authenticator.requestContext(context.Background(), session, "req", "")
	if err != nil {
		t.Fatalf("single-cluster namespaces rejected: %v", err)
	}
	requestContext, ok := RequestContextFromContext(ctx)
	if !ok || len(requestContext.NamespaceScopes) != 2 {
		t.Fatalf("namespace projection = %+v, want two entries", requestContext.NamespaceScopes)
	}
}

func TestVerifyNativeOriginUsesConfiguredPublicOriginOnly(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://internal:8080/login", nil)
	request.Header.Set("Origin", "https://ops.example")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("X-Forwarded-Host", "evil.example")
	if err := VerifyNativeOrigin(request, "https://ops.example"); err != nil {
		t.Fatalf("configured public origin rejected: %v", err)
	}
	if err := VerifyNativeOrigin(request, "https://other.example"); err == nil {
		t.Fatal("wrong configured origin accepted")
	}
}

func TestNativeRequestContextKeepsOnlyOperatorClusterScope(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	operatorCluster := uuid.Must(uuid.NewV7())
	adminCluster := uuid.Must(uuid.NewV7())
	session := identity.ActiveSession{
		TenantID: tenantID, UserID: uuid.Must(uuid.NewV7()),
		SessionID: uuid.Must(uuid.NewV7()), Mode: "full",
		AuthorizationVersion: 1, SessionVersion: 1,
		Grants: []identity.SessionGrant{
			{
				BindingID: uuid.Must(uuid.NewV7()), UserID: uuid.Must(uuid.NewV7()),
				TenantID: tenantID, Role: string(Operator),
				ClusterUIDs:          []string{operatorCluster.String()},
				Namespaces:           []string{"operator-ns"},
				AuthorizationVersion: 1, GrantedAt: time.Now().UTC(),
			},
			{
				BindingID: uuid.Must(uuid.NewV7()), UserID: uuid.Must(uuid.NewV7()),
				TenantID: tenantID, Role: string(PlatformAdmin),
				ClusterUIDs:          []string{adminCluster.String()},
				Namespaces:           []string{"admin-ns"},
				AuthorizationVersion: 1, GrantedAt: time.Now().UTC(),
			},
		},
	}
	store := &fakeNativeSessions{session: session}
	authenticator, err := NewNativeAuthenticator(store, tenantID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := authenticator.requestContext(context.Background(), session, "req", "")
	if err != nil {
		t.Fatal(err)
	}
	requestContext, ok := RequestContextFromContext(ctx)
	if !ok {
		t.Fatal("request context missing")
	}
	if len(requestContext.ClusterScopes) != 0 {
		t.Fatalf("namespace grant widened to cluster scopes = %v", requestContext.ClusterScopes)
	}
	if len(requestContext.NamespaceScopes) != 1 || requestContext.NamespaceScopes[0].Namespace != "operator-ns" {
		t.Fatalf("namespace scopes = %+v, want operator-only", requestContext.NamespaceScopes)
	}
	ctx = WithNativeSession(ctx, NativeSessionContext{Session: session})
	for _, probe := range []struct {
		cluster   uuid.UUID
		namespace string
		status    int
	}{
		{operatorCluster, "operator-ns", 204}, {operatorCluster, "other", 403},
		{operatorCluster, "", 403}, {adminCluster, "admin-ns", 403}, {uuid.Nil, "operator-ns", 403},
	} {
		r := httptest.NewRequest(http.MethodGet, "/scope", nil).WithContext(ctx)
		w := httptest.NewRecorder()
		RequireOperatorScope(probe.cluster, probe.namespace)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
		if w.Code != probe.status {
			t.Fatalf("native namespace scope %s/%s=%d want %d", probe.cluster, probe.namespace, w.Code, probe.status)
		}
	}
	requestContext.ClusterScopes = []uuid.UUID{operatorCluster}
	requestContext.NamespaceScopes = nil
	ctx = WithRequestContext(ctx, requestContext)
	w := httptest.NewRecorder()
	RequireOperatorScope(operatorCluster, "any-namespace")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/scope", nil).WithContext(ctx))
	if w.Code != 204 {
		t.Fatal("whole-cluster operator grant rejected a namespace")
	}
}
