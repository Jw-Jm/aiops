package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type EnvironmentInput struct {
	SchemaVersion int               `json:"schemaVersion"`
	Context       string            `json:"context"`
	Namespace     string            `json:"namespace"`
	PublicTrust   map[string]string `json:"publicTrust"`
}

type EnvironmentSecret struct {
	Name string            `json:"name"`
	Data map[string]string `json:"data"`
}

type EnvironmentReceipt struct {
	Namespace       string              `json:"namespace"`
	NamespaceUID    string              `json:"namespaceUid"`
	InstallationID  string              `json:"installationId"`
	PublicTrustKeys []string            `json:"publicTrustKeys"`
	SecretKeys      map[string][]string `json:"secretKeys"`
}

var environmentName = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
var environmentBucket = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func (c EnvironmentInput) Validate(secrets []EnvironmentSecret) error {
	if c.SchemaVersion != 1 || c.Context == "" || !environmentName.MatchString(c.Namespace) || c.Namespace == "default" || strings.HasPrefix(c.Namespace, "kube-") {
		return errors.New("explicit context and dedicated bootstrap namespace required")
	}
	if len(c.PublicTrust) != 5 {
		return errors.New("exact independent public bootstrap trust required")
	}
	for _, key := range []string{"openbao-ca.pem", "oidc-ca.pem", "archive-ca.pem"} {
		ca := c.PublicTrust[key]
		if strings.Contains(ca, "PRIVATE KEY") || !x509.NewCertPool().AppendCertsFromPEM([]byte(ca)) {
			return errors.New("bootstrap trust requires public CA certificates")
		}
	}
	if !environmentBucket.MatchString(c.PublicTrust["archive-bucket"]) || strings.Contains(c.PublicTrust["archive-bucket"], "..") {
		return errors.New("explicit archive bucket required")
	}
	var registry map[string]string
	if json.Unmarshal([]byte(c.PublicTrust["registry-trust.json"]), &registry) != nil || len(registry) == 0 {
		return errors.New("independent Registry trust required")
	}
	for id, key := range registry {
		decoded, err := base64.StdEncoding.DecodeString(key)
		if id == "" || len(id) > 200 || err != nil || len(decoded) != ed25519.PublicKeySize {
			return errors.New("invalid Registry public trust")
		}
	}
	required := map[string][]string{
		"ops-postgresql-auth": {"username", "password"}, "ops-keycloak-auth": {"username", "password"},
		"ops-keycloak-database": {"username", "password"}, "ops-seaweedfs-auth": {"accessKey", "secretKey"},
		"ops-openbao-bootstrap-tls": {"tls.crt", "tls.key"}, "ops-keycloak-tls": {"tls.crt", "tls.key"},
		"ops-seaweedfs-tls": {"tls.crt", "tls.key"}, "ops-seaweedfs-iam": {"s3.json"},
	}
	if len(secrets) != len(required) {
		return errors.New("all explicit core dependency secrets required")
	}
	seen := map[string]bool{}
	for _, secret := range secrets {
		keys, ok := required[secret.Name]
		if !ok || seen[secret.Name] || len(secret.Data) != len(keys) {
			return errors.New("unknown, duplicate or incomplete dependency Secret")
		}
		seen[secret.Name] = true
		for _, key := range keys {
			value := secret.Data[key]
			if value == "" || len(value) > 256<<10 {
				return errors.New("dependency Secret required key missing or oversized")
			}
		}
		if secret.Name == "ops-keycloak-database" && secret.Data["username"] != "ops_keycloak" {
			return errors.New("dedicated Keycloak database identity required")
		}
		if secret.Name == "ops-postgresql-auth" && secret.Data["username"] == "ops_keycloak" {
			return errors.New("database bootstrap and Keycloak identities must differ")
		}
		if strings.HasSuffix(secret.Name, "-tls") {
			pair, err := tls.X509KeyPair([]byte(secret.Data["tls.crt"]), []byte(secret.Data["tls.key"]))
			if err != nil {
				return errors.New("dependency TLS key and certificate do not match")
			}
			leaf, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				return errors.New("invalid dependency TLS certificate")
			}
			service, caKey := "ops-openbao", "openbao-ca.pem"
			if secret.Name == "ops-keycloak-tls" {
				service, caKey = "ops-keycloak", "oidc-ca.pem"
			}
			if secret.Name == "ops-seaweedfs-tls" {
				service, caKey = "ops-seaweedfs-s3", "archive-ca.pem"
			}
			roots := x509.NewCertPool()
			roots.AppendCertsFromPEM([]byte(c.PublicTrust[caKey]))
			intermediates := x509.NewCertPool()
			for _, der := range pair.Certificate[1:] {
				cert, err := x509.ParseCertificate(der)
				if err != nil {
					return errors.New("invalid dependency TLS chain")
				}
				intermediates.AddCert(cert)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: service + "." + c.Namespace + ".svc.cluster.local", CurrentTime: time.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
				return errors.New("dependency TLS differs from independent target trust")
			}
		}
	}
	var admin, iam map[string]string
	for _, secret := range secrets {
		if secret.Name == "ops-seaweedfs-auth" {
			admin = secret.Data
		}
		if secret.Name == "ops-seaweedfs-iam" {
			iam = secret.Data
		}
	}
	if err := validateBootstrapArchiveIAM(iam["s3.json"], admin, c.PublicTrust["archive-bucket"]); err != nil {
		return err
	}

	return nil
}

// EnvironmentClient exposes only the fixed bootstrap resource operations. Its
// CLI implementation uses the already locked kubectl with private stdin.
type EnvironmentClient interface {
	NamespaceExists(context.Context, string) (bool, error)
	Create(context.Context, map[string]any) (string, error)
}

