package openbao

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const (
	initKeyShares    = 3
	initKeyThreshold = 2
	pkiMaxLeaseTTL   = 8760 * time.Hour
	transitKeyName   = "evidence-archive"
	transitKeyType   = "aes256-gcm96"
	pkiRoleName      = "platform-client"
	sshRoleName      = "ops-command-runner"
)

type RecoveryMaterial struct {
	Version   string   `json:"version"`
	Shares    []string `json:"shares"`
	RootToken string   `json:"rootToken"`
}

type initResponse struct {
	Shares    []string `json:"keys_base64"`
	RootToken string   `json:"root_token"`
}

type dataResponse struct {
	Data map[string]any `json:"data"`
}

func (c *Client) Initialize(ctx context.Context, recoveryFile string) error {
	if err := validateExternalPath(recoveryFile, c.repositoryRoot, c.bundleDirectory); err != nil {
		return fmt.Errorf("recovery material path: %w", err)
	}
	if info, err := os.Lstat(recoveryFile); err == nil {
		if info.Size() > 0 {
			return errors.New("recovery material file already exists; refusing to overwrite it")
		}
		// A zero-byte file is the residue of an initialization whose response was
		// lost. It is only safe to discard once the server itself reports an
		// uninitialized instance; otherwise the shares are gone for good.
		seal, sealErr := c.readSealStatus(ctx)
		if sealErr != nil {
			return fmt.Errorf("inspect OpenBao state before discarding an empty recovery file: %w", sealErr)
		}
		if seal.Initialized {
			return fmt.Errorf("%w: an initialized instance has no recovery material; reset the dedicated volume before initializing again (%s)", ErrRecoveryUnavailable, recoveryFile)
		}
		if err := os.Remove(recoveryFile); err != nil {
			return fmt.Errorf("replace the empty recovery material file: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect recovery material path: %w", err)
	}
	seal, err := c.readSealStatus(ctx)
	if err != nil {
		return err
	}
	if seal.Initialized {
		return ErrAlreadyInitialized
	}

	file, err := os.OpenFile(recoveryFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create one-time recovery material file: %w", err)
	}
	fileOpen := true
	defer func() {
		if fileOpen {
			_ = file.Close()
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("set recovery material permissions: %w", err)
	}

	var response initResponse
	if err := c.request(ctx, http.MethodPut, "/v1/sys/init", map[string]any{
		"secret_shares": initKeyShares, "secret_threshold": initKeyThreshold,
	}, &response); err != nil {
		var statusErr *apiError
		if errors.As(err, &statusErr) && statusErr.statusCode < http.StatusInternalServerError {
			_ = file.Close()
			fileOpen = false
			_ = os.Remove(recoveryFile)
			if statusErr.statusCode == http.StatusBadRequest {
				return ErrAlreadyInitialized
			}
		}
		// The initialization request failed after the server may already have
		// committed state. Never leave an empty recovery file behind: decide from
		// the server itself, so a retry is either clean or clearly impossible.
		_ = os.Remove(recoveryFile)
		if state, stateErr := c.readSealStatus(ctx); stateErr == nil && state.Initialized {
			return fmt.Errorf("%w: OpenBao committed initialization but no recovery material was received (%s); reset the dedicated volume before initializing again", ErrRecoveryUnavailable, recoveryFile)
		}
		return fmt.Errorf("OPENBAO_INIT_OUTCOME_UNCERTAIN: no recovery material was received and the instance still reports uninitialized; retrying initialization is safe")
	}
	if len(response.Shares) != initKeyShares || response.RootToken == "" {
		return errors.New("OPENBAO_INIT_RESPONSE_INVALID: initialization response did not contain the expected recovery material")
	}
	material := RecoveryMaterial{Version: "1", Shares: response.Shares, RootToken: response.RootToken}
	encoded, err := json.Marshal(material)
	if err != nil {
		return fmt.Errorf("encode recovery material: %w", err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return errors.New("OPENBAO_RECOVERY_WRITE_FAILED: recovery material could not be durably stored; preserve the reserved file and stop")
	}
	if err := file.Sync(); err != nil {
		return errors.New("OPENBAO_RECOVERY_SYNC_FAILED: recovery material could not be durably stored; preserve the reserved file and stop")
	}
	if err := file.Close(); err != nil {
		fileOpen = false
		return errors.New("OPENBAO_RECOVERY_CLOSE_FAILED: recovery material file could not be closed cleanly")
	}
	fileOpen = false
	return nil
}

func readRecoveryMaterial(path string) (RecoveryMaterial, error) {
	file, err := os.Open(path)
	if err != nil {
		return RecoveryMaterial{}, fmt.Errorf("open recovery material: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return RecoveryMaterial{}, fmt.Errorf("stat recovery material: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return RecoveryMaterial{}, errors.New("recovery material permissions must be exactly 0600")
	}
	contents, err := ioReadLimited(file, 1<<20)
	if err != nil {
		return RecoveryMaterial{}, err
	}
	var material RecoveryMaterial
	if err := json.Unmarshal(contents, &material); err != nil {
		return RecoveryMaterial{}, errors.New("recovery material file is invalid")
	}
	if material.Version != "1" || len(material.Shares) != initKeyShares || material.RootToken == "" {
		return RecoveryMaterial{}, errors.New("recovery material file is incomplete")
	}
	for _, share := range material.Shares {
		if strings.TrimSpace(share) == "" {
			return RecoveryMaterial{}, errors.New("recovery material file contains an empty key share")
		}
	}
	return material, nil
}

func ReadExternalRecoveryMaterial(path, repositoryRoot, bundleDirectory string) (RecoveryMaterial, error) {
	if err := validateExternalPath(path, repositoryRoot, bundleDirectory); err != nil {
		return RecoveryMaterial{}, fmt.Errorf("recovery material path: %w", err)
	}
	return readRecoveryMaterial(path)
}

func (c *Client) Unseal(ctx context.Context, key string) (Status, error) {
	if strings.TrimSpace(key) == "" {
		return Status{}, errors.New("Shamir key share is required")
	}
	seal, err := c.readSealStatus(ctx)
	if err != nil {
		return Status{}, err
	}
	if !seal.Initialized {
		return seal.status(), errors.New("OPENBAO_UNINITIALIZED")
	}
	if !seal.Sealed {
		return seal.status(), nil
	}
	var response sealResponse
	if err := c.request(ctx, http.MethodPost, "/v1/sys/unseal", map[string]string{"key": key}, &response); err != nil {
		return Status{}, err
	}
	return response.status(), nil
}

func (c *Client) Configure(ctx context.Context) error {
	seal, err := c.readSealStatus(ctx)
	if err != nil {
		return err
	}
	if !seal.Initialized || seal.Sealed {
		return ErrSealed
	}
	if c.expectedVersion != "" && seal.Version != "" && c.expectedVersion != seal.Version {
		return fmt.Errorf("%w: OPENBAO_VERSION_MISMATCH", ErrConfigurationDrift)
	}
	if c.token == "" {
		return errors.New("OpenBao root token from the external recovery file is required for configure")
	}
	for name, engineType := range map[string]string{"transit": "transit", "pki": "pki", "ssh": "ssh"} {
		if err := c.ensureMount(ctx, name, engineType); err != nil {
			return err
		}
	}
	if err := c.ensurePKIMountTTL(ctx); err != nil {
		return err
	}
	if err := c.ensureAuthMethod(ctx); err != nil {
		return err
	}
	if err := c.ensureKubernetesConfig(ctx); err != nil {
		return err
	}
	if err := c.ensureTransitKey(ctx); err != nil {
		return err
	}
	if err := c.ensureAuditSigningKey(ctx); err != nil {
		return err
	}
	if err := c.ensurePKIRoot(ctx); err != nil {
		return err
	}
	if err := c.ensurePKIRole(ctx); err != nil {
		return err
	}
	for _, serviceAccount := range workloadServiceAccounts {
		if err := c.ensureWorkloadPKIRole(ctx, serviceAccount); err != nil {
			return err
		}
	}
	if err := c.ensureSSHCA(ctx); err != nil {
		return err
	}
	if err := c.ensureSSHRole(ctx); err != nil {
		return err
	}
	for name, policy := range runtimePolicies {
		if err := c.ensurePolicy(ctx, name, policy); err != nil {
			return err
		}
	}
	for name, serviceAccount := range map[string]string{"ops-api": "ops-api", "ops-worker": "ops-worker", "ops-command-runner": "ops-command-runner"} {
		if err := c.ensureKubernetesRole(ctx, name, serviceAccount); err != nil {
			return err
		}
	}
	for _, serviceAccount := range workloadServiceAccounts {
		role, err := WorkloadAuthRoleName(serviceAccount)
		if err != nil {
			return err
		}
		if err := c.ensureKubernetesWorkloadRole(ctx, role, serviceAccount); err != nil {
			return err
		}
	}
	return c.verifyConfiguration(ctx)
}

// ConfigureWorkloadPKI provisions short-lived, exact-SAN workload roles. It can
// run independently in an isolated OpenBao instance where Kubernetes auth is
// unavailable, while production Configure still performs the full setup.
func (c *Client) ConfigureWorkloadPKI(ctx context.Context) error {
	if c.token == "" {
		return errors.New("OpenBao operator token is required to configure workload PKI")
	}
	seal, err := c.readSealStatus(ctx)
	if err != nil {
		return err
	}
	if !seal.Initialized || seal.Sealed {
		return ErrSealed
	}
	if err := c.ensureMount(ctx, "pki", "pki"); err != nil {
		return err
	}
	if err := c.ensurePKIMountTTL(ctx); err != nil {
		return err
	}
	if err := c.ensurePKIRoot(ctx); err != nil {
		return err
	}
	for _, serviceAccount := range workloadServiceAccounts {
		if err := c.ensureWorkloadPKIRole(ctx, serviceAccount); err != nil {
			return err
		}
	}
	for _, serviceAccount := range workloadServiceAccounts {
		role, expected, err := c.workloadPKIRoleConfig(serviceAccount)
		if err != nil {
			return err
		}
		if err := c.verifyObject(ctx, "/v1/pki/roles/"+role, expected); err != nil {
			return err
		}
	}
	return nil
}

// ConfigureWorkloadAuthentication installs dedicated Kubernetes auth roles
// and least-privilege signing policies without configuring a cluster endpoint.
// The full Configure operation also applies these roles after Kubernetes auth
// has been configured for the OpenBao Pod.
func (c *Client) ConfigureWorkloadAuthentication(ctx context.Context) error {
	if c.token == "" {
		return errors.New("OpenBao operator token is required to configure workload authentication")
	}
	seal, err := c.readSealStatus(ctx)
	if err != nil {
		return err
	}
	if !seal.Initialized || seal.Sealed {
		return ErrSealed
	}
	if err := c.ensureAuthMethod(ctx); err != nil {
		return err
	}
	for _, serviceAccount := range workloadServiceAccounts {
		role, err := WorkloadAuthRoleName(serviceAccount)
		if err != nil {
			return err
		}
		policy, ok := runtimePolicies[role]
		if !ok {
			return ErrConfigurationDrift
		}
		if err := c.ensurePolicy(ctx, role, policy); err != nil {
			return err
		}
		if err := c.ensureKubernetesWorkloadRole(ctx, role, serviceAccount); err != nil {
			return err
		}
	}
	for _, serviceAccount := range workloadServiceAccounts {
		role, err := WorkloadAuthRoleName(serviceAccount)
		if err != nil {
			return err
		}
		if err := c.verifyObject(ctx, "/v1/auth/kubernetes/role/"+role, map[string]any{
			"bound_service_account_names":      []any{serviceAccount},
			"bound_service_account_namespaces": []any{namespaceFromServiceDomain(c.serviceDomain)},
			"audience":                         "openbao", "policies": []any{role}, "token_policies": []any{role},
			"token_ttl": "15m", "token_max_ttl": "1h",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) ensureMount(ctx context.Context, path, engineType string) error {
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/sys/mounts", nil, &response); err != nil {
		return err
	}
	existing, ok := response.Data[path+"/"]
	if !ok {
		return c.request(ctx, http.MethodPost, "/v1/sys/mounts/"+path, map[string]any{"type": engineType}, nil)
	}
	settings, ok := existing.(map[string]any)
	if !ok || settings["type"] != engineType {
		return fmt.Errorf("%w: secret engine mount %s has an unexpected type", ErrConfigurationDrift, path)
	}
	return nil
}

func (c *Client) ensureAuthMethod(ctx context.Context) error {
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/sys/auth", nil, &response); err != nil {
		return err
	}
	entry, ok := response.Data["kubernetes/"]
	if !ok {
		return c.request(ctx, http.MethodPost, "/v1/sys/auth/kubernetes", map[string]any{"type": "kubernetes"}, nil)
	}
	settings, ok := entry.(map[string]any)
	if !ok || settings["type"] != "kubernetes" {
		return fmt.Errorf("%w: auth/kubernetes mount has an unexpected type", ErrConfigurationDrift)
	}
	return nil
}

func (c *Client) ensureKubernetesConfig(ctx context.Context) error {
	expected := map[string]any{"kubernetes_host": "https://kubernetes.default.svc", "disable_iss_validation": true}
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/auth/kubernetes/config", nil, &response); err != nil {
		var statusErr *apiError
		if errors.As(err, &statusErr) && statusErr.isNotFound() {
			return c.request(ctx, http.MethodPost, "/v1/auth/kubernetes/config", expected, nil)
		}
		return err
	}
	if !containsExpected(response.Data, expected) {
		return fmt.Errorf("%w: Kubernetes auth configuration differs from the local service-account trust configuration", ErrConfigurationDrift)
	}
	return nil
}

func (c *Client) ensureTransitKey(ctx context.Context) error {
	path := "/v1/transit/keys/" + transitKeyName
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		var statusErr *apiError
		if errors.As(err, &statusErr) && statusErr.isNotFound() {
			return c.request(ctx, http.MethodPost, path, map[string]any{
				"type": transitKeyType, "exportable": false, "allow_plaintext_backup": false,
			}, nil)
		}
		return err
	}
	if response.Data["type"] != transitKeyType && response.Data["key_type"] != transitKeyType {
		return fmt.Errorf("%w: Transit key %s has an unexpected type", ErrConfigurationDrift, transitKeyName)
	}
	return nil
}

func (c *Client) ensurePKIRoot(ctx context.Context) error {
	return c.ensurePKIRootPresence(ctx, false)
}

func (c *Client) ensurePKIMountTTL(ctx context.Context) error {
	var current dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/sys/mounts/pki/tune", nil, &current); err != nil {
		return err
	}
	seconds, ok := durationSeconds(current.Data["max_lease_ttl"])
	if !ok {
		return fmt.Errorf("%w: PKI mount max_lease_ttl is missing or invalid", ErrConfigurationDrift)
	}
	if seconds >= pkiMaxLeaseTTL.Seconds() {
		return nil
	}
	if err := c.request(ctx, http.MethodPost, "/v1/sys/mounts/pki/tune", map[string]any{"max_lease_ttl": pkiMaxLeaseTTL.String()}, nil); err != nil {
		return err
	}
	return c.verifyPKIMountTTL(ctx)
}

func (c *Client) verifyPKIMountTTL(ctx context.Context) error {
	var current dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/sys/mounts/pki/tune", nil, &current); err != nil {
		return err
	}
	seconds, ok := durationSeconds(current.Data["max_lease_ttl"])
	if !ok || seconds < pkiMaxLeaseTTL.Seconds() {
		return fmt.Errorf("%w: PKI mount max_lease_ttl is below 8760h", ErrConfigurationDrift)
	}
	return nil
}

func durationSeconds(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case string:
		duration, err := time.ParseDuration(typed)
		if err != nil {
			return 0, false
		}
		return duration.Seconds(), true
	default:
		return 0, false
	}
}

