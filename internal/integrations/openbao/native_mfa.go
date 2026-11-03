package openbao

import (
	"context"
	"fmt"
	"net/http"
)

func (c *Client) workloadPolicies(role string) []any {
	policies := []any{role}
	if role == "ops-api-workload" && c.nativeMFAKey != "" {
		policies = append(policies, "ops-native-mfa-"+c.nativeMFAKey)
	}
	return policies
}

// Provision no key or key rotation implicitly: the operator selects an existing
// nonexportable MFA key, independently of the evidence and signing keys.
func (c *Client) configureNativeMFA(ctx context.Context) error {
	if c.nativeMFAKey == "" {
		return nil
	}
	policy, err := c.nativeMFAPolicy(ctx)
	if err != nil {
		return err
	}
	return c.ensurePolicy(ctx, "ops-native-mfa-"+c.nativeMFAKey, policy)
}

func (c *Client) nativeMFAPolicy(ctx context.Context) (string, error) {
	var key dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/transit/keys/"+c.nativeMFAKey, nil, &key); err != nil {
		return "", asDriftIfNotFound(err, "the configured native MFA key does not exist")
	}
	if key.Data["type"] != "aes256-gcm96" || key.Data["exportable"] != false || key.Data["allow_plaintext_backup"] != false {
		return "", fmt.Errorf("%w: native MFA key must be nonexportable aes256-gcm96 without plaintext backup", ErrConfigurationDrift)
	}
	return fmt.Sprintf("path %q { capabilities = [\"update\"] }\npath %q { capabilities = [\"update\"] }",
		"transit/encrypt/"+c.nativeMFAKey, "transit/decrypt/"+c.nativeMFAKey), nil
}

func (c *Client) verifyNativeMFA(ctx context.Context) error {
	if c.nativeMFAKey == "" {
		return nil
	}
	policy, err := c.nativeMFAPolicy(ctx)
	if err != nil {
		return err
	}
	var response dataResponse
	name := "ops-native-mfa-" + c.nativeMFAKey
	if err := c.request(ctx, http.MethodGet, "/v1/sys/policies/acl/"+name, nil, &response); err != nil {
		return asDriftIfNotFound(err, "native MFA ACL policy is missing")
	}
	if current, _ := response.Data["policy"].(string); !allowsCommandPolicy(name, current, policy) {
		return fmt.Errorf("%w: native MFA ACL policy has drifted", ErrConfigurationDrift)
	}
	return nil
}
