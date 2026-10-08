package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"ops-platform/internal/bootstrap"
)

func runBootstrapOIDCAdministrator(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap oidc-administrator", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stage := flags.String("stage", "", "prepare or retire; fresh named LoA-2 bearer required")
	profilePath := flags.String("profile", "", "current bundled Profile")
	configPath := flags.String("config", "", "explicit named tenant/subject/username JSON")
	privatePath := flags.String("secrets-file", "", "external private bootstrap credentials and replacement password")
	caPath := flags.String("ca", "", "independent Keycloak public CA")
	tokenPath := flags.String("token-file", "", "external private fresh named LoA-2 bearer")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*stage != "prepare" && *stage != "retire") || *profilePath == "" || *configPath == "" || *privatePath == "" || *caPath == "" || *tokenPath == "" {
		return errors.New("OIDC administrator requires --stage, --profile, --config, --secrets-file, --ca and --token-file")
	}
	var config bootstrap.OIDCAdministrator
	var private struct {
		BootstrapUsername   string `json:"bootstrapUsername"`
		BootstrapPassword   string `json:"bootstrapPassword"`
		ReplacementPassword string `json:"replacementBootstrapPassword"`
	}
	for _, input := range []struct {
		path   string
		target any
		secret bool
	}{{*configPath, &config, false}, {*privatePath, &private, true}} {
		if input.secret {
			info, err := os.Stat(input.path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(input.path) {
				return errors.New("OIDC administration credentials require a private repository-external file")
			}
		}
		raw, err := readBoundedFile(input.path, 64<<10)
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err != nil || decoder.Decode(input.target) != nil || decoder.Decode(new(any)) != io.EOF {
			return errors.New("explicit OIDC administration input invalid")
		}
	}
	if config.Validate() != nil || len(private.BootstrapUsername) < 1 || len(private.BootstrapPassword) < 24 || len(private.ReplacementPassword) < 24 || private.BootstrapPassword == private.ReplacementPassword || config.Username == private.BootstrapUsername {
		return errors.New("separate explicit named and temporary OIDC identities and replacement credential required")
	}
	info, err := os.Stat(*tokenPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(*tokenPath) {
		return errors.New("named bearer requires a private repository-external file")
	}
	bearer, err := readBoundedFile(*tokenPath, 64<<10)
	if err != nil || len(bytes.TrimSpace(bearer)) == 0 {
		return errors.New("fresh named bearer required")
	}
	p, err := readResolvedProfile(*profilePath)
	if err != nil {
		return err
	}
	c := p.Components["keycloak"]
	u, err := url.Parse(c.Endpoint)
	if err != nil || c.Mode != "bundled" || c.Namespace == "" || c.Namespace == "default" || strings.HasPrefix(c.Namespace, "kube-") || u.Scheme != "https" || u.Hostname() != "ops-keycloak."+c.Namespace+".svc.cluster.local" || u.Port() != "8443" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("exact current bundled Keycloak target required")
	}
	get := func(kind, name string, target any) error {
		cmd := exec.CommandContext(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", c.Namespace, "get", kind, name, "-o", "json")
		raw, err := cmd.Output()
		if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, target) != nil {
			return errors.New("owned OIDC bootstrap resource unavailable")
		}
		return nil
	}
	var ns struct {
		Metadata struct {
			UID    string
			Labels map[string]string
		}
	}
	if err := get("namespace", c.Namespace, &ns); err != nil || ns.Metadata.UID == "" || ns.Metadata.Labels["ops.platform.io/managed-by"] != "opsctl-bootstrap" {
		return errors.New("OIDC administration requires formal namespace ownership")
	}
	installationID := ns.Metadata.Labels["ops.platform.io/installation-id"]
	if _, err := uuid.Parse(installationID); err != nil {
		return errors.New("OIDC installation identity invalid")
	}
	var trust struct {
		Data     map[string]string
		Metadata struct{ Labels map[string]string }
	}
	if get("configmap", "ops-platform-bootstrap", &trust) != nil || trust.Metadata.Labels["ops.platform.io/installation-id"] != installationID {
		return errors.New("OIDC independent bootstrap trust unavailable")
	}
	ca, err := readBoundedFile(*caPath, 1<<20)
	roots := x509.NewCertPool()
	if err != nil || !bytes.Equal(ca, []byte(trust.Data["oidc-ca.pem"])) || bytes.Contains(ca, []byte("PRIVATE KEY")) || !roots.AppendCertsFromPEM(ca) {
		return errors.New("OIDC public CA differs from independent installation trust")
	}
	var secret map[string]any
	if get("secret", "ops-keycloak-auth", &secret) != nil {
		return errors.New("owned temporary administrator Secret unavailable")
	}
	metadata, _ := secret["metadata"].(map[string]any)
	labels, _ := metadata["labels"].(map[string]any)
	data, _ := secret["data"].(map[string]any)
	encodedUser, _ := data["username"].(string)
	encodedPassword, _ := data["password"].(string)
	username, uErr := base64.StdEncoding.DecodeString(encodedUser)
	password, pErr := base64.StdEncoding.DecodeString(encodedPassword)
	if labels["ops.platform.io/installation-id"] != installationID || metadata["uid"] == nil || metadata["resourceVersion"] == nil || uErr != nil || pErr != nil || string(username) != private.BootstrapUsername || string(password) != private.BootstrapPassword {
		return errors.New("temporary administrator input differs from owned installation Secret")
	}
	address, stop, err := keycloakBootstrapForward(ctx, p.Kubernetes.Context, c.Namespace)
	if err != nil {
		return err
	}
	defer stop()
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: u.Hostname()}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != u.Host {
			return nil, errors.New("OIDC administrative route outside pinned service")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	trusted := oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(trusted, c.Endpoint+"/realms/ops")
	if err != nil {
		return errors.New("named administrator OIDC discovery unavailable")
	}
	token, err := provider.VerifierContext(trusted, &oidc.Config{ClientID: "ops-api", SupportedSigningAlgs: []string{"RS256"}}).Verify(trusted, strings.TrimSpace(string(bearer)))
	identity := bootstrap.FirstTenant{TenantID: config.TenantID, AdminSubject: config.Subject.String()}
	if err != nil || verifyBootstrapIdentity(token, identity, time.Now()) != nil {
		return errors.New("fresh named administrator RS256/tenant/subject/LoA-2 identity rejected")
	}
	privateInput := bootstrap.OIDCPrivateInput{BootstrapUsername: private.BootstrapUsername, BootstrapPassword: private.BootstrapPassword}
	if *stage == "prepare" {
		if err = bootstrap.PrepareOIDCAdministrator(ctx, client, c.Endpoint, config, privateInput); err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"stage": "named-role-prepared", "namespaceUid": ns.Metadata.UID, "installationId": installationID, "subject": config.Subject, "realm": "ops", "role": "realm-management/realm-admin", "freshLoginRequiredBeforeRetirement": true, "temporaryAdministratorRetired": false})
	}
	rotate := func() error {
		data["password"] = base64.StdEncoding.EncodeToString([]byte(private.ReplacementPassword))
		raw, _ := json.Marshal(secret)
		cmd := exec.CommandContext(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", c.Namespace, "replace", "-f", "-", "-o", "name")
		cmd.Stdin = bytes.NewReader(raw)
		if cmd.Run() != nil {
			return errors.New("owned temporary credential rotation rejected; no principal removed")
		}
		return nil
	}
	removedID, err := bootstrap.RetireOIDCAdministrator(ctx, client, c.Endpoint, config, privateInput, strings.TrimSpace(string(bearer)), rotate)
	if err != nil {
		return err
	}
	for _, command := range [][]string{{"rollout", "restart", "deployment/ops-keycloak"}, {"rollout", "status", "deployment/ops-keycloak", "--timeout=180s"}} {
		args := append([]string{"--context", p.Kubernetes.Context, "-n", c.Namespace}, command...)
		if exec.CommandContext(ctx, "kubectl", args...).Run() != nil {
			return errors.New("BOOTSTRAP_PARTIAL_PRINCIPAL_REMOVED: Keycloak restart not confirmed; named administrator previously verified")
		}
	}
	// The old tunnel may terminate as its selected Pod is replaced. Re-open it
	// and prove named management survives the actual restart.
	newAddress, newStop, err := keycloakBootstrapForward(ctx, p.Kubernetes.Context, c.Namespace)
	if err != nil {
		return errors.New("BOOTSTRAP_PARTIAL_PRINCIPAL_REMOVED: post-restart route unavailable")
	}
	defer newStop()
	address = newAddress
	transport.CloseIdleConnections()
	if bootstrap.VerifyOIDCAdministrator(ctx, client, c.Endpoint, config, strings.TrimSpace(string(bearer))) != nil {
		return errors.New("BOOTSTRAP_PARTIAL_PRINCIPAL_REMOVED: named post-restart management not confirmed")
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"stage": "temporary-administrator-retired", "namespaceUid": ns.Metadata.UID, "installationId": installationID, "subject": config.Subject, "retiredMasterSubject": removedID, "temporaryCredentialRotated": true, "oldCredentialAndTokenRejected": true, "keycloakRestarted": true, "namedPostRestartManagementVerified": true, "masterAdministrationDenied": true})
}
