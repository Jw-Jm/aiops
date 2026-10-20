package bootstrap

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestOIDCBootstrapKeepsFrozenPKCEAndStepUpContract(t *testing.T) {
	original, err := os.ReadFile("../../deploy/keycloak/realm-ops.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, RealmTemplate) {
		t.Fatal("embedded bootstrap realm drifted from the frozen official realm")
	}
	c := OIDCInput{SchemaVersion: 1, CallbackURL: "https://current.example.invalid/api/v1/auth/callback", TenantID: uuid.New(), Username: "explicit-admin", Email: "explicit-admin@example.invalid", FirstName: "Explicit", LastName: "Admin"}
	private := OIDCPrivateInput{BootstrapUsername: "bootstrap", BootstrapPassword: "separate-private-bootstrap-123456", ClientSecret: "separate-private-client-123456", SubjectPassword: "separate-private-subject-123456"}
	raw, err := BuildOIDCRealm(c, private)
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected map[string]any
	if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(original, &expected) != nil {
		t.Fatal("realm invalid")
	}
	for _, v := range actual["clients"].([]any) {
		client := v.(map[string]any)
		if client["clientId"] == "ops-web" {
			redirects := client["redirectUris"].([]any)
			origins := client["webOrigins"].([]any)
			if len(redirects) != 1 || redirects[0] != c.CallbackURL || len(origins) != 1 || origins[0] != "https://current.example.invalid" || client["secret"] != private.ClientSecret {
				t.Fatal("callback/client secret not explicitly bound")
			}
			delete(client, "secret")
			for _, v := range expected["clients"].([]any) {
				base := v.(map[string]any)
				if base["clientId"] == "ops-web" {
					client["redirectUris"] = base["redirectUris"]
					client["webOrigins"] = base["webOrigins"]
				}
			}
		}
	}
	normalized, _ := json.Marshal(actual)
	frozen, _ := json.Marshal(expected)
	if !bytes.Equal(normalized, frozen) {
		t.Fatal("realm security semantics changed during bootstrap")
	}
	for _, callback := range []string{"http://current.example.invalid/api/v1/auth/callback", "https://current.example.invalid/*", "https://current.example.invalid/api/v1/auth/callback?forward=https://other.invalid"} {
		c.CallbackURL = callback
		if _, err := BuildOIDCRealm(c, private); err == nil {
			t.Fatal("unsafe OIDC callback accepted")
		}
	}
}

func TestOIDCBrowserBootstrapMatchesWebCallbackWithoutClientSecret(t *testing.T) {
	c := OIDCInput{SchemaVersion: 1, ClientMode: "browser-pkce", SubjectID: uuid.NewString(), CallbackURL: "https://current.example.invalid:30443/auth/callback", TenantID: uuid.New(), Username: "explicit-admin", Email: "admin@example.invalid", FirstName: "Explicit", LastName: "Admin"}
	private := OIDCPrivateInput{BootstrapUsername: "bootstrap", BootstrapPassword: "separate-private-bootstrap-123456", SubjectPassword: "separate-private-subject-123456"}
	raw, err := BuildOIDCRealm(c, private)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	json.Unmarshal(raw, &actual)
	found := false
	for _, v := range actual["clients"].([]any) {
		client := v.(map[string]any)
		if client["clientId"] != "ops-web" {
			continue
		}
		found = true
		if client["publicClient"] != true || client["secret"] != nil || client["directAccessGrantsEnabled"] != false || client["implicitFlowEnabled"] != false || client["attributes"].(map[string]any)["pkce.code.challenge.method"] != "S256" {
			t.Fatal("browser PKCE boundary changed")
		}
		redirects := client["redirectUris"].([]any)
		if len(redirects) != 2 || redirects[0] != c.CallbackURL || redirects[1] != "https://current.example.invalid:30443/auth/silent-callback" {
			t.Fatal("callbacks differ from Web")
		}
	}
	if !found {
		t.Fatal("browser client missing")
	}
	for _, callback := range []string{"https://current.example.invalid:30443/api/v1/auth/callback", "https://current.example.invalid:30443/auth/callback?forward=other", "https://current.example.invalid:30443/*"} {
		changed := c
		changed.CallbackURL = callback
		if _, err := BuildOIDCRealm(changed, private); err == nil {
			t.Fatal("unsafe or incompatible callback accepted")
		}
	}
	private.ClientSecret = "unnecessary-private-client-123456"
	if _, err := BuildOIDCRealm(c, private); err == nil {
		t.Fatal("browser client accepted shared secret")
	}
	private.ClientSecret = ""
	c.ClientMode = "other"
	if _, err := BuildOIDCRealm(c, private); err == nil {
		t.Fatal("unknown client mode accepted")
	}
}

