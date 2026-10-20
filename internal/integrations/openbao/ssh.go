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

const commandTransitPolicy = `
path "transit/encrypt/evidence-archive" { capabilities = ["update"] }
path "transit/decrypt/evidence-archive" { capabilities = ["update"] }`

const commandSSHPolicy = `
path "ssh/sign/ops-sp07-user" { capabilities = ["update"] }
path "ssh/sign/ops-sp07-root" { capabilities = ["update"] }`

// commandPolicyScopes lists the exact ACL variants a runtime workload policy may
// hold on top of the least-privilege baseline: the Transit extension required by
// Kubernetes execution and, for the API workload only, the explicit SSH
// extension. Re-running "configure" after either extension must stay idempotent
// instead of reporting drift against the bare baseline.
func commandPolicyScopes(name, policy string) []string {
	scopes := []string{strings.TrimSpace(policy)}
	switch name {
	case "ops-api-workload":
		scopes = append(scopes, strings.TrimSpace(policy+commandTransitPolicy),
			strings.TrimSpace(policy+commandTransitPolicy+commandSSHPolicy))
	case "ops-worker-workload":
		scopes = append(scopes, strings.TrimSpace(policy+commandTransitPolicy))
	}
	return scopes
}

func allowsCommandPolicy(name, current, policy string) bool {
	for _, scope := range commandPolicyScopes(name, policy) {
		if strings.TrimSpace(current) == scope {
			return true
		}
	}
	return false
}

// Kubernetes execution needs Transit without granting SSH signing authority.
func (c *Client) ConfigureCommandExecutionTransit(ctx context.Context) error {
	for _, name := range []string{"ops-api-workload", "ops-worker-workload"} {
		if err := c.extendCommandPolicy(ctx, name, runtimePolicies[name]+commandTransitPolicy); err != nil {
			return err
		}
	}
	return nil
}

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
	policy := runtimePolicies["ops-api-workload"] + commandTransitPolicy + commandSSHPolicy
	if err := c.extendCommandPolicy(ctx, "ops-api-workload", policy); err != nil {
		return err
	}
	workerPolicy := runtimePolicies["ops-worker-workload"] + commandTransitPolicy
	return c.extendCommandPolicy(ctx, "ops-worker-workload", workerPolicy)
}

func (c *Client) extendCommandPolicy(ctx context.Context, name, desired string) error {
	var current dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/sys/policies/acl/"+name, nil, &current); err != nil {
		return err
	}
	raw, ok := current.Data["policy"].(string)
	raw = strings.TrimSpace(raw)
	if !ok {
		return fmt.Errorf("command policy differs from expected existing scope")
	}
	// A Transit-only rerun must preserve an explicitly initialized SSH scope.
	if raw == strings.TrimSpace(desired) || name == "ops-api-workload" && raw == strings.TrimSpace(runtimePolicies[name]+commandTransitPolicy+commandSSHPolicy) {
		return nil
	}
	if raw != strings.TrimSpace(runtimePolicies[name]) && raw != strings.TrimSpace(runtimePolicies[name]+commandTransitPolicy) {
		return fmt.Errorf("command policy differs from expected existing scope")
	}
	if err := c.request(ctx, http.MethodPut, "/v1/sys/policies/acl/"+name, map[string]any{"policy": desired}, nil); err != nil {
		return err
	}
	return c.verifyObject(ctx, "/v1/sys/policies/acl/"+name, map[string]any{"policy": desired})
}
