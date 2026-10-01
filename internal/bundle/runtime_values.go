package bundle

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/profile"
)

// Runtime trust and the archive destination are provisioned independently by
// the operator. A signed Bundle must not bootstrap its own application trust.
func platformRuntimeValues(ctx context.Context, p profile.ResolvedProfile, run CommandRunner) (map[string]any, error) {
	issuer := strings.TrimSuffix(p.Components["keycloak"].Endpoint, "/")
	for _, endpoint := range []string{issuer, p.Components["openbao"].Endpoint} {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("runtime Keycloak and OpenBao endpoints require HTTPS without credentials")
		}
	}
	archive := p.Components["seaweedfs"].Endpoint
	u, err := url.Parse(archive)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("runtime archive endpoint is invalid")
	}
	encoded, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "--namespace", "ops-system", "get", "configmap", "ops-platform-bootstrap", "-o", "json")
	if err != nil {
		return nil, errors.New("independent ops-platform-bootstrap ConfigMap is required before import")
	}
	var bootstrap struct {
		Data map[string]string `json:"data"`
	}
	if len(encoded) > 1<<20 || json.Unmarshal(encoded, &bootstrap) != nil {
		return nil, errors.New("runtime bootstrap ConfigMap is invalid")
	}
	validateCA := func(key string, required bool) error {
		pem := bootstrap.Data[key]
		if !required && pem == "" {
			return nil
		}
		if strings.Contains(pem, "PRIVATE KEY") || !x509.NewCertPool().AppendCertsFromPEM([]byte(pem)) {
			return fmt.Errorf("runtime bootstrap %s must contain public CA certificates", key)
		}
		return nil
	}
	if err := validateCA("openbao-ca.pem", true); err != nil {
		return nil, err
	}
	if err := validateCA("oidc-ca.pem", true); err != nil {
		return nil, err
	}
	bucket := bootstrap.Data["archive-bucket"]
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(bucket) || strings.Contains(bucket, "..") {
		return nil, errors.New("runtime bootstrap archive-bucket is required and must be an S3 bucket name")
	}
	var trust map[string]string
	if len(bootstrap.Data["registry-trust.json"]) > 64<<10 || json.Unmarshal([]byte(bootstrap.Data["registry-trust.json"]), &trust) != nil || len(trust) == 0 {
		return nil, errors.New("independent registry public trust is required")
	}
	for keyID, key := range trust {
		public, err := base64.StdEncoding.DecodeString(key)
		if keyID == "" || len(keyID) > 200 || err != nil || len(public) != ed25519.PublicKeySize {
			return nil, errors.New("runtime bootstrap registry trust contains an invalid public key")
		}
	}
	profileYAML, err := yaml.Marshal(p)
	if err != nil {
		return nil, err
	}
	return map[string]any{"profile": string(profileYAML), "oidcIssuerURL": issuer + "/realms/ops", "oidcCABundle": bootstrap.Data["oidc-ca.pem"], "openbaoAddress": p.Components["openbao"].Endpoint, "openbaoCABundle": bootstrap.Data["openbao-ca.pem"], "archiveEndpoint": archive, "archiveBucket": bucket, "registryTrust": trust}, nil
}
