package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/bootstrap"
)

func runBootstrapTenant(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap first-tenant", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "explicit FirstTenant JSON, outside Bundle")
	issuer := flags.String("issuer", "", "real Keycloak HTTPS issuer")
	caPath := flags.String("ca", "", "independently trusted issuer CA")
	profilePath := flags.String("profile", "", "resolved Profile for current bundled Keycloak HTTPS routing")
	tokenPath := flags.String("token-file", "", "private fresh LoA-2 bearer token; never passed as an argument")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *configPath == "" || *issuer == "" || *caPath == "" || *tokenPath == "" {
		return errors.New("bootstrap requires --config, --issuer, --ca and --token-file")
	}
	raw, err := readBoundedFile(*configPath, 64<<10)
	if err != nil {
		return errors.New("first tenant input unavailable")
	}
	var config bootstrap.FirstTenant
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || config.Validate() != nil {
		return errors.New("first tenant input invalid; no default identity or scope")
	}
	u, err := url.Parse(*issuer)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("bootstrap issuer requires credential-free HTTPS")
	}
	ca, err := readBoundedFile(*caPath, 1<<20)
	if err != nil {
		return errors.New("independent issuer CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) || bytes.Contains(ca, []byte("PRIVATE KEY")) {
		return errors.New("independent issuer public CA invalid")
	}
	info, err := os.Stat(*tokenPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(*tokenPath) {
		return errors.New("bootstrap bearer must be a private repository-external file")
	}
	token, err := readBoundedFile(*tokenPath, 64<<10)
	if err != nil {
		return errors.New("private bootstrap bearer unavailable")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer transport.CloseIdleConnections()
	if *profilePath != "" {
		p, err := readResolvedProfile(*profilePath)
		if err != nil {
			return err
		}
		component := p.Components["keycloak"]
		if *issuer != strings.TrimSuffix(component.Endpoint, "/")+"/realms/ops" {
			return errors.New("bootstrap issuer differs from resolved Profile")
		}
		if component.Mode == "bundled" {
			if component.Namespace == "" || u.Hostname() != "ops-keycloak."+component.Namespace+".svc.cluster.local" || u.Port() != "8443" {
				return errors.New("bundled bootstrap requires the exact Keycloak namespace/DNS/port")
			}
			address, stop, err := keycloakBootstrapForward(ctx, p.Kubernetes.Context, component.Namespace)
			if err != nil {
				return err
			}
			defer stop()
			transport.TLSClientConfig.ServerName = u.Hostname()
			transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
				if target != u.Host {
					return nil, errors.New("OIDC bootstrap route outside pinned issuer")
				}
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
			}
		} else if component.Mode != "external" {
			return errors.New("active Keycloak identity required")
		}
	} else if strings.HasSuffix(u.Hostname(), ".svc.cluster.local") {
		return errors.New("bundled bootstrap requires --profile for exact target routing")
	}
	httpClient := &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx = oidc.ClientContext(ctx, httpClient)
	provider, err := oidc.NewProvider(ctx, *issuer)
	if err != nil {
		return errors.New("bootstrap OIDC discovery failed")
	}
	verified, err := provider.VerifierContext(ctx, &oidc.Config{ClientID: "ops-api", SupportedSigningAlgs: []string{"RS256"}}).Verify(ctx, strings.TrimSpace(string(token)))
	if err != nil {
		return errors.New("bootstrap OIDC token rejected")
	}
	if err = verifyBootstrapIdentity(verified, config, time.Now()); err != nil {
		return err
	}
	dsn := os.Getenv("OPS_BOOTSTRAP_DATABASE_URL")
	if dsn == "" {
		return errors.New("separate OPS_BOOTSTRAP_DATABASE_URL is required")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.New("bootstrap database unavailable")
	}
	defer conn.Close(ctx)
	binding, err := bootstrap.ProvisionFirstTenant(ctx, conn, config)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"tenantId": config.TenantID, "initialAdminBindingId": binding, "role": "platform_admin", "scope": "administration only; explicit operator scopes via API"})
}
func verifyBootstrapIdentity(token *oidc.IDToken, c bootstrap.FirstTenant, now time.Time) error {
	if token == nil || token.Subject != c.AdminSubject || !now.Before(token.Expiry) {
		return bootstrap.ErrIdentity
	}
	var claims struct {
		TenantID  string   `json:"tenant_id"`
		TenantIDs []string `json:"tenant_ids"`
		ACR       string   `json:"acr"`
		AuthTime  int64    `json:"auth_time"`
	}
	if token.Claims(&claims) != nil || claims.ACR != auth.StepUpACRLevel2 || claims.AuthTime <= 0 || claims.AuthTime > now.Unix()+30 || now.Unix()-claims.AuthTime > 3600 {
		return bootstrap.ErrIdentity
	}
	ids := append(claims.TenantIDs, claims.TenantID)
	for _, value := range ids {
		if id, err := uuid.Parse(value); err == nil && id == c.TenantID {
			return nil
		}
	}
	return bootstrap.ErrIdentity
}