func TestOIDCPreallocatedSubjectUsesRealmImportAndVerifiedIdentity(t *testing.T) {
	c := OIDCInput{SchemaVersion: 1, SubjectID: "018f0f2b-91c2-7d42-a8dc-f719c5987204", CallbackURL: "https://current.example.invalid/api/v1/auth/callback", TenantID: uuid.New(), Username: "explicit-admin", Email: "admin@example.invalid", FirstName: "Explicit", LastName: "Admin"}
	private := OIDCPrivateInput{BootstrapUsername: "bootstrap", BootstrapPassword: "separate-private-bootstrap-123456", ClientSecret: "separate-private-client-123456", SubjectPassword: "separate-private-subject-123456"}
	for _, subject := range []string{"PENDING_SUBJECT", uuid.Nil.String(), strings.ToUpper(c.SubjectID), "../users/foreign", " " + c.SubjectID} {
		changed := c
		changed.SubjectID = subject
		if _, err := BuildOIDCRealm(changed, private); err == nil {
			t.Fatalf("invalid subject ID accepted: %q", subject)
		}
	}
	for _, test := range []struct {
		name        string
		mutate      func(map[string]any)
		status      int
		invalidBody string
	}{
		{name: "verified", status: 200},
		{name: "wrong ID", status: 200, mutate: func(u map[string]any) { u["id"] = uuid.NewString() }},
		{name: "wrong username", status: 200, mutate: func(u map[string]any) { u["username"] = "foreign" }},
		{name: "disabled", status: 200, mutate: func(u map[string]any) { u["enabled"] = false }},
		{name: "wrong tenant", status: 200, mutate: func(u map[string]any) { u["attributes"] = map[string]any{"tenant_id": []string{uuid.NewString()}} }},
		{name: "extra tenant", status: 200, mutate: func(u map[string]any) {
			u["attributes"].(map[string]any)["tenant_ids"] = []string{c.TenantID.String(), uuid.NewString()}
		}},
		{name: "missing OTP enrollment", status: 200, mutate: func(u map[string]any) { delete(u, "requiredActions") }},
		{name: "missing subject", status: 404},
		{name: "malformed response", status: 200, invalidBody: "{"},
		{name: "oversized response", status: 200, invalidBody: strings.Repeat(" ", 64<<10) + "{}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			var imported map[string]any
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.URL.Path != "/realms/master/protocol/openid-connect/token" && r.Header.Get("Authorization") != "Bearer bootstrap-token" {
					t.Error("administrative request missing verified bootstrap token")
				}
				switch r.Method + " " + r.URL.Path {
				case "POST /realms/master/protocol/openid-connect/token":
					json.NewEncoder(w).Encode(map[string]string{"access_token": "bootstrap-token"})
				case "GET /admin/realms/ops":
					w.WriteHeader(404)
				case "POST /admin/realms":
					var realm map[string]any
					if json.NewDecoder(r.Body).Decode(&realm) != nil {
						t.Error("invalid realm import")
						w.WriteHeader(400)
						return
					}
					users, ok := realm["users"].([]any)
					if !ok || len(users) != 1 {
						t.Error("initial subject must be imported with the realm")
						w.WriteHeader(400)
						return
					}
					imported = users[0].(map[string]any)
					if imported["id"] != c.SubjectID || imported["username"] != c.Username || imported["requiredActions"].([]any)[0] != "CONFIGURE_TOTP" || imported["credentials"].([]any)[0].(map[string]any)["value"] != private.SubjectPassword {
						t.Error("imported identity, credentials or OTP requirements changed")
					}
					delete(realm, "users")
					withoutSubject := c
					withoutSubject.SubjectID = ""
					base, err := BuildOIDCRealm(withoutSubject, private)
					actual, _ := json.Marshal(realm)
					if err != nil || !bytes.Equal(base, actual) {
						t.Error("preallocation changed frozen realm security semantics")
					}
					w.WriteHeader(201)
				case "GET /admin/realms/ops/users/" + c.SubjectID:
					if test.mutate != nil {
						test.mutate(imported)
					}
					w.WriteHeader(test.status)
					if test.invalidBody != "" {
						w.Write([]byte(test.invalidBody))
					} else {
						json.NewEncoder(w).Encode(imported)
					}
				default:
					t.Error("unexpected bootstrap endpoint", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			id, err := BootstrapOIDC(t.Context(), server.Client(), server.URL, c, private)
			if test.name == "verified" {
				if err != nil || id != c.SubjectID {
					t.Fatalf("preallocated subject not verified: id=%q err=%v", id, err)
				}
			} else if err == nil || id != "" || !strings.Contains(err.Error(), "BOOTSTRAP_PARTIAL_REALM_RETAINED") {
				t.Fatalf("unverified import accepted or partial state hidden: id=%q err=%v", id, err)
			}
			if len(calls) != 4 || calls[3] != "GET /admin/realms/ops/users/"+c.SubjectID {
				t.Fatalf("subject creation bypassed realm import/readback: %v", calls)
			}
		})
	}
}

func TestOIDCBootstrapPreservesRandomSubjectAndRefusesExistingRealm(t *testing.T) {
	c := OIDCInput{SchemaVersion: 1, CallbackURL: "https://current.example.invalid/api/v1/auth/callback", TenantID: uuid.New(), Username: "explicit-admin", Email: "admin@example.invalid", FirstName: "Explicit", LastName: "Admin"}
	private := OIDCPrivateInput{BootstrapUsername: "bootstrap", BootstrapPassword: "separate-private-bootstrap-123456", ClientSecret: "separate-private-client-123456", SubjectPassword: "separate-private-subject-123456"}
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "random subject", true: "existing realm"}[existing], func(t *testing.T) {
			calls := 0
			expected := uuid.NewString()
			input := c
			if existing {
				input.SubjectID = expected
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch r.Method + " " + r.URL.Path {
				case "POST /realms/master/protocol/openid-connect/token":
					json.NewEncoder(w).Encode(map[string]string{"access_token": "bootstrap-token"})
				case "GET /admin/realms/ops":
					if !existing {
						w.WriteHeader(404)
					}
				case "POST /admin/realms":
					var realm map[string]any
					if json.NewDecoder(r.Body).Decode(&realm) != nil || realm["users"] != nil {
						t.Error("random subject flow changed to realm user import")
					}
					w.WriteHeader(201)
				case "POST /admin/realms/ops/users":
					var user map[string]any
					if json.NewDecoder(r.Body).Decode(&user) != nil || user["id"] != nil || user["username"] != c.Username {
						t.Error("random subject flow supplied an ID or lost the named user")
					}
					w.Header().Set("Location", "https://"+r.Host+"/admin/realms/ops/users/"+expected)
					w.WriteHeader(201)
				default:
					t.Error("unexpected bootstrap endpoint", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			id, err := BootstrapOIDC(t.Context(), server.Client(), server.URL, input, private)
			if existing {
				if err == nil || id != "" || calls != 2 || !strings.Contains(err.Error(), "refuses an existing") {
					t.Fatalf("existing realm reached mutations: calls=%d err=%v", calls, err)
				}
			} else if err != nil || id != expected || calls != 4 {
				t.Fatalf("random subject flow changed: id=%q calls=%d err=%v", id, calls, err)
			}
		})
	}
}
