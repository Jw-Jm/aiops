package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AppConfig contains the configuration required to construct either process.
type AppConfig struct {
	DatabaseURL              string
	OIDCIssuerURL            string
	ProfilePath              string
	HumanAuthMode            string
	NativeAuthTenantID       uuid.UUID
	NativeSessionIdleTTL     time.Duration
	NativeSessionAbsoluteTTL time.Duration
	NativeAuthPublicOrigin   string
}

const (
	HumanAuthModeOIDC   = "oidc"
	HumanAuthModeNative = "native"
	defaultIdleTTL      = 30 * time.Minute
	defaultAbsoluteTTL  = 12 * time.Hour
)

// ConfigFromEnv reads the minimal process configuration from the environment.
func ConfigFromEnv() AppConfig {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("HUMAN_AUTH_MODE")))
	if mode == "" {
		mode = HumanAuthModeOIDC
	}
	tenantID := uuid.Nil
	if rawTenant := os.Getenv("NATIVE_AUTH_TENANT_ID"); rawTenant != "" {
		parsed, err := uuid.Parse(rawTenant)
		if err != nil {
			tenantID = uuid.Nil
		} else {
			tenantID = parsed
		}
	}
	return AppConfig{
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		OIDCIssuerURL:            os.Getenv("OIDC_ISSUER_URL"),
		ProfilePath:              os.Getenv("PLATFORM_PROFILE"),
		HumanAuthMode:            mode,
		NativeAuthTenantID:       tenantID,
		NativeSessionIdleTTL:     durationFromEnv("NATIVE_SESSION_IDLE_TTL", defaultIdleTTL),
		NativeSessionAbsoluteTTL: durationFromEnv("NATIVE_SESSION_ABSOLUTE_TTL", defaultAbsoluteTTL),
		NativeAuthPublicOrigin:   strings.TrimRight(strings.TrimSpace(os.Getenv("NATIVE_AUTH_PUBLIC_ORIGIN")), "/"),
	}
}

func durationFromEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// ConfigError describes all required configuration fields that are absent.
type ConfigError struct {
	MissingFields []string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("missing required configuration: %v", e.MissingFields)
}

// APIApp is the platform API process skeleton.
type APIApp struct {
	config AppConfig
}

// WorkerApp is the platform worker process skeleton.
type WorkerApp struct {
	config AppConfig
}

// NewAPI validates configuration and constructs the API process skeleton.
func NewAPI(config AppConfig) (*APIApp, error) {
	if err := validateConfig(config, true); err != nil {
		return nil, err
	}
	return &APIApp{config: config}, nil
}

// NewWorker validates configuration and constructs the worker process skeleton.
// The worker never authenticates OIDC tokens, so OIDC_ISSUER_URL is not required.
func NewWorker(config AppConfig) (*WorkerApp, error) {
	if err := validateConfig(config, false); err != nil {
		return nil, err
	}
	return &WorkerApp{config: config}, nil
}

func validateConfig(config AppConfig, isAPI bool) error {
	mode := config.HumanAuthMode
	if mode == "" {
		mode = HumanAuthModeOIDC
	}
	native := mode == HumanAuthModeNative
	if mode != HumanAuthModeOIDC && mode != HumanAuthModeNative {
		return fmt.Errorf("HUMAN_AUTH_MODE must be %q or %q", HumanAuthModeOIDC, HumanAuthModeNative)
	}
	missing := make([]string, 0, 3)
	if config.DatabaseURL == "" {
		missing = append(missing, "database_url")
	}
	if isAPI && !native && config.OIDCIssuerURL == "" {
		missing = append(missing, "oidc_issuer_url")
	}
	if config.ProfilePath == "" {
		missing = append(missing, "profile_path")
	}
	if native && config.NativeAuthTenantID == uuid.Nil {
		missing = append(missing, "native_auth_tenant_id")
	}
	if len(missing) > 0 {
		return &ConfigError{MissingFields: missing}
	}
	return nil
}