func (c *Client) ensurePKIRootPresence(ctx context.Context, driftOnMissing bool) error {
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/pki/issuer/default/json", nil, &response); err != nil {
		if errors.Is(err, ErrNoDefaultIssuer) {
			if driftOnMissing {
				return fmt.Errorf("%w: PKI root issuer is missing", ErrConfigurationDrift)
			}
			return c.request(ctx, http.MethodPost, "/v1/pki/root/generate/internal", map[string]any{
				"common_name": "Ops Platform Development Internal CA",
				"ttl":         "8760h",
				"key_type":    "ec",
				"key_bits":    256,
			}, nil)
		}
		return err
	}
	certificatePEM, ok := response.Data["certificate"].(string)
	if !ok || certificatePEM == "" {
		return fmt.Errorf("%w: PKI default issuer response is incomplete", ErrConfigurationDrift)
	}
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("%w: PKI default issuer certificate is invalid", ErrConfigurationDrift)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || certificate.NotAfter.Before(time.Now().Add(360*24*time.Hour)) {
		return fmt.Errorf("%w: PKI default issuer certificate expires before the 360-day minimum", ErrConfigurationDrift)
	}
	return nil
}

func (c *Client) ensurePKIRole(ctx context.Context) error {
	expected := map[string]any{
		"allowed_domains":             []any{c.serviceDomain},
		"allow_subdomains":            true,
		"allow_bare_domains":          false,
		"allow_ip_sans":               false,
		"allow_localhost":             false,
		"allow_wildcard_certificates": false,
		"server_flag":                 true,
		"client_flag":                 true,
		"max_ttl":                     "24h",
		"key_type":                    "ec",
		"key_bits":                    float64(256),
	}
	return c.ensureObject(ctx, "/v1/pki/roles/"+pkiRoleName, expected)
}

