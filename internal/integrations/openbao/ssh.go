package openbao

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var sshPrincipal = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (c *Client) SignSSHCertificate(ctx context.Context, role, publicKey, principal string, ttl time.Duration) (string, error) {
	if !transitNamePattern.MatchString(role) || !sshPrincipal.MatchString(principal) || ttl <= 0 || ttl > 15*time.Minute || len(publicKey) > 16384 || !strings.HasPrefix(publicKey, "ssh-ed25519 ") || strings.ContainsAny(publicKey, "\r\n") {
		return "", fmt.Errorf("invalid SSH certificate request")
	}
	var out dataResponse
	if err := c.request(ctx, http.MethodPost, "/v1/ssh/sign/"+role, map[string]any{"public_key": publicKey, "valid_principals": principal, "cert_type": "user", "ttl": fmt.Sprintf("%ds", int(ttl.Seconds()))}, &out); err != nil {
		return "", fmt.Errorf("SSH_CA_SIGN_UNAVAILABLE")
	}
	cert, ok := out.Data["signed_key"].(string)
	if !ok || !strings.HasPrefix(cert, "ssh-ed25519-cert-v01@openssh.com ") {
		return "", fmt.Errorf("SSH_CA_CERTIFICATE_INVALID")
	}
	return cert, nil
}
func (p *ProjectedTransit) SignSSHCertificate(ctx context.Context, role, key, principal string, ttl time.Duration) (string, error) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	if err := p.s.authenticate(ctx); err != nil {
		return "", err
	}
	return p.s.client.SignSSHCertificate(ctx, role, key, principal, ttl)
}

// ConfigureCommandExecutionSSH creates only exact user/root certificate roles.
// It is explicitly invoked by the SP07 initialization flag, independently of
// investigation permissions. The old bootstrap role remains unchanged.
func (c *Client) ConfigureCommandExecutionSSH(ctx context.Context, ordinary string) error {
	if !sshPrincipal.MatchString(ordinary) || ordinary == "root" || c.token == "" {
		return fmt.Errorf("invalid execution SSH initialization")
	}
	for role, principal := range map[string]string{"ops-sp07-user": ordinary, "ops-sp07-root": "root"} {
		expected := map[string]any{"key_type": "ca", "allow_user_certificates": true, "allow_host_certificates": false, "allowed_users": principal, "default_user": principal, "ttl": "15m", "max_ttl": "15m", "default_extensions": map[string]any{}, "allowed_extensions": ""}
		if err := c.ensureObject(ctx, "/v1/ssh/roles/"+role, expected); err != nil {
			return err
		}
	}
	// Projected API authentication uses the same exact policy already provisioned
	// for its PKI/signing identity. No signing rights are added to investigators.
	policy := runtimePolicies["ops-api-workload"] + `
path "transit/encrypt/evidence-archive" { capabilities = ["update"] }
path "transit/decrypt/evidence-archive" { capabilities = ["update"] }
path "ssh/sign/ops-sp07-user" { capabilities = ["update"] }
path "ssh/sign/ops-sp07-root" { capabilities = ["update"] }`
	if err := c.extendCommandPolicy(ctx, "ops-api-workload", policy); err != nil {
		return err
	}
	workerPolicy := runtimePolicies["ops-worker-workload"] + `
path "transit/encrypt/evidence-archive" { capabilities = ["update"] }
path "transit/decrypt/evidence-archive" { capabilities = ["update"] }`
	return c.extendCommandPolicy(ctx, "ops-worker-workload", workerPolicy)
}

func (c *Client) extendCommandPolicy(ctx context.Context, name, desired string) error {
	var current dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/sys/policies/acl/"+name, nil, &current); err != nil {
		return err
	}
	raw, ok := current.Data["policy"].(string)
	if !ok || (strings.TrimSpace(raw) != strings.TrimSpace(runtimePolicies[name]) && strings.TrimSpace(raw) != strings.TrimSpace(desired)) {
		return fmt.Errorf("command policy differs from expected existing scope")
	}
	if strings.TrimSpace(raw) == strings.TrimSpace(desired) {
		return nil
	}
	if err := c.request(ctx, http.MethodPut, "/v1/sys/policies/acl/"+name, map[string]any{"policy": desired}, nil); err != nil {
		return err
	}
	return c.verifyObject(ctx, "/v1/sys/policies/acl/"+name, map[string]any{"policy": desired})
}
