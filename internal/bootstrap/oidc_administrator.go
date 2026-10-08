package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/google/uuid"
)

// OIDCAdministrator identifies one explicitly enrolled subject. The operation
// grants only the existing Keycloak ops realm role, never master administration
// or a platform workload scope.
type OIDCAdministrator struct {
	SchemaVersion int       `json:"schemaVersion"`
	TenantID      uuid.UUID `json:"tenantId"`
	Subject       uuid.UUID `json:"subject"`
	Username      string    `json:"username"`
}

func (c OIDCAdministrator) Validate() error {
	if c.SchemaVersion != 1 || c.TenantID == uuid.Nil || c.Subject == uuid.Nil || len(c.Username) < 3 || len(c.Username) > 200 {
		return errors.New("explicit enrolled tenant/subject/username required")
	}
	return nil
}

type oidcAdministration struct {
	client *http.Client
	base   string
	token  string
}

var ErrOIDCTemporaryRejected = errors.New("OIDC temporary administrator rejected")

func validateOIDCAdministration(client *http.Client, base string) error {
	u, err := url.Parse(base)
	if client == nil || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("fixed trusted HTTPS OIDC administrative client required")
	}
	return nil
}

func (a oidcAdministration) call(ctx context.Context, method, path string, body any, target any) (int, error) {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return 0, errors.New("invalid fixed OIDC administrative input")
		}
	}
	request, _ := http.NewRequestWithContext(ctx, method, a.base+path, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+a.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return 0, errors.New("fixed OIDC administration request unavailable")
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return response.StatusCode, errors.New("OIDC administration response exceeded bounds")
	}
	if target != nil && response.StatusCode == http.StatusOK && json.Unmarshal(raw, target) != nil {
		return response.StatusCode, errors.New("OIDC administration response invalid")
	}
	return response.StatusCode, nil
}

func bootstrapAdministration(ctx context.Context, client *http.Client, base string, private OIDCPrivateInput) (oidcAdministration, error) {
	if err := validateOIDCAdministration(client, base); err != nil {
		return oidcAdministration{}, err
	}
	form := url.Values{"client_id": {"admin-cli"}, "grant_type": {"password"}, "username": {private.BootstrapUsername}, "password": {private.BootstrapPassword}}
	request, _ := http.NewRequestWithContext(ctx, "POST", base+"/realms/master/protocol/openid-connect/token", bytes.NewBufferString(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return oidcAdministration{}, errors.New("OIDC temporary administrator unavailable")
	}
	defer response.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err == nil && (response.StatusCode == 400 || response.StatusCode == 401) && len(raw) <= 64<<10 {
		var refusal struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &refusal) == nil && refusal.Error == "invalid_grant" {
			return oidcAdministration{}, ErrOIDCTemporaryRejected
		}
	}
	if response.StatusCode != 200 || err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &token) != nil || token.AccessToken == "" {
		return oidcAdministration{}, errors.New("OIDC temporary administrator authentication not confirmed")
	}
	return oidcAdministration{client, base, token.AccessToken}, nil
}

func enrolledOIDCAdministrator(ctx context.Context, a oidcAdministration, c OIDCAdministrator) error {
	var user struct {
		ID         string              `json:"id"`
		Username   string              `json:"username"`
		Enabled    bool                `json:"enabled"`
		TOTP       bool                `json:"totp"`
		Attributes map[string][]string `json:"attributes"`
	}
	status, err := a.call(ctx, "GET", "/admin/realms/ops/users/"+c.Subject.String(), nil, &user)
	if err != nil || status != 200 || user.ID != c.Subject.String() || user.Username != c.Username || !user.Enabled || !user.TOTP {
		return errors.New("named OIDC administrator must match an enabled, OTP-enrolled subject")
	}
	for _, id := range append(user.Attributes["tenant_ids"], user.Attributes["tenant_id"]...) {
		if id == c.TenantID.String() {
			return nil
		}
	}
	return errors.New("named OIDC administrator tenant membership differs")
}