func (c *Client) ensureWorkloadPKIRole(ctx context.Context, serviceAccount string) error {
	role, expected, err := c.workloadPKIRoleConfig(serviceAccount)
	if err != nil {
		return err
	}
	return c.ensureObject(ctx, "/v1/pki/roles/"+role, expected)
}

func (c *Client) workloadPKIRoleConfig(serviceAccount string) (string, map[string]any, error) {
	role, err := WorkloadRoleName(serviceAccount)
	if err != nil {
		return "", nil, err
	}
	dnsName := serviceAccount + "." + namespaceFromServiceDomain(c.serviceDomain) + ".svc.cluster.local"
	uriSAN := "spiffe://ops.local/ns/" + namespaceFromServiceDomain(c.serviceDomain) + "/sa/" + serviceAccount
	expected := map[string]any{
		"allowed_domains":             []any{dnsName},
		"allowed_uri_sans":            []any{uriSAN},
		"allow_subdomains":            false,
		"allow_bare_domains":          true,
		"allow_any_name":              false,
		"require_cn":                  false,
		"use_csr_common_name":         false,
		"allow_ip_sans":               false,
		"allow_localhost":             false,
		"allow_wildcard_certificates": false,
		"server_flag":                 true,
		"client_flag":                 true,
		"use_csr_sans":                true,
		"max_ttl":                     "1h",
		"key_type":                    "ec",
		"key_bits":                    float64(256),
	}
	return role, expected, nil
}

