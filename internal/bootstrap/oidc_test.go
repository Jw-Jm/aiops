package bootstrap

import (
	"bytes"
	"encoding/json"
	"os"
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
