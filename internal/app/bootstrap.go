package app

import (
	"fmt"
	"os"
)

// AppConfig contains the configuration required to construct either process.
type AppConfig struct {
	DatabaseURL   string
	OIDCIssuerURL string
	ProfilePath   string
}

// ConfigFromEnv reads the minimal process configuration from the environment.
func ConfigFromEnv() AppConfig {
	return AppConfig{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		OIDCIssuerURL: os.Getenv("OIDC_ISSUER_URL"),
		ProfilePath:   os.Getenv("PLATFORM_PROFILE"),
	}
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
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &APIApp{config: config}, nil
}

// NewWorker validates configuration and constructs the worker process skeleton.
func NewWorker(config AppConfig) (*WorkerApp, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &WorkerApp{config: config}, nil
}

func validateConfig(config AppConfig) error {
	missing := make([]string, 0, 3)
	if config.DatabaseURL == "" {
		missing = append(missing, "database_url")
	}
	if config.OIDCIssuerURL == "" {
		missing = append(missing, "oidc_issuer_url")
	}
	if config.ProfilePath == "" {
		missing = append(missing, "profile_path")
	}
	if len(missing) > 0 {
		return &ConfigError{MissingFields: missing}
	}
	return nil
}
