package bootstrap

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

//go:embed assets/realm-ops.json
var RealmTemplate []byte

type OIDCInput struct {
	SchemaVersion int       `json:"schemaVersion"`
	CallbackURL   string    `json:"callbackURL"`
	TenantID      uuid.UUID `json:"tenantId"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	FirstName     string    `json:"firstName"`
	LastName      string    `json:"lastName"`
}
type OIDCPrivateInput struct {
	BootstrapUsername string `json:"bootstrapUsername"`
	BootstrapPassword string `json:"bootstrapPassword"`
	ClientSecret      string `json:"clientSecret"`
	SubjectPassword   string `json:"subjectPassword"`
}

func (c OIDCInput) Validate(private OIDCPrivateInput) error {
	u, err := url.Parse(c.CallbackURL)
	if c.SchemaVersion != 1 || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/v1/auth/callback" || strings.Contains(u.Host, "*") || c.TenantID == uuid.Nil || len(c.Username) < 3 || len(c.Username) > 200 || strings.TrimSpace(c.FirstName) == "" || strings.TrimSpace(c.LastName) == "" || !strings.Contains(c.Email, "@") {
		return errors.New("explicit HTTPS callback, tenant and named initial subject required")
	}
	if len(private.BootstrapUsername) < 1 || len(private.BootstrapPassword) < 24 || len(private.ClientSecret) < 24 || len(private.SubjectPassword) < 24 || private.BootstrapPassword == private.SubjectPassword {
		return errors.New("separate explicit bootstrap, client and subject credentials required")
	}
	return nil
}
func BuildOIDCRealm(c OIDCInput, private OIDCPrivateInput) ([]byte, error) {
	if err := c.Validate(private); err != nil {
		return nil, err
	}
	var realm map[string]any
	if json.Unmarshal(RealmTemplate, &realm) != nil {
		return nil, errors.New("locked realm template invalid")
	}
	callback, _ := url.Parse(c.CallbackURL)
	for _, v := range realm["clients"].([]any) {
		client := v.(map[string]any)
		if client["clientId"] == "ops-web" {
			client["redirectUris"] = []string{c.CallbackURL}
			client["webOrigins"] = []string{callback.Scheme + "://" + callback.Host}
			client["secret"] = private.ClientSecret
		}
	}
	return json.Marshal(realm)
}

// BootstrapOIDC uses only fixed Keycloak administrative endpoints to initialize
// a new realm and named subject. Enrollment and LoA-2 login remain real browser
// flows. No execution API or arbitrary URL fetching Tool is exposed.
func BootstrapOIDC(ctx context.Context, client *http.Client, base string, c OIDCInput, private OIDCPrivateInput) (string, error) {
	realm, err := BuildOIDCRealm(c, private)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || client == nil {
		return "", errors.New("trusted Keycloak HTTPS client required")
	}
	base = strings.TrimSuffix(base, "/")
	form := url.Values{"client_id": {"admin-cli"}, "grant_type": {"password"}, "username": {private.BootstrapUsername}, "password": {private.BootstrapPassword}}
	request, _ := http.NewRequestWithContext(ctx, "POST", base+"/realms/master/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("Keycloak bootstrap authentication unavailable")
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	response.Body.Close()
	if response.StatusCode != 200 || readErr != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &token) != nil || token.AccessToken == "" {
		return "", errors.New("Keycloak bootstrap authentication rejected")
	}
	call := func(method, path string, body []byte) (int, string, error) {
		request, _ := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token.AccessToken)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return 0, "", errors.New("Keycloak bootstrap request failed")
		}
		defer response.Body.Close()
		// Administrative error bodies may contain credentials and are not logged.
		return response.StatusCode, response.Header.Get("Location"), nil
	}
	status, _, err := call("GET", "/admin/realms/ops", nil)
	if err != nil || status != 404 {
		return "", errors.New("OIDC bootstrap refuses an existing or inaccessible ops realm")
	}
	status, _, err = call("POST", "/admin/realms", realm)
	if err != nil || status != 201 {
		return "", errors.New("OIDC realm initialization rejected")
	}
	subject, _ := json.Marshal(map[string]any{"username": c.Username, "email": c.Email, "firstName": c.FirstName, "lastName": c.LastName, "enabled": true, "emailVerified": false, "attributes": map[string]any{"tenant_id": []string{c.TenantID.String()}, "tenant_ids": []string{c.TenantID.String()}}, "requiredActions": []string{"CONFIGURE_TOTP"}, "credentials": []any{map[string]any{"type": "password", "value": private.SubjectPassword, "temporary": false}}})
	status, location, err := call("POST", "/admin/realms/ops/users", subject)
	if err != nil || status != 201 {
		return "", errors.New("BOOTSTRAP_PARTIAL_REALM_RETAINED: initial OIDC subject creation rejected")
	}
	created, err := url.Parse(location)
	if err != nil || created.Scheme != u.Scheme || created.Host != u.Host || created.RawQuery != "" || created.Fragment != "" || !strings.HasPrefix(created.Path, "/admin/realms/ops/users/") {
		return "", errors.New("BOOTSTRAP_PARTIAL_REALM_RETAINED: subject identity response invalid")
	}
	id := strings.TrimPrefix(created.Path, "/admin/realms/ops/users/")
	if _, err := uuid.Parse(id); err != nil {
		return "", errors.New("BOOTSTRAP_PARTIAL_REALM_RETAINED: subject identity missing")
	}
	return id, nil
}
