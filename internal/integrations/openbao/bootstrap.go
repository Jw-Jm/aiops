package openbao

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
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
	if _, err := os.Lstat(recoveryFile); err == nil {
		return errors.New("recovery material file already exists; refusing to overwrite it")
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
		return fmt.Errorf("OPENBAO_INIT_OUTCOME_UNCERTAIN: reserved recovery path %q; inspect OpenBao state before retrying", recoveryFile)
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
	return c.verifyConfiguration(ctx)
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
	if response.Data["policy"] != policy {
		return fmt.Errorf("%w: ACL policy %s differs from the least-privilege policy", ErrConfigurationDrift, name)
	}
	return nil
}

func (c *Client) ensureKubernetesRole(ctx context.Context, name, serviceAccount string) error {
	expected := map[string]any{
		"bound_service_account_names":      []any{serviceAccount},
		"bound_service_account_namespaces": []any{namespaceFromServiceDomain(c.serviceDomain)},
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
		if response.Data["policy"] != policy {
			return fmt.Errorf("%w: ACL policy %s has drifted", ErrConfigurationDrift, name)
		}
	}
	for name, serviceAccount := range map[string]string{"ops-api": "ops-api", "ops-worker": "ops-worker", "ops-command-runner": "ops-command-runner"} {
		if err := c.verifyObject(ctx, "/v1/auth/kubernetes/role/"+name, map[string]any{
			"bound_service_account_names":      []any{serviceAccount},
			"bound_service_account_namespaces": []any{namespaceFromServiceDomain(c.serviceDomain)},
			"policies":                         []any{name}, "token_policies": []any{name}, "token_ttl": "15m", "token_max_ttl": "1h",
		}); err != nil {
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
	if len(parts) >= 3 && parts[1] != "svc" {
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
}

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

func EnsureBootstrapTLS(ctx context.Context, kubeContext, namespace, serviceName, caFile, repositoryRoot, bundleDirectory string) error {
	if !validKubernetesName(namespace) || !validKubernetesName(serviceName) {
		return errors.New("OpenBao TLS bootstrap requires a valid namespace and service name")
	}
	if err := validateExternalPath(caFile, repositoryRoot, bundleDirectory); err != nil {
		return fmt.Errorf("bootstrap CA path: %w", err)
	}
	secret, exists, err := readBootstrapSecret(ctx, kubeContext, namespace, "ops-openbao-bootstrap-tls")
	if err != nil {
		return err
	}
	caPEM, caErr := os.ReadFile(caFile)
	if exists {
		if caErr != nil {
			return errors.New("OPENBAO_CA_UNTRUSTED: existing TLS Secret has no explicitly trusted bootstrap CA file")
		}
		if err := verifyServerCertificate(caPEM, secret.Data["tls.crt"], serviceName, namespace); err != nil {
			return err
		}
		return nil
	}
	if caErr == nil {
		return errors.New("OPENBAO_TLS_TRUST_DRIFT: bootstrap CA exists but the Kubernetes TLS Secret is missing")
	}
	if !errors.Is(caErr, os.ErrNotExist) {
		return fmt.Errorf("read bootstrap CA path: %w", caErr)
	}

	caCert, leafCert, leafKey, err := generateBootstrapCertificates(serviceName, namespace)
	if err != nil {
		return err
	}
	ca, err := os.OpenFile(caFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create bootstrap CA trust file: %w", err)
	}
	if err := ca.Chmod(0o600); err != nil {
		_ = ca.Close()
		_ = os.Remove(caFile)
		return fmt.Errorf("set bootstrap CA trust file permissions: %w", err)
	}
	if _, err := ca.Write(caCert); err != nil {
		_ = ca.Close()
		_ = os.Remove(caFile)
		return fmt.Errorf("write bootstrap CA trust file: %w", err)
	}
	if err := ca.Sync(); err != nil {
		_ = ca.Close()
		_ = os.Remove(caFile)
		return fmt.Errorf("sync bootstrap CA trust file: %w", err)
	}
	if err := ca.Close(); err != nil {
		return fmt.Errorf("close bootstrap CA trust file: %w", err)
	}
	secretObject := map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{
			"name": "ops-openbao-bootstrap-tls", "namespace": namespace,
			"labels": map[string]string{"ops.platform.io/managed-by": "opsctl", "ops.platform.io/component": "openbao"},
		},
		"type": "kubernetes.io/tls",
		"data": map[string]string{
			"tls.crt": base64.StdEncoding.EncodeToString(leafCert),
			"tls.key": base64.StdEncoding.EncodeToString(leafKey),
		},
	}
	encoded, err := json.Marshal(secretObject)
	if err != nil {
		_ = os.Remove(caFile)
		return fmt.Errorf("encode bootstrap TLS Secret: %w", err)
	}
	command := exec.CommandContext(ctx, "kubectl", "--context", kubeContext, "apply", "--filename", "-")
	command.Stdin = strings.NewReader(string(encoded))
	if _, err := command.Output(); err != nil {
		_ = os.Remove(caFile)
		return errors.New("OPENBAO_TLS_SECRET_CREATE_FAILED: kubectl could not create the bootstrap TLS Secret")
	}
	return nil
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