func (c *Client) ensureSSHCA(ctx context.Context) error {
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/ssh/config/ca", nil, &response); err == nil {
		if value, ok := response.Data["public_key"].(string); !ok || value == "" {
			return fmt.Errorf("%w: SSH signing CA response is incomplete", ErrConfigurationDrift)
		}
		return nil
	} else {
		var statusErr *apiError
		if !errors.As(err, &statusErr) || (statusErr.statusCode != http.StatusNotFound && statusErr.statusCode != http.StatusBadRequest) {
			return err
		}
	}
	return c.request(ctx, http.MethodPost, "/v1/ssh/config/ca", map[string]any{"generate_signing_key": true}, nil)
}

func (c *Client) ensureSSHRole(ctx context.Context) error {
	expected := map[string]any{
		"key_type":                "ca",
		"allow_user_certificates": true,
		"allow_host_certificates": false,
		"allowed_users":           "ops",
		"default_user":            "ops",
		"ttl":                     "30m",
	}
	return c.ensureObject(ctx, "/v1/ssh/roles/"+sshRoleName, expected)
}

func (c *Client) ensurePolicy(ctx context.Context, name, policy string) error {
	path := "/v1/sys/policies/acl/" + name
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		var statusErr *apiError
		if errors.As(err, &statusErr) && statusErr.isNotFound() {
			return c.request(ctx, http.MethodPost, path, map[string]string{"policy": policy}, nil)
		}
		return err
	}
	if current, _ := response.Data["policy"].(string); !allowsCommandPolicy(name, current, policy) {
		return fmt.Errorf("%w: ACL policy %s differs from the least-privilege policy", ErrConfigurationDrift, name)
	}
	return nil
}

func (c *Client) ensureKubernetesRole(ctx context.Context, name, serviceAccount string) error {
	expected := map[string]any{
		"audience":                         "openbao",
		"bound_service_account_names":      []any{serviceAccount},
		"bound_service_account_namespaces": []any{namespaceFromServiceDomain(c.serviceDomain)},
		"policies":                         []any{name},
		"token_policies":                   []any{name},
		"token_ttl":                        "15m",
		"token_max_ttl":                    "1h",
	}
	return c.ensureObject(ctx, "/v1/auth/kubernetes/role/"+name, expected)
}

func (c *Client) ensureKubernetesWorkloadRole(ctx context.Context, name, serviceAccount string) error {
	expected := map[string]any{
		"bound_service_account_names":      []any{serviceAccount},
		"bound_service_account_namespaces": []any{namespaceFromServiceDomain(c.serviceDomain)},
		"audience":                         "openbao",
		"policies":                         []any{name},
		"token_policies":                   []any{name},
		"token_ttl":                        "15m",
		"token_max_ttl":                    "1h",
	}
	return c.ensureObject(ctx, "/v1/auth/kubernetes/role/"+name, expected)
}

func (c *Client) ensureObject(ctx context.Context, path string, expected map[string]any) error {
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		var statusErr *apiError
		if errors.As(err, &statusErr) && statusErr.isNotFound() {
			return c.request(ctx, http.MethodPost, path, expected, nil)
		}
		return err
	}
	if !containsExpected(response.Data, expected) {
		return fmt.Errorf("%w: configuration at %s differs from the resolved development contract", ErrConfigurationDrift, strings.TrimPrefix(path, "/v1/"))
	}
	return nil
}