// CreateEnvironment never adopts a historical namespace or prints Secret
// values. Dependencies and business activation remain separate signed steps.
func CreateEnvironment(ctx context.Context, client EnvironmentClient, c EnvironmentInput, secrets []EnvironmentSecret) (EnvironmentReceipt, error) {
	var receipt EnvironmentReceipt
	if err := c.Validate(secrets); err != nil {
		return receipt, err
	}
	if client == nil {
		return receipt, errors.New("bootstrap Kubernetes client required")
	}
	exists, err := client.NamespaceExists(ctx, c.Namespace)
	if err != nil || exists {
		return receipt, errors.New("bootstrap refuses existing or inaccessible namespace")
	}
	id := uuid.NewString()
	labels := map[string]string{"ops.platform.io/installation-id": id, "ops.platform.io/managed-by": "opsctl-bootstrap"}
	object := func(kind, name string) map[string]any {
		metadata := map[string]any{"name": name, "labels": labels}
		if kind != "Namespace" {
			metadata["namespace"] = c.Namespace
		}
		return map[string]any{"apiVersion": "v1", "kind": kind, "metadata": metadata}
	}
	uid, err := client.Create(ctx, object("Namespace", c.Namespace))
	if err != nil || uid == "" {
		return receipt, errors.New("bootstrap namespace creation failed")
	}
	receipt = EnvironmentReceipt{Namespace: c.Namespace, NamespaceUID: uid, InstallationID: id, SecretKeys: map[string][]string{}}
	trust := object("ConfigMap", "ops-platform-bootstrap")
	trust["data"] = c.PublicTrust
	if _, err := client.Create(ctx, trust); err != nil {
		return receipt, errors.New("BOOTSTRAP_PARTIAL_NAMESPACE_RETAINED: public trust creation failed")
	}
	for key := range c.PublicTrust {
		receipt.PublicTrustKeys = append(receipt.PublicTrustKeys, key)
	}
	sort.Strings(receipt.PublicTrustKeys)
	for _, input := range secrets {
		secret := object("Secret", input.Name)
		secret["type"] = "Opaque"
		secret["stringData"] = input.Data
		if _, err := client.Create(ctx, secret); err != nil {
			return receipt, errors.New("BOOTSTRAP_PARTIAL_NAMESPACE_RETAINED: dependency Secret creation failed")
		}
		for key := range input.Data {
			receipt.SecretKeys[input.Name] = append(receipt.SecretKeys[input.Name], key)
		}
		sort.Strings(receipt.SecretKeys[input.Name])
	}
	return receipt, nil
}

func validateBootstrapArchiveIAM(raw string, admin map[string]string, bucket string) error {
	var config struct {
		Identities []struct {
			Name        string `json:"name"`
			Credentials []struct {
				AccessKey string `json:"accessKey"`
				SecretKey string `json:"secretKey"`
			} `json:"credentials"`
			Actions     []string `json:"actions"`
			PolicyNames []string `json:"policyNames"`
		} `json:"identities"`
		Policies []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"policies"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || len(config.Identities) == 0 {
		return errors.New("explicit Archive IAM identities required")
	}
	names, keys := map[string]bool{}, map[string]bool{}
	adminCount := 0
	policies := map[string]string{}
	for _, policy := range config.Policies {
		if policy.Name == "" || policy.Content == "" || policies[policy.Name] != "" {
			return errors.New("distinct explicit Archive policies required")
		}
		policies[policy.Name] = policy.Content
	}
	usedPolicies := map[string]bool{}
	duties := map[string]map[string]bool{}
	scoped := regexp.MustCompile(`^(Read|Write|List):` + regexp.QuoteMeta(bucket) + `/tenants/[a-f0-9]{64}/\*$`)
	for _, identity := range config.Identities {
		if identity.Name == "" || names[identity.Name] || len(identity.Credentials) != 1 || (len(identity.Actions) == 0 && len(identity.PolicyNames) == 0) {
			return errors.New("invalid Archive IAM identity")
		}
		names[identity.Name] = true
		credential := identity.Credentials[0]
		if credential.AccessKey == "" || credential.SecretKey == "" || keys[credential.AccessKey] {
			return errors.New("distinct explicit Archive IAM credentials required")
		}
		keys[credential.AccessKey] = true
		if credential.AccessKey == admin["accessKey"] && credential.SecretKey == admin["secretKey"] {
			if len(identity.Actions) != 1 || identity.Actions[0] != "Admin" || len(identity.PolicyNames) != 0 {
				return errors.New("bootstrap Archive admin duty differs")
			}
			adminCount++
		} else {
			if len(identity.PolicyNames) > 0 {
				if len(identity.Actions) != 0 || len(identity.PolicyNames) != 1 || identity.PolicyNames[0] != identity.Name || usedPolicies[identity.Name] || policies[identity.Name] == "" {
					return errors.New("Archive role must have one exact private policy")
				}
				prefix, role, err := validateArchiveRolePolicy(policies[identity.Name], bucket)
				if err != nil {
					return err
				}
				if duties[prefix] == nil {
					duties[prefix] = map[string]bool{}
				}
				if duties[prefix][role] {
					return errors.New("duplicate Archive role for tenant prefix")
				}
				duties[prefix][role] = true
				usedPolicies[identity.Name] = true
				continue
			}
			for _, action := range identity.Actions {
				if !scoped.MatchString(action) {
					return errors.New("Archive runtime IAM exceeds tenant prefix")
				}
			}
		}
	}
	if adminCount != 1 {
		return errors.New("exact separate Archive bootstrap admin required")
	}
	if len(usedPolicies) != len(policies) {
		return errors.New("unused Archive policy refused")
	}
	for _, roles := range duties {
		if len(roles) != 4 {
			return errors.New("all four separated Archive duties required")
		}
	}
	return nil
}
