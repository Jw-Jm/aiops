//go:build pre_sp07_live

package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/bootstrap"
	"ops-platform/internal/integrations/keycloak"
	"ops-platform/internal/profile"
)

func formalOIDCIssuer(t *testing.T, privateDir string) string {
	t.Helper()
	p, err := profile.ReadResolvedProfileFile(filepath.Join(privateDir, "resolved-profile.yaml"))
	if err != nil {
		t.Fatal("actual installation's resolved Profile required")
	}
	component := p.Components["keycloak"]
	u, err := url.Parse(component.Endpoint)
	if err != nil || component.Mode != "bundled" || component.Namespace == "" || u.Scheme != "https" || u.Hostname() != "ops-keycloak."+component.Namespace+".svc.cluster.local" || u.Port() != "8443" {
		t.Fatal("actual bundled HTTPS Keycloak identity required")
	}
	return strings.TrimSuffix(component.Endpoint, "/") + "/realms/ops"
}

func freshFormalOIDCLogin(t *testing.T) (keycloak.TokenSet, bootstrap.OIDCInput, bootstrap.OIDCPrivateInput) {
	t.Helper()
	privateDir := os.Getenv("PRE_SP07_OIDC_PRIVATE_DIR")
	kcDial := os.Getenv("PRE_SP07_OIDC_LOOPBACK")
	if privateDir == "" || kcDial == "" {
		t.Fatal("actual new HTTPS Keycloak and independently prepared private inputs required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var input bootstrap.OIDCInput
	var private bootstrap.OIDCPrivateInput
	decode := func(name string, value any) {
		raw, err := os.ReadFile(filepath.Join(privateDir, name))
		if err != nil || json.Unmarshal(raw, value) != nil {
			t.Fatal("private bootstrap inputs unavailable")
		}
	}
	decode("oidc-input.json", &input)
	decode("oidc-private.json", &private)
	callback, _ := url.Parse(input.CallbackURL)
	issuer := formalOIDCIssuer(t, privateDir)
	issuerURL, _ := url.Parse(issuer)
	caPEM, err := os.ReadFile(filepath.Join(privateDir, "oidc-ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("public OIDC trust invalid")
	}
	block, _ := pem.Decode(caPEM)
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM, err := os.ReadFile(filepath.Join(privateDir, "ops-keycloak-ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	keyBlock, _ := pem.Decode(privatePEM)
	signer, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	receiverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	leaf := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: callback.Hostname()}, DNSNames: []string{callback.Hostname()}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &receiverKey.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	code := make(chan url.Values, 1)
	receiver := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != callback.Path {
			http.NotFound(w, r)
			return
		}
		code <- r.URL.Query()
		w.WriteHeader(http.StatusNoContent)
	}))
	receiver.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: receiverKey}}}
	receiver.StartTLS()
	defer receiver.Close()
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		address := ""
		if target == issuerURL.Host {
			address = kcDial
		} else if target == callback.Host+":443" {
			address = receiver.Listener.Addr().String()
		} else {
			return nil, bootstrap.ErrIdentity
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Transport: transport, Jar: jar, Timeout: 10 * time.Second}
	trusted := oidc.ClientContext(ctx, browser)
	client, err := keycloak.NewClient(trusted, keycloak.Config{IssuerURL: issuer, ClientID: "ops-web", ClientSecret: private.ClientSecret, RedirectURL: input.CallbackURL, StepUpACR: auth.StepUpACRLevel2})
	if err != nil {
		t.Fatal("real HTTPS OIDC discovery failed")
	}
	flow, err := client.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	response, err := browser.Get(flow.AuthorizationURL)
	if err != nil {
		t.Fatal("real authorization request failed")
	}
	otpSecret := ""
	if saved, err := os.ReadFile(filepath.Join(privateDir, "enrolled-otp.secret")); err == nil {
		otpSecret = string(saved)
	}
	var tokens keycloak.TokenSet
	completed := false
	for attempt := 0; attempt < 8; attempt++ {
		if response.Request.URL.Host == callback.Host && response.Request.URL.Path == callback.Path {
			response.Body.Close()
			select {
			case result := <-code:
				tokens, err = client.Exchange(trusted, result.Get("code"), result.Get("state"), flow)
			case <-ctx.Done():
				t.Fatal("real callback timeout")
			}
			if err != nil {
				t.Fatal("PKCE/nonce/LoA-2 exchange rejected")
			}
			completed = true
			break
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		form, found := findLoginForm(body)
		setup := false
		if !found {
			form, otpSecret, found = findOTPSetupForm(body)
			setup = true
		}
		if !found {
			t.Fatalf("real Keycloak returned no recognized login form: %s", keycloakPageSummary(body))
		}
		if setup {
			form.fields.Set("userLabel", "Pre-SP07 actual bootstrap enrollment")
			form.fields.Set("totp", currentTOTP(t, otpSecret, time.Now()))
		} else if form.fields.Has("username") {
			form.fields.Set("username", input.Username)
			form.fields.Set("password", private.SubjectPassword)
		} else if form.fields.Has("otp") {
			form.fields.Set("otp", currentTOTP(t, otpSecret, time.Now()))
		} else {
			t.Fatal("unexpected real login form")
		}
		request, _ := http.NewRequestWithContext(ctx, "POST", form.action, strings.NewReader(form.fields.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err = browser.Do(request)
		if err != nil {
			t.Fatal("real credential/OTP submission failed")
		}
	}
	if !completed || tokens.ACR != auth.StepUpACRLevel2 || tokens.TenantID != input.TenantID.String() || tokens.AuthTime.IsZero() {
		t.Fatal("real step-up or tenant claims missing")
	}
	// Runtime tokens and OTP secrets stay only in this external controlled directory.
	if err := os.WriteFile(filepath.Join(privateDir, "fresh-loa2.token"), []byte(tokens.AccessToken), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateDir, "enrolled-otp.secret"), []byte(otpSecret), 0600); err != nil {
		t.Fatal(err)
	}
	return tokens, input, private
}

func TestFormalCurrentInstallationOperatorLogin(t *testing.T) {
	// A second real login in the same TOTP period must not reuse an accepted
	// one-time code. Keep Keycloak's replay protection enabled and wait for a
	// new code, as the existing post-retirement login gate does.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	waitForNextTOTPPeriod(t, ctx)
	tokens, input, _ := freshFormalOIDCLogin(t)
	var first bootstrap.FirstTenant
	raw, err := os.ReadFile(filepath.Join(os.Getenv("PRE_SP07_OIDC_PRIVATE_DIR"), "first-tenant.json"))
	if err != nil || json.Unmarshal(raw, &first) != nil {
		t.Fatal("previous formal first-tenant receipt required")
	}
	if tokens.Subject != first.AdminSubject || input.TenantID != first.TenantID || tokens.AuthTime.Before(time.Now().Add(-time.Minute)) {
		t.Fatal("fresh LoA-2 operator identity differs from installed tenant administrator")
	}
	t.Log("fresh actual HTTPS Keycloak OTP/PKCE/nonce/LoA-2 login; installed operator and tenant matched; no business table mutation")
}

// The current installation has already completed the separately evidenced
// signed Linux migration flow. This gate uses the verified release installer
// and the formal bootstrap API; it never builds an alternate installer or seeds
// business tables in the acceptance harness.
func TestFormalCurrentInstallationFirstTenant(t *testing.T) {
	tokens, input, _ := freshFormalOIDCLogin(t)
	privateDir := os.Getenv("PRE_SP07_OIDC_PRIVATE_DIR")
	lockedInstaller := os.Getenv("PRE_SP07_LOCKED_OPSCTL")
	expectedSHA256 := os.Getenv("PRE_SP07_LOCKED_OPSCTL_SHA256")
	if !filepath.IsAbs(lockedInstaller) || len(expectedSHA256) != 64 {
		t.Fatal("independently verified release installer identity required")
	}
	raw, err := os.ReadFile(lockedInstaller)
	if err != nil {
		t.Fatal("locked installer unavailable")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != expectedSHA256 {
		t.Fatal("locked installer bytes differ from verified release")
	}
	dsn := os.Getenv("PRE_SP07_OIDC_DATABASE_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("new migrated database unavailable")
	}
	defer conn.Close(ctx)
	var version, tenantCount int
	if conn.QueryRow(ctx, `SELECT (SELECT max(version_id) FROM public.goose_db_version WHERE is_applied),(SELECT count(*) FROM platform.tenants)`).Scan(&version, &tenantCount) != nil || version != 35 || tenantCount != 0 {
		t.Fatal("actual migrated empty business database required")
	}
	first := bootstrap.FirstTenant{TenantID: input.TenantID, Slug: "pre-sp07-current", DisplayName: "Current nonvirtual readiness", AdminSubject: tokens.Subject}
	raw, _ = json.Marshal(first)
	firstPath := filepath.Join(privateDir, "first-tenant.json")
	if os.WriteFile(firstPath, raw, 0600) != nil {
		t.Fatal("explicit first tenant input unavailable")
	}
	p, err := profile.ReadResolvedProfileFile(filepath.Join(privateDir, "resolved-profile.yaml"))
	if err != nil {
		t.Fatal("actual installation profile required")
	}
	issuer := strings.TrimSuffix(p.Components["keycloak"].Endpoint, "/") + "/realms/ops"
	args := []string{"bootstrap", "first-tenant", "--config", firstPath, "--issuer", issuer, "--ca", filepath.Join(privateDir, "oidc-ca.pem"), "--token-file", filepath.Join(privateDir, "fresh-loa2.token"), "--profile", filepath.Join(privateDir, "resolved-profile.yaml")}
	invoke := func() ([]byte, error) {
		command := exec.CommandContext(ctx, lockedInstaller, args...)
		command.Env = append(os.Environ(), "OPS_BOOTSTRAP_DATABASE_URL="+dsn)
		return command.CombinedOutput()
	}
	output, err := invoke()
	if err != nil {
		os.WriteFile(filepath.Join(privateDir, "current-first-tenant-private-error.log"), output, 0600)
		t.Fatal("formal release first-tenant initializer failed; private original retained")
	}
	if output, err = invoke(); err == nil || !strings.Contains(string(output), "BOOTSTRAP_ALREADY_INITIALIZED") {
		t.Fatal("repeated initialization did not fail closed")
	}
	var bindings, audits int
	if conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM platform.tenants),(SELECT count(*) FROM platform.role_bindings WHERE role_name='platform_admin' AND cluster_scopes='[]' AND namespace_scopes='[]'),(SELECT count(*) FROM audit.records WHERE event_type='installation.first_tenant_bootstrapped')`).Scan(&tenantCount, &bindings, &audits) != nil || tenantCount != 1 || bindings != 1 || audits != 1 {
		t.Fatal("first tenant default scope or atomic Audit differs")
	}
	t.Log("current signed dependency installation: actual HTTPS PKCE/nonce/OTP LoA-2, verified release first-tenant initializer, empty migrated business database, atomic Audit and duplicate rejection; no business seed")
}

func TestFormalHTTPSOIDCAndFirstTenantBootstrap(t *testing.T) {
	tokens, input, _ := freshFormalOIDCLogin(t)
	privateDir := os.Getenv("PRE_SP07_OIDC_PRIVATE_DIR")
	dsn := os.Getenv("PRE_SP07_OIDC_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("actual isolated PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	issuer := formalOIDCIssuer(t, privateDir)
	decode := func(name string, value any) {
		raw, err := os.ReadFile(filepath.Join(privateDir, name))
		if err != nil || json.Unmarshal(raw, value) != nil {
			t.Fatal("private bootstrap inputs unavailable")
		}
	}
	first := bootstrap.FirstTenant{TenantID: input.TenantID, Slug: "pre-sp07-oidc-bootstrap", DisplayName: "Pre-SP07 actual bootstrap", AdminSubject: tokens.Subject}
	raw, _ := json.Marshal(first)
	firstPath := filepath.Join(privateDir, "first-tenant.json")
	os.WriteFile(firstPath, raw, 0600)
	binary := filepath.Join(privateDir, "opsctl-current")
	migrator := filepath.Join(privateDir, "db-migrate-current")
	build := func(target, pkg string) {
		cmd := exec.CommandContext(ctx, "go", "build", "-o", target, pkg)
		cmd.Dir = "../.."
		if cmd.Run() != nil {
			t.Fatal("formal tool build failed")
		}
	}
	build(binary, "./cmd/opsctl")
	build(migrator, "./cmd/db-migrate")
	adminState, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("new isolated PostgreSQL unavailable")
	}
	var version int64
	err = adminState.QueryRow(ctx, `SELECT COALESCE((SELECT max(version_id) FROM public.goose_db_version WHERE is_applied),0)`).Scan(&version)
	if err != nil {
		version = 0
	}
	adminState.Close(ctx)
	var logins bootstrap.DatabaseLogins
	var migration *exec.Cmd
	if version == 0 {
		migration = exec.CommandContext(ctx, migrator, "-dir", "migrations", "-to", "1")
		migration.Dir = "../.."
		migration.Env = append(os.Environ(), "SP03_MIGRATION_DATABASE_URL="+dsn)
		if migration.Run() != nil {
			t.Fatal("formal version 1 migration failed")
		}
		suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
		logins = bootstrap.DatabaseLogins{SchemaVersion: 1, Migration: bootstrap.DatabaseLogin{Name: "m_" + suffix, Password: uuid.NewString() + uuid.NewString()}, API: bootstrap.DatabaseLogin{Name: "a_" + suffix, Password: uuid.NewString() + uuid.NewString()}, Worker: bootstrap.DatabaseLogin{Name: "w_" + suffix, Password: uuid.NewString() + uuid.NewString()}}
		raw, _ := json.Marshal(logins)
		loginPath := filepath.Join(privateDir, "database-logins.json")
		os.WriteFile(loginPath, raw, 0600)
		command := exec.CommandContext(ctx, binary, "bootstrap", "database-logins", "--secrets-file", loginPath)
		command.Env = append(os.Environ(), "OPS_BOOTSTRAP_DATABASE_URL="+dsn)
		if command.Run() != nil {
			t.Fatal("formal database LOGIN initializer failed")
		}
	} else if version == 1 || version == 35 {
		decode("database-logins.json", &logins)
		if logins.Validate() != nil {
			t.Fatal("retained successful LOGIN inputs invalid")
		}
		t.Log("resuming after committed bootstrap step; existing database and LOGIN identities retained")
	} else {
		t.Fatal("unexpected isolated migration state")
	}
	migrationURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	migrationURL.User = url.UserPassword(logins.Migration.Name, logins.Migration.Password)
	query := migrationURL.Query()
	query.Set("options", "-c role=migration_role")
	migrationURL.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	probe, err := pgx.Connect(ctx, migrationURL.String())
	if err != nil {
		os.WriteFile(filepath.Join(privateDir, "migration-login-private-error.log"), []byte(err.Error()), 0600)
		t.Fatal("restricted migration LOGIN unavailable; private error retained")
	}
	var sessionUser, currentRole string
	if err := probe.QueryRow(ctx, `SELECT session_user,current_user`).Scan(&sessionUser, &currentRole); err != nil || sessionUser != logins.Migration.Name || currentRole != "migration_role" {
		t.Fatal("migration connection used the wrong identity")
	}
	probe.Close(ctx)
	migration = exec.CommandContext(ctx, migrator, "-dir", "migrations")
	migration.Dir = "../.."
	migration.Env = append(os.Environ(), "SP03_MIGRATION_DATABASE_URL="+migrationURL.String())
	if migration.Run() != nil {
		t.Fatal("formal restricted forward migration failed")
	}
	args := []string{"bootstrap", "first-tenant", "--config", firstPath, "--issuer", issuer, "--ca", filepath.Join(privateDir, "oidc-ca.pem"), "--token-file", filepath.Join(privateDir, "fresh-loa2.token"), "--profile", filepath.Join(privateDir, "resolved-profile.yaml")}
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = append(os.Environ(), "OPS_BOOTSTRAP_DATABASE_URL="+dsn)
	output, err := command.CombinedOutput()
	if err != nil {
		os.WriteFile(filepath.Join(privateDir, "first-tenant-private-error.log"), output, 0600)
		t.Fatal("formal first-tenant CLI failed after real HTTPS PKCE/OTP; private original error retained")
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var tenants, bindings, audits int
	if err := admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM platform.tenants),(SELECT count(*) FROM platform.role_bindings WHERE role_name='platform_admin' AND cluster_scopes='[]' AND namespace_scopes='[]'),(SELECT count(*) FROM audit.records WHERE event_type='installation.first_tenant_bootstrapped')`).Scan(&tenants, &bindings, &audits); err != nil || tenants != 1 || bindings != 1 || audits != 1 {
		t.Fatal("atomic bootstrap/default operating scope differs")
	}
	t.Log("actual HTTPS discovery, frozen realm, PKCE, nonce, OTP enrollment, LoA-2, tenant claims, formal forward migrations and opsctl first-tenant atomic Audit verified; initialized database retained")
}
