package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/identity"
)

// nativeFirstTenantInput is a private, repository-external JSON document for the
// native cold start. It carries no OIDC issuer, CA or bearer token: the native
// first tenant never depends on an identity provider.
type nativeFirstTenantInput struct {
	DatabaseURL      string `json:"databaseUrl"`
	TenantSlug       string `json:"tenantSlug"`
	TenantDisplay    string `json:"tenantDisplayName"`
	AdminLoginName   string `json:"adminLoginName"`
	AdminDisplayName string `json:"adminDisplayName"`
}

func (input nativeFirstTenantInput) validate() error {
	if input.DatabaseURL == "" {
		return errors.New("databaseUrl is required")
	}
	parsed, err := url.Parse(input.DatabaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Host == "" || parsed.User == nil || parsed.User.String() == "" {
		return errors.New("databaseUrl must be a PostgreSQL URL with a dedicated identity bootstrap LOGIN")
	}
	if identity.ValidateLoginName(identity.NormalizeLoginName(input.AdminLoginName)) != nil {
		return errors.New("adminLoginName is invalid")
	}
	if identity.ValidateDisplayName(strings.TrimSpace(input.AdminDisplayName)) != nil {
		return errors.New("adminDisplayName is invalid")
	}
	if strings.TrimSpace(input.TenantDisplay) == "" || len(strings.TrimSpace(input.TenantDisplay)) > 200 {
		return errors.New("tenantDisplayName is invalid")
	}
	if strings.TrimSpace(input.TenantSlug) == "" {
		return errors.New("tenantSlug is required")
	}
	return nil
}

// runBootstrapFirstTenantNative creates the first tenant and its first native
// administrator without any OIDC dependency. The tenant, account, credential,
// platform_admin grant, marker and audit records commit atomically under the
// dedicated NOINHERIT migration-duty LOGIN.
func runBootstrapFirstTenantNative(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap first-tenant-native", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "private 0600 bootstrap JSON outside the repository")
	passwordPath := flags.String("password-file", "", "private 0600 file containing initial password bytes without newline")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *inputPath == "" || *passwordPath == "" {
		return errors.New("native first tenant bootstrap requires --input and --password-file")
	}
	rawInput, err := readPrivateExternalFile(*inputPath, 64<<10)
	if err != nil {
		return errors.New("native first tenant input unavailable")
	}
	var config nativeFirstTenantInput
	decoder := json.NewDecoder(strings.NewReader(string(rawInput)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || decoder.Decode(new(any)) != io.EOF || config.validate() != nil {
		return errors.New("native first tenant input invalid")
	}
	password, err := readPrivatePasswordFile(*passwordPath)
	if err != nil {
		return errors.New("initial administrator password unavailable")
	}

	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(bounded, config.DatabaseURL)
	if err != nil {
		return errors.New("identity bootstrap database unavailable")
	}
	defer pool.Close()
	if err := identity.RequireNativeBootstrapDuty(bounded, pool); err != nil {
		return errors.New("identity bootstrap requires a dedicated NOINHERIT migration-duty LOGIN; ordinary CLI/API roles are refused")
	}
	repository, err := identity.NewRepository(pool)
	if err != nil {
		return errors.New("identity repository unavailable")
	}
	result, err := repository.CreateFirstTenant(bounded, identity.FirstTenantInput{
		Slug:             config.TenantSlug,
		DisplayName:      config.TenantDisplay,
		AdminLoginName:   config.AdminLoginName,
		AdminDisplayName: config.AdminDisplayName,
		Password:         password,
	})
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrTenantExists):
			return errors.New("tenant slug is already initialized; existing tenant was not changed")
		case errors.Is(err, identity.ErrBootstrapDuty):
			return errors.New("identity bootstrap requires a dedicated NOINHERIT migration-duty LOGIN; ordinary CLI/API roles are refused")
		case errors.Is(err, identity.ErrTenantUnavailable):
			return errors.New("first tenant input is invalid")
		case errors.Is(err, identity.ErrLoginNameUnavailable):
			return errors.New("login name is unavailable")
		case errors.Is(err, identity.ErrPasswordInvalid):
			return errors.New("initial password is invalid")
		case errors.Is(err, identity.ErrAlreadyInitialized):
			return errors.New("administrator already initialized; existing account was not changed")
		}
		return errors.New("native first tenant bootstrap failed")
	}
	// Only identifiers, the login and versions are returned; password, hash,
	// database URL and role membership never leave the process.
	return json.NewEncoder(stdout).Encode(map[string]any{
		"tenantId": result.TenantID, "slug": config.TenantSlug,
		"userId": result.Administrator.UserID, "loginName": result.Administrator.LoginName,
		"accountRevision":      result.Administrator.AccountRevision,
		"credentialVersion":    result.Administrator.CredentialVersion,
		"authorizationVersion": result.Administrator.AuthorizationVersion,
		"sessionVersion":       result.Administrator.SessionVersion,
		"initialState":         identity.PasswordChangeRequired,
	})
}
