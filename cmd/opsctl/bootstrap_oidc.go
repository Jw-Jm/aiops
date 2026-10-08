package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"ops-platform/internal/bootstrap"
)

func runBootstrapOIDC(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap oidc-realm", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "", "current resolved Profile")
	configPath := flags.String("config", "", "explicit OIDC subject/callback JSON")
	privatePath := flags.String("secrets-file", "", "repository-external private OIDC bootstrap inputs")
	caPath := flags.String("ca", "", "independent Keycloak public CA")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *profilePath == "" || *configPath == "" || *privatePath == "" || *caPath == "" {
		return errors.New("OIDC bootstrap requires --profile, --config, --secrets-file and --ca")
	}
	var config bootstrap.OIDCInput
	var private bootstrap.OIDCPrivateInput
	for _, input := range []struct {
		Path  string
		Value any
	}{{*configPath, &config}, {*privatePath, &private}} {
		if input.Path == *privatePath {
			info, err := os.Stat(input.Path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(input.Path) {
				return errors.New("OIDC bootstrap credentials require a private repository-external file")
			}
		}
		raw, err := readBoundedFile(input.Path, 64<<10)
		if err != nil {
			return errors.New("OIDC bootstrap input unavailable")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(input.Value) != nil || decoder.Decode(new(any)) != io.EOF {
			return errors.New("OIDC bootstrap input invalid")
		}
	}
	if err := config.Validate(private); err != nil {
		return err
	}
	p, err := readResolvedProfile(*profilePath)
	if err != nil {
		return err
	}
	component := p.Components["keycloak"]
	endpoint, err := url.Parse(component.Endpoint)
	if err != nil || component.Mode != "bundled" || component.Namespace == "" || component.Namespace == "default" || strings.HasPrefix(component.Namespace, "kube-") || endpoint.Scheme != "https" || endpoint.Hostname() != "ops-keycloak."+component.Namespace+".svc.cluster.local" || endpoint.Port() != "8443" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "" {
		return errors.New("OIDC bootstrap requires the explicit current bundled Keycloak identity")
	}
	raw, err := readBoundedFile(*caPath, 1<<20)
	roots := x509.NewCertPool()
	if err != nil || bytes.Contains(raw, []byte("PRIVATE KEY")) || !roots.AppendCertsFromPEM(raw) {
		return errors.New("independent Keycloak public CA required")
	}
	address, stop, err := keycloakBootstrapForward(ctx, p.Kubernetes.Context, component.Namespace)
	if err != nil {
		return err
	}
	defer stop()
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: endpoint.Hostname()}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != endpoint.Host {
			return nil, errors.New("OIDC bootstrap route outside pinned Keycloak")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	subject, err := bootstrap.BootstrapOIDC(ctx, client, component.Endpoint, config, private)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"issuer": component.Endpoint + "/realms/ops", "tenantId": config.TenantID, "adminSubject": subject, "callbackURL": config.CallbackURL, "otpEnrollment": "required CONFIGURE_TOTP; real LoA-2 browser login required before first-tenant bootstrap", "bootstrapAdministratorRetirement": "pending named Keycloak realm administrator verification and credential retirement"})
}

// The only tunnel created by this initializer is the selected namespace's
// bundled Keycloak HTTPS Service. It is bound to loopback and always stopped.
func keycloakBootstrapForward(ctx context.Context, kubeContext, namespace string) (string, func(), error) {
	return serviceBootstrapForward(ctx, kubeContext, namespace, "ops-keycloak", 8443)
}

// Only fixed installation services can be tunneled. This is not a user Tool.
func serviceBootstrapForward(ctx context.Context, kubeContext, namespace, service string, remotePort int) (string, func(), error) {
	if !((service == "ops-keycloak" && remotePort == 8443) || (service == "ops-seaweedfs-s3" && remotePort == 8333) || (service == "ops-api" && remotePort == 8080)) {
		return "", nil, errors.New("unsupported fixed bootstrap service")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, errors.New("Keycloak bootstrap loopback unavailable")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	child, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(child, "kubectl", "--context", kubeContext, "--namespace", namespace, "port-forward", "--address", "127.0.0.1", "service/"+service, fmt.Sprintf("%d:%d", port, remotePort))
	if command.Start() != nil {
		cancel()
		return "", nil, errors.New("Keycloak bootstrap tunnel unavailable")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stop := func() { cancel(); <-done }
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	retry := time.NewTicker(200 * time.Millisecond)
	defer retry.Stop()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	for {
		select {
		case <-ctx.Done():
			stop()
			return "", nil, errors.New("Keycloak bootstrap cancelled")
		case <-timeout.C:
			stop()
			return "", nil, errors.New("Keycloak bootstrap tunnel timeout")
		case <-done:
			cancel()
			return "", nil, errors.New("Keycloak bootstrap tunnel failed")
		case <-retry.C:
			conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
			if err == nil {
				conn.Close()
				return address, stop, nil
			}
		}
	}
}