func (c *Client) verifyConfiguration(ctx context.Context) error {
	var mounts dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/sys/mounts", nil, &mounts); err != nil {
		return err
	}
	for path, kind := range map[string]string{"transit/": "transit", "pki/": "pki", "ssh/": "ssh"} {
		entry, ok := mounts.Data[path].(map[string]any)
		if !ok || entry["type"] != kind {
			return fmt.Errorf("%w: mount %s is missing or has a different type", ErrConfigurationDrift, path)
		}
	}
	if err := c.verifyPKIMountTTL(ctx); err != nil {
		return err
	}
	var auth dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/sys/auth", nil, &auth); err != nil {
		return err
	}
	kubernetes, ok := auth.Data["kubernetes/"].(map[string]any)
	if !ok || kubernetes["type"] != "kubernetes" {
		return fmt.Errorf("%w: Kubernetes auth method is missing or has a different type", ErrConfigurationDrift)
	}
	var transit dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/transit/keys/"+transitKeyName, nil, &transit); err != nil {
		return asDriftIfNotFound(err, "Transit evidence key is missing")
	}
	if transit.Data["type"] != transitKeyType && transit.Data["key_type"] != transitKeyType {
		return fmt.Errorf("%w: Transit evidence key has a different type", ErrConfigurationDrift)
	}
	var auditSigning dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/transit/keys/audit-signing", nil, &auditSigning); err != nil {
		return asDriftIfNotFound(err, "Transit audit signing key is missing")
	}
	if (auditSigning.Data["type"] != "ed25519" && auditSigning.Data["key_type"] != "ed25519") ||
		auditSigning.Data["exportable"] == true || auditSigning.Data["allow_plaintext_backup"] == true {
		return fmt.Errorf("%w: Transit audit signing key type or exportability has drifted", ErrConfigurationDrift)
	}
	if err := c.ensurePKIRootPresence(ctx, true); err != nil {
		return err
	}
	var kubernetesConfig dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/auth/kubernetes/config", nil, &kubernetesConfig); err != nil {
		return asDriftIfNotFound(err, "Kubernetes auth config is missing")
	}
	if !containsExpected(kubernetesConfig.Data, map[string]any{"kubernetes_host": "https://kubernetes.default.svc", "disable_iss_validation": true}) {
		return fmt.Errorf("%w: Kubernetes auth settings have drifted", ErrConfigurationDrift)
	}
	if err := c.verifyObject(ctx, "/v1/pki/roles/"+pkiRoleName, map[string]any{
		"allowed_domains": []any{c.serviceDomain}, "allow_subdomains": true, "allow_bare_domains": false,
		"allow_ip_sans": false, "allow_localhost": false, "allow_wildcard_certificates": false,
		"server_flag": true, "client_flag": true, "max_ttl": "24h", "key_type": "ec", "key_bits": float64(256),
	}); err != nil {
		return err
	}
	if err := c.verifyObject(ctx, "/v1/ssh/roles/"+sshRoleName, map[string]any{
		"key_type": "ca", "allow_user_certificates": true, "allow_host_certificates": false,
		"allowed_users": "ops", "default_user": "ops", "ttl": "30m",
	}); err != nil {
		return err
	}
	for name, policy := range runtimePolicies {
		var response dataResponse
		if err := c.request(ctx, http.MethodGet, "/v1/sys/policies/acl/"+name, nil, &response); err != nil {
			return asDriftIfNotFound(err, "runtime ACL policy is missing")
		}
		if current, _ := response.Data["policy"].(string); !allowsCommandPolicy(name, current, policy) {
			return fmt.Errorf("%w: ACL policy %s has drifted", ErrConfigurationDrift, name)
		}
	}
	for name, serviceAccount := range map[string]string{"ops-api": "ops-api", "ops-worker": "ops-worker", "ops-command-runner": "ops-command-runner"} {
		if err := c.verifyObject(ctx, "/v1/auth/kubernetes/role/"+name, map[string]any{
			"audience":                         "openbao",
			"bound_service_account_names":      []any{serviceAccount},
			"bound_service_account_namespaces": []any{namespaceFromServiceDomain(c.serviceDomain)},
			"policies":                         []any{name}, "token_policies": []any{name}, "token_ttl": "15m", "token_max_ttl": "1h",
		}); err != nil {
			return err
		}
	}
	for _, serviceAccount := range workloadServiceAccounts {
		role, err := WorkloadAuthRoleName(serviceAccount)
		if err != nil {
			return err
		}
		if err := c.verifyObject(ctx, "/v1/auth/kubernetes/role/"+role, map[string]any{
			"bound_service_account_names":      []any{serviceAccount},
			"bound_service_account_namespaces": []any{namespaceFromServiceDomain(c.serviceDomain)},
			"audience":                         "openbao", "policies": []any{role}, "token_policies": []any{role},
			"token_ttl": "15m", "token_max_ttl": "1h",
		}); err != nil {
			return err
		}
		pkiRole, expectedPKI, err := c.workloadPKIRoleConfig(serviceAccount)
		if err != nil {
			return err
		}
		if err := c.verifyObject(ctx, "/v1/pki/roles/"+pkiRole, expectedPKI); err != nil {
			return err
		}
	}
	var sshCA dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/ssh/config/ca", nil, &sshCA); err != nil {
		return asDriftIfNotFound(err, "SSH signing CA is missing")
	}
	if key, ok := sshCA.Data["public_key"].(string); !ok || key == "" {
		return fmt.Errorf("%w: SSH signing CA is not configured", ErrConfigurationDrift)
	}
	return nil
}

func (c *Client) verifyObject(ctx context.Context, path string, expected map[string]any) error {
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		return asDriftIfNotFound(err, "required configuration object is missing")
	}
	if !containsExpected(response.Data, expected) {
		return fmt.Errorf("%w: configuration at %s has drifted", ErrConfigurationDrift, strings.TrimPrefix(path, "/v1/"))
	}
	return nil
}

func asDriftIfNotFound(err error, message string) error {
	var statusErr *apiError
	if errors.As(err, &statusErr) && statusErr.isNotFound() {
		return fmt.Errorf("%w: %s", ErrConfigurationDrift, message)
	}
	return err
}

func containsExpected(actual, expected map[string]any) bool {
	for key, want := range expected {
		got, ok := actual[key]
		if !ok || !valuesEqual(got, want) {
			return false
		}
	}
	return true
}

func valuesEqual(actual, expected any) bool {
	if reflect.DeepEqual(normalizeValue(actual), normalizeValue(expected)) {
		return true
	}
	duration, expectedIsDuration := expected.(string)
	seconds, actualIsNumber := actual.(float64)
	if !expectedIsDuration || !actualIsNumber {
		return false
	}
	parsed, err := time.ParseDuration(duration)
	return err == nil && seconds == parsed.Seconds()
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, fmt.Sprint(item))
		}
		return values
	case []string:
		return typed
	default:
		return value
	}
}

func namespaceFromServiceDomain(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) >= 3 && parts[1] == "svc" {
		return parts[0]
	}
	return "ops-system"
}

