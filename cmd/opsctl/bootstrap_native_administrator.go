package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/identity"
)

// nativeAdministratorInput is a private, repository-external JSON document.
// The database URL is intentionally part of the same 0600 file; the initial
// password is always a separate 0600 file and is never accepted as a flag.
type nativeAdministratorInput struct {
	DatabaseURL string `json:"databaseUrl"`
	TenantSlug  string `json:"tenantSlug,omitempty"`
	TenantID    string `json:"tenantId,omitempty"`
	LoginName   string `json:"loginName"`
	DisplayName string `json:"displayName"`
}

func (input nativeAdministratorInput) validate() error {
	if input.DatabaseURL == "" {
		return errors.New("databaseUrl is required")
	}
	parsed, err := url.Parse(input.DatabaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Host == "" || parsed.User == nil || parsed.User.String() == "" {
		return errors.New("databaseUrl must be a PostgreSQL URL with a dedicated identity bootstrap LOGIN")
	}
	if (input.TenantSlug == "") == (input.TenantID == "") {
		return errors.New("specify exactly one of tenantSlug or tenantId")
	}
	if input.TenantID != "" {
		if _, err := uuid.Parse(input.TenantID); err != nil {
			return errors.New("tenantId must be a UUID")
		}
	}
	if identity.ValidateLoginName(identity.NormalizeLoginName(input.LoginName)) != nil {
		return errors.New("loginName is invalid")
	}
	if identity.ValidateDisplayName(input.DisplayName) != nil {
		return errors.New("displayName is invalid")
	}
	return nil
}

func runBootstrapNativeAdministrator(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap native-administrator", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "private 0600 bootstrap JSON outside the repository")
	passwordPath := flags.String("password-file", "", "private 0600 file containing initial password bytes without newline")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *inputPath == "" || *passwordPath == "" {
		return errors.New("native administrator bootstrap requires --input and --password-file")
	}
	rawInput, err := readPrivateExternalFile(*inputPath, 64<<10)
	if err != nil {
		return errors.New("native administrator input unavailable")
	}
	var config nativeAdministratorInput
	decoder := json.NewDecoder(strings.NewReader(string(rawInput)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || decoder.Decode(new(any)) != io.EOF || config.validate() != nil {
		return errors.New("native administrator input invalid")
	}
	password, err := readPrivatePasswordFile(*passwordPath)
	if err != nil {
		return errors.New("initial administrator password unavailable")
	}

	request := identity.FirstAdministratorInput{
		LoginName:   identity.NormalizeLoginName(config.LoginName),
		DisplayName: config.DisplayName,
		Password:    password,
	}
	if config.TenantSlug != "" {
		request.TenantSlug = strings.TrimSpace(config.TenantSlug)
	} else {
		tenantID, _ := uuid.Parse(config.TenantID)
		request.TenantID = tenantID
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
	result, err := repository.CreateFirstAdministrator(bounded, request)
	if err != nil {
		return mapNativeAdministratorError(err)
	}
	// The result intentionally contains identifiers, versions, and login only.
	// Password, password hash, database URL, and role membership are never returned.
	return json.NewEncoder(stdout).Encode(result)
}

func readPrivateExternalFile(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(path) {
		return nil, errors.New("private input must be a 0600 repository-external regular file")
	}
	return readBoundedFile(path, limit)
}

func readPrivatePasswordFile(path string) (string, error) {
	raw, err := readPrivateExternalFile(path, 4<<10)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || strings.ContainsAny(string(raw), "\r\n") {
		return "", errors.New("password file must contain password bytes without a newline")
	}
	if identity.ValidatePassword(string(raw)) != nil {
		return "", errors.New("password file has an invalid length")
	}
	return string(raw), nil
}

func mapNativeAdministratorError(err error) error {
	switch {
	case errors.Is(err, identity.ErrAlreadyInitialized):
		return errors.New("administrator already initialized; existing account was not changed")
	case errors.Is(err, identity.ErrBootstrapDuty):
		return errors.New("identity bootstrap requires a dedicated NOINHERIT migration-duty LOGIN; ordinary CLI/API roles are refused")
	case errors.Is(err, identity.ErrTenantUnavailable):
		return errors.New("bootstrap tenant is unavailable or disabled")
	case errors.Is(err, identity.ErrLoginNameUnavailable):
		return errors.New("login name is unavailable")
	case errors.Is(err, identity.ErrPasswordInvalid):
		return errors.New("initial password has an invalid length")
	case errors.Is(err, identity.ErrLoginNameInvalid):
		return errors.New("login name is invalid")
	case errors.Is(err, identity.ErrDisplayName):
		return errors.New("display name is invalid")
	default:
		return fmt.Errorf("native administrator initialization failed: %w", err)
	}
}