func PrepareOIDCAdministrator(ctx context.Context, client *http.Client, base string, c OIDCAdministrator, private OIDCPrivateInput) error {
	if c.Validate() != nil {
		return c.Validate()
	}
	a, err := bootstrapAdministration(ctx, client, base, private)
	if err != nil {
		return err
	}
	if err = enrolledOIDCAdministrator(ctx, a, c); err != nil {
		return err
	}
	var clients []struct {
		ID       string `json:"id"`
		ClientID string `json:"clientId"`
	}
	status, err := a.call(ctx, "GET", "/admin/realms/ops/clients?clientId=realm-management", nil, &clients)
	if err != nil || status != 200 || len(clients) != 1 || clients[0].ClientID != "realm-management" {
		return errors.New("locked ops realm-management client unavailable")
	}
	id, err := uuid.Parse(clients[0].ID)
	if err != nil {
		return errors.New("realm-management identity invalid")
	}
	var role map[string]any
	status, err = a.call(ctx, "GET", "/admin/realms/ops/clients/"+id.String()+"/roles/realm-admin", nil, &role)
	roleID, idErr := uuid.Parse(textString(role, "id"))
	if err != nil || status != 200 || idErr != nil || roleID == uuid.Nil || textString(role, "name") != "realm-admin" || role["clientRole"] != true || textString(role, "containerId") != id.String() {
		return errors.New("locked realm administrator role unavailable")
	}
	status, err = a.call(ctx, "POST", "/admin/realms/ops/users/"+c.Subject.String()+"/role-mappings/clients/"+id.String(), []any{role}, nil)
	if err != nil || status != 204 {
		return errors.New("named realm administrator assignment rejected")
	}
	return nil
}

func textString(m map[string]any, key string) string { value, _ := m[key].(string); return value }

// VerifyOIDCAdministrator performs independent positive and negative access
// controls using a newly authenticated named bearer. Token identity/LoA checks
// are completed by the CLI before this operation is called.
func VerifyOIDCAdministrator(ctx context.Context, client *http.Client, base string, c OIDCAdministrator, bearer string) error {
	if c.Validate() != nil {
		return c.Validate()
	}
	if err := validateOIDCAdministration(client, base); err != nil {
		return err
	}
	a := oidcAdministration{client, base, bearer}
	if err := enrolledOIDCAdministrator(ctx, a, c); err != nil {
		return err
	}
	status, err := a.call(ctx, "GET", "/admin/realms/ops", nil, nil)
	if err != nil || status != 200 {
		return errors.New("named administrator positive ops realm control rejected")
	}
	status, err = a.call(ctx, "GET", "/admin/realms/master", nil, nil)
	if err != nil || status != 403 {
		return errors.New("named administrator must remain outside master administration")
	}
	return nil
}

func RetireOIDCAdministrator(ctx context.Context, client *http.Client, base string, c OIDCAdministrator, private OIDCPrivateInput, bearer string, rotateSecret func() error) (string, error) {
	// Do not mutate the temporary identity until the replacement's real current
	// authority succeeds. This is also a positive control for refusal tests.
	if err := VerifyOIDCAdministrator(ctx, client, base, c, bearer); err != nil {
		return "", err
	}
	a, err := bootstrapAdministration(ctx, client, base, private)
	if err != nil {
		return "", err
	}
	var users []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	status, err := a.call(ctx, "GET", "/admin/realms/master/users?exact=true&username="+url.QueryEscape(private.BootstrapUsername), nil, &users)
	if err != nil || status != 200 || len(users) != 1 || users[0].Username != private.BootstrapUsername {
		return "", errors.New("exact temporary administrator identity unavailable")
	}
	id, err := uuid.Parse(users[0].ID)
	if err != nil || id == c.Subject || rotateSecret == nil {
		return "", errors.New("temporary administrator identity/rotation invalid")
	}
	if err := rotateSecret(); err != nil {
		return "", err
	}
	status, err = a.call(ctx, "DELETE", "/admin/realms/master/users/"+id.String(), nil, nil)
	if err != nil || status != 204 {
		return "", errors.New("BOOTSTRAP_PARTIAL_SECRET_ROTATED: temporary administrator removal not confirmed; named administrator verified")
	}
	status, err = a.call(ctx, "GET", "/admin/realms/ops", nil, nil)
	if err != nil || (status != 401 && status != 403) {
		return id.String(), errors.New("BOOTSTRAP_PARTIAL_PRINCIPAL_REMOVED: old administrative token rejection not confirmed")
	}
	if _, err = bootstrapAdministration(ctx, client, base, private); !errors.Is(err, ErrOIDCTemporaryRejected) {
		return id.String(), errors.New("BOOTSTRAP_PARTIAL_PRINCIPAL_REMOVED: old credential rejection not confirmed")
	}
	return id.String(), nil
}