var runtimePolicies = map[string]string{
	"ops-api": `path "transit/encrypt/evidence-archive" { capabilities = ["update"] }
path "transit/decrypt/evidence-archive" { capabilities = ["update"] }
path "pki/issue/platform-client" { capabilities = ["update"] }`,
	"ops-worker": `path "transit/encrypt/evidence-archive" { capabilities = ["update"] }
path "transit/decrypt/evidence-archive" { capabilities = ["update"] }
path "transit/sign/audit-signing" { capabilities = ["update"] }
path "transit/verify/audit-signing" { capabilities = ["update"] }`,
	"ops-command-runner": `path "ssh/sign/ops-command-runner" { capabilities = ["update"] }`,
	"ops-api-workload": `path "pki/sign/platform-workload-ops-api" { capabilities = ["update"] }
path "transit/keys/investigation-signing" { capabilities = ["read"] }
path "transit/sign/investigation-signing" { capabilities = ["update"] }`,
	"ops-worker-workload": `path "pki/sign/platform-workload-ops-worker" { capabilities = ["update"] }
path "transit/keys/investigation-signing" { capabilities = ["read"] }
path "transit/sign/investigation-signing" { capabilities = ["update"] }`,
	"ops-investigator-workload":   `path "pki/sign/platform-workload-ops-investigator" { capabilities = ["update"] }`,
	"ops-command-runner-workload": `path "pki/sign/platform-workload-ops-command-runner" { capabilities = ["update"] }`,
}

var workloadServiceAccounts = []string{"ops-api", "ops-worker", "ops-investigator", "ops-command-runner"}

func validateExternalPath(path, repositoryRoot, bundleDirectory string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("an explicit recovery material path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve recovery material path: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return fmt.Errorf("recovery material directory must already exist: %w", err)
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("resolve recovery material path: %w", err)
	}
	for _, protectedRoot := range []string{repositoryRoot, bundleDirectory} {
		if protectedRoot == "" {
			continue
		}
		root, err := filepath.Abs(protectedRoot)
		if err != nil {
			return fmt.Errorf("resolve protected directory: %w", err)
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return fmt.Errorf("resolve protected directory: %w", err)
		}
		relative, err := filepath.Rel(root, absolute)
		if err != nil {
			return fmt.Errorf("compare recovery path: %w", err)
		}
		if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return fmt.Errorf("recovery material must be outside %s", protectedRoot)
		}
	}
	return nil
}

func ioReadLimited(reader io.Reader, limit int64) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read recovery material: %w", err)
	}
	if int64(len(contents)) > limit {
		return nil, errors.New("recovery material file exceeds the supported size")
	}
	return contents, nil
}

// bootstrapPendingMaterial is the minimal material of the identity that is about
// to be submitted. It holds the leaf certificate and its private key, never the
// CA key, and it is bound to the CA and to the service identity. It is written
// with 0600 outside the repository so that a submission whose outcome is unknown
// can be retried with exactly the same identity.
type bootstrapPendingMaterial struct {
	SchemaVersion string `json:"schemaVersion"`
	CADigest      string `json:"caDigest"`
	Service       string `json:"service"`
	Namespace     string `json:"namespace"`
	Secret        string `json:"secret"`
	Certificate   string `json:"certificate"`
	PrivateKey    string `json:"privateKey"`
}

const bootstrapSecretName = "ops-openbao-bootstrap-tls"

// pendingMaterialPath is the protected location of the pending submission.
func pendingMaterialPath(caFile string) string { return caFile + ".pending" }

func writePendingMaterial(root *os.Root, path string, material bootstrapPendingMaterial) error {
	encoded, err := json.Marshal(material)
	if err != nil {
		return fmt.Errorf("encode the pending bootstrap material: %w", err)
	}
	file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create the pending bootstrap material: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set the pending bootstrap material permissions: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return fmt.Errorf("write the pending bootstrap material: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync the pending bootstrap material: %w", err)
	}
	return file.Close()
}

func readPendingMaterial(root *os.Root, path string) (bootstrapPendingMaterial, bool, error) {
	var material bootstrapPendingMaterial
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return material, false, nil
	}
	if err != nil {
		return material, false, fmt.Errorf("inspect the pending bootstrap material: %w", err)
	}
	if !info.Mode().IsRegular() {
		return material, false, errors.New("OPENBAO_TLS_PENDING_INVALID: pending material must be a regular file")
	}
	file, err := root.Open(path)
	if err != nil {
		return material, false, fmt.Errorf("open the pending bootstrap material: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return material, false, errors.New("OPENBAO_TLS_PENDING_INVALID: pending material changed while opening")
	}
	encoded, err := ioReadLimited(file, 1<<20)
	if err != nil {
		return material, false, err
	}
	if err := json.Unmarshal(encoded, &material); err != nil {
		return material, false, errors.New("OPENBAO_TLS_PENDING_INVALID: the pending bootstrap material is not readable")
	}
	if material.SchemaVersion != "bootstrap-pending/v1" || material.Certificate == "" || material.PrivateKey == "" {
		return material, false, errors.New("OPENBAO_TLS_PENDING_INVALID: the pending bootstrap material is incomplete")
	}
	return material, true, nil
}

// createBootstrapSecret submits the identity. The Secret is created, never
// applied: an existing Secret of another source is not overwritten.
func createBootstrapSecret(ctx context.Context, kubeContext, namespace, name string, material bootstrapPendingMaterial) error {
	secretObject := map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{
			"name": name, "namespace": namespace,
			"labels": map[string]string{"ops.platform.io/managed-by": "opsctl", "ops.platform.io/component": "openbao"},
		},
		"type": "kubernetes.io/tls",
		"data": map[string]string{
			"tls.crt": material.Certificate,
			"tls.key": material.PrivateKey,
		},
	}
	encoded, err := json.Marshal(secretObject)
	if err != nil {
		return fmt.Errorf("encode bootstrap TLS Secret: %w", err)
	}
	command := exec.CommandContext(ctx, "kubectl", "--context", kubeContext, "create", "--filename", "-")
	command.Stdin = strings.NewReader(string(encoded))
	if _, err := command.Output(); err != nil {
		return errors.New("OPENBAO_TLS_SECRET_CREATE_FAILED: kubectl could not create the bootstrap TLS Secret")
	}
	return nil
}

