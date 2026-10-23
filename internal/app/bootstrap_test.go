package app

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestBootstrapRequiresConfig(t *testing.T) {
	validConfig := AppConfig{
		DatabaseURL:   "postgres://platform:secret@localhost/platform",
		OIDCIssuerURL: "https://identity.example.test",
		ProfilePath:   "deploy/profiles/dev-orbstack.yaml",
	}

	constructors := []struct {
		name     string
		new      func(AppConfig) (any, error)
		requires []string
	}{
		{name: "API", new: func(config AppConfig) (any, error) { return NewAPI(config) }, requires: []string{"database_url", "oidc_issuer_url", "profile_path"}},
		// The worker never verifies OIDC tokens, so it must not require the issuer.
		{name: "worker", new: func(config AppConfig) (any, error) { return NewWorker(config) }, requires: []string{"database_url", "profile_path"}},
	}
	missingFields := []struct {
		name  string
		field string
		clear func(*AppConfig)
	}{
		{name: "database", field: "database_url", clear: func(config *AppConfig) { config.DatabaseURL = "" }},
		{name: "OIDC", field: "oidc_issuer_url", clear: func(config *AppConfig) { config.OIDCIssuerURL = "" }},
		{name: "profile", field: "profile_path", clear: func(config *AppConfig) { config.ProfilePath = "" }},
	}

	for _, constructor := range constructors {
		for _, missing := range missingFields {
			required := false
			for _, field := range constructor.requires {
				if field == missing.field {
					required = true
				}
			}
			t.Run(constructor.name+"/missing_"+missing.name, func(t *testing.T) {
				config := validConfig
				missing.clear(&config)

				_, err := constructor.new(config)
				if !required {
					if err != nil {
						t.Fatalf("field %q must not be required for %s: %v", missing.field, constructor.name, err)
					}
					return
				}
				if err == nil {
					t.Fatal("expected a structured configuration error")
				}

				var configErr *ConfigError
				if !errors.As(err, &configErr) {
					t.Fatalf("expected *ConfigError, got %T: %v", err, err)
				}
				if len(configErr.MissingFields) != 1 {
					t.Fatalf("expected one missing field, got %v", configErr.MissingFields)
				}
				if configErr.MissingFields[0] != missing.field {
					t.Fatalf("expected missing field %q, got %q", missing.field, configErr.MissingFields[0])
				}
			})
		}
	}
}

// Constructor validation makes no network or database calls; this is not a cold-start acceptance.
func TestNativeBootstrapDoesNotRequireOIDC(t *testing.T) {
	config := AppConfig{DatabaseURL: "postgres://fixture@localhost/not-used", ProfilePath: "not-opened-by-constructor", HumanAuthMode: HumanAuthModeNative, NativeAuthTenantID: uuid.New()}
	if _, err := NewAPI(config); err != nil {
		t.Fatalf("native API required issuer: %v", err)
	}
	if _, err := NewWorker(config); err != nil {
		t.Fatalf("native Worker required issuer: %v", err)
	}
	config.NativeAuthTenantID = uuid.Nil
	if _, err := NewAPI(config); err == nil {
		t.Fatal("native API accepted an unknown deployment tenant")
	}
	config.HumanAuthMode = "typo"
	if _, err := NewAPI(config); err == nil {
		t.Fatal("unknown identity mode fell back to OIDC")
	}
}