// submitBootstrapIdentity submits the pending identity and confirms its outcome.
// A create whose result is unknown is never read as "it will never land": the
// remote is inspected, and only a matching certificate and key converge.
func submitBootstrapIdentity(ctx context.Context, kubeContext, namespace, serviceName string, material bootstrapPendingMaterial) error {
	createErr := createBootstrapSecret(ctx, kubeContext, namespace, bootstrapSecretName, material)
	remote, exists, readErr := readBootstrapSecret(ctx, kubeContext, namespace, bootstrapSecretName)
	if readErr != nil {
		return errors.New("OPENBAO_TLS_SECRET_CREATE_UNKNOWN: the bootstrap TLS Secret write could not be confirmed; the CA trust and the pending identity were kept")
	}
	if !exists {
		if createErr != nil {
			return errors.New("OPENBAO_TLS_SECRET_CREATE_UNKNOWN: the bootstrap TLS Secret write could not be confirmed; the CA trust and the pending identity were kept")
		}
		return errors.New("OPENBAO_TLS_SECRET_CREATE_UNKNOWN: the bootstrap TLS Secret is not readable yet; the CA trust and the pending identity were kept")
	}
	if remote.Data["tls.crt"] != material.Certificate || remote.Data["tls.key"] != material.PrivateKey {
		// The remote holds another identity. The trust anchor and the pending
		// material are kept; an unknown certificate is never trusted.
		return errors.New("OPENBAO_TLS_SECRET_IDENTITY_MISMATCH: the bootstrap TLS Secret does not hold the submitted identity")
	}
	return nil
}

func EnsureBootstrapTLS(ctx context.Context, kubeContext, namespace, serviceName, caFile, repositoryRoot, bundleDirectory string) error {
	if !validKubernetesName(namespace) || !validKubernetesName(serviceName) {
		return errors.New("OpenBao TLS bootstrap requires a valid namespace and service name")
	}
	if err := validateExternalPath(caFile, repositoryRoot, bundleDirectory); err != nil {
		return fmt.Errorf("bootstrap CA path: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(caFile))
	if err != nil {
		return fmt.Errorf("resolve bootstrap directory: %w", err)
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("inspect bootstrap directory: %w", err)
	}
	pendingFile := pendingMaterialPath(caFile)
	if err := validateExternalPath(filepath.Join(parent, filepath.Base(pendingFile)), repositoryRoot, bundleDirectory); err != nil {
		return fmt.Errorf("bootstrap pending path: %w", err)
	}
	// Pin the validated directory; exclusive creation never follows pending links.
	root, err := os.OpenRoot(parent)
	if err != nil {
		return fmt.Errorf("open bootstrap directory: %w", err)
	}
	defer root.Close()
	openedParent, err := root.Stat(".")
	if err != nil || !os.SameFile(parentInfo, openedParent) {
		return errors.New("OPENBAO_TLS_PENDING_INVALID: bootstrap directory changed while opening")
	}
	caPath, pendingPath := filepath.Base(caFile), filepath.Base(pendingFile)
	pending, hasPending, err := readPendingMaterial(root, pendingPath)
	if err != nil {
		return err
	}
	secret, exists, err := readBootstrapSecret(ctx, kubeContext, namespace, bootstrapSecretName)
	if err != nil {
		return err
	}
	// Existing public CA aliases may point to another validated external directory.
	caPEM, caErr := os.ReadFile(caFile)
	if exists {
		// The Secret is in place: it has to be signed by the trusted CA, and when
		// the pending identity of this CA is known, it has to hold the same key.
		if caErr != nil {
			return errors.New("OPENBAO_CA_UNTRUSTED: existing TLS Secret has no explicitly trusted bootstrap CA file")
		}
		if err := verifyServerCertificate(caPEM, secret.Data["tls.crt"], serviceName, namespace); err != nil {
			return err
		}
		certificate, certErr := base64.StdEncoding.DecodeString(secret.Data["tls.crt"])
		key, keyErr := base64.StdEncoding.DecodeString(secret.Data["tls.key"])
		if certErr != nil || keyErr != nil {
			return errors.New("OPENBAO_TLS_SECRET_INVALID: TLS certificate or key data is invalid")
		}
		if _, err := tls.X509KeyPair(certificate, key); err != nil {
			return errors.New("OPENBAO_TLS_SECRET_INVALID: TLS certificate and private key do not form a valid pair")
		}
		if hasPending && pending.CADigest == digestBytes(caPEM) && secret.Data["tls.key"] != pending.PrivateKey {
			return errors.New("OPENBAO_TLS_SECRET_IDENTITY_MISMATCH: the bootstrap TLS Secret does not hold the submitted identity")
		}
		// The identity is in place; the pending material is no longer needed.
		_ = root.Remove(pendingPath)
		return nil
	}
	if caErr == nil {
		// A CA trust exists but the Secret is not readable. The identity of that
		// trust is only known through the pending material. Without it the trust
		// anchor is never destroyed: the recovery is explicit.
		if !hasPending {
			return errors.New("OPENBAO_TLS_TRUST_UNRECOVERABLE: the bootstrap CA trust exists but the identity behind it is unknown; remove the CA file and the pending material by hand to bootstrap a new identity")
		}
		if pending.CADigest != digestBytes(caPEM) || pending.Service != serviceName || pending.Namespace != namespace {
			return errors.New("OPENBAO_TLS_TRUST_UNRECOVERABLE: the pending bootstrap identity does not belong to the trusted CA")
		}
		// The same identity is submitted again; a create that already landed is
		// confirmed by the remote instead of overwriting it.
		if err := submitBootstrapIdentity(ctx, kubeContext, namespace, serviceName, pending); err != nil {
			return err
		}
		_ = root.Remove(pendingPath)
		return nil
	}
	if !errors.Is(caErr, os.ErrNotExist) {
		return fmt.Errorf("read bootstrap CA path: %w", caErr)
	}
	caCert, leafCert, leafKey, err := generateBootstrapCertificates(serviceName, namespace)
	if err != nil {
		return err
	}
	ca, err := root.OpenFile(caPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create bootstrap CA trust file: %w", err)
	}
	if err := ca.Chmod(0o600); err != nil {
		_ = ca.Close()
		_ = root.Remove(caPath)
		return fmt.Errorf("set bootstrap CA trust file permissions: %w", err)
	}
	if _, err := ca.Write(caCert); err != nil {
		_ = ca.Close()
		_ = root.Remove(caPath)
		return fmt.Errorf("write bootstrap CA trust file: %w", err)
	}
	if err := ca.Sync(); err != nil {
		_ = ca.Close()
		_ = root.Remove(caPath)
		return fmt.Errorf("sync bootstrap CA trust file: %w", err)
	}
	if err := ca.Close(); err != nil {
		return fmt.Errorf("close bootstrap CA trust file: %w", err)
	}
	material := bootstrapPendingMaterial{
		SchemaVersion: "bootstrap-pending/v1",
		CADigest:      digestBytes(caCert),
		Service:       serviceName,
		Namespace:     namespace,
		Secret:        bootstrapSecretName,
		Certificate:   base64.StdEncoding.EncodeToString(leafCert),
		PrivateKey:    base64.StdEncoding.EncodeToString(leafKey),
	}
	// The pending identity is stored before the submission, so an unknown
	// outcome can be retried with exactly the same identity.
	if err := writePendingMaterial(root, pendingPath, material); err != nil {
		_ = root.Remove(caPath)
		return err
	}
	if err := submitBootstrapIdentity(ctx, kubeContext, namespace, serviceName, material); err != nil {
		return err
	}
	_ = root.Remove(pendingPath)
	return nil
}

// digestBytes is the identity of a trust anchor or a submitted certificate.
func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ReadBootstrapCA(caFile, repositoryRoot, bundleDirectory string) ([]byte, error) {
	if err := validateExternalPath(caFile, repositoryRoot, bundleDirectory); err != nil {
		return nil, fmt.Errorf("bootstrap CA path: %w", err)
	}
	info, err := os.Stat(caFile)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap CA file: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return nil, errors.New("bootstrap CA file permissions must be exactly 0600")
	}
	contents, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(contents) {
		return nil, errors.New("OPENBAO_CA_UNTRUSTED: bootstrap CA file contains no valid certificate")
	}
	return contents, nil
}

func readBootstrapSecret(ctx context.Context, kubeContext, namespace, name string) (kubernetesSecret, bool, error) {
	command := exec.CommandContext(ctx, "kubectl", "--context", kubeContext, "--namespace", namespace, "get", "secret", name, "--ignore-not-found", "-o", "json")
	output, err := command.Output()
	if err != nil {
		return kubernetesSecret{}, false, errors.New("OPENBAO_TLS_SECRET_LOOKUP_FAILED: unable to inspect the bootstrap TLS Secret")
	}
	if len(bytesTrimSpace(output)) == 0 {
		return kubernetesSecret{}, false, nil
	}
	var secret kubernetesSecret
	if err := json.Unmarshal(output, &secret); err != nil {
		return kubernetesSecret{}, false, errors.New("OPENBAO_TLS_SECRET_INVALID: bootstrap TLS Secret response is invalid")
	}
	return secret, true, nil
}

type kubernetesSecret struct {
	Data map[string]string `json:"data"`
}

func verifyServerCertificate(caPEM []byte, encodedCertificate, service, namespace string) error {
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return errors.New("OPENBAO_CA_UNTRUSTED: bootstrap CA file contains no valid certificate")
	}
	certificatePEM, err := base64.StdEncoding.DecodeString(encodedCertificate)
	if err != nil {
		return errors.New("OPENBAO_TLS_SECRET_INVALID: TLS certificate data is invalid")
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("OPENBAO_TLS_SECRET_INVALID: TLS certificate PEM is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return errors.New("OPENBAO_TLS_SECRET_INVALID: TLS certificate cannot be parsed")
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: caPool, DNSName: service + "." + namespace + ".svc.cluster.local"}); err != nil {
		return errors.New("OPENBAO_CA_UNTRUSTED: bootstrap TLS certificate is not signed by the trusted CA")
	}
	return nil
}

func generateBootstrapCertificates(service, namespace string) ([]byte, []byte, []byte, error) {
	now := time.Now().UTC()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate bootstrap CA key: %w", err)
	}
	caSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate bootstrap CA serial: %w", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject:      pkix.Name{CommonName: "Ops Platform OpenBao Bootstrap CA"},
		NotBefore:    now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true, IsCA: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create bootstrap CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse bootstrap CA certificate: %w", err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate OpenBao TLS key: %w", err)
	}
	leafSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate OpenBao TLS serial: %w", err)
	}
	names := []string{service, service + "." + namespace, service + "." + namespace + ".svc", service + "." + namespace + ".svc.cluster.local"}
	leafTemplate := &x509.Certificate{
		SerialNumber: leafSerial,
		Subject:      pkix.Name{CommonName: service + "." + namespace + ".svc.cluster.local"},
		DNSNames:     names,
		NotBefore:    now.Add(-5 * time.Minute), NotAfter: now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create OpenBao TLS certificate: %w", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	return caPEM, leafPEM, leafKeyPEM, nil
}

func bytesTrimSpace(value []byte) []byte { return []byte(strings.TrimSpace(string(value))) }

func validKubernetesName(value string) bool {
	if len(value) == 0 || len(value) > 63 {
		return false
	}
	if value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
			return false
		}
	}
	return true
}
