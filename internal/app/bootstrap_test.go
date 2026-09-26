package app

import (
	"errors"
	"testing"
)

func TestBootstrapRequiresConfig(t *testing.T) {
	validConfig := AppConfig{
		DatabaseURL:   "postgres://platform:secret@localhost/platform",
		OIDCIssuerURL: "https://identity.example.test",
		ProfilePath:   "deploy/profiles/dev-orbstack.yaml",
	}

	constructors := []struct {
		name string
		new  func(AppConfig) (any, error)
	}{
		{name: "API", new: func(config AppConfig) (any, error) { return NewAPI(config) }},
		{name: "worker", new: func(config AppConfig) (any, error) { return NewWorker(config) }},
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
			t.Run(constructor.name+"/missing_"+missing.name, func(t *testing.T) {
				config := validConfig
				missing.clear(&config)

				_, err := constructor.new(config)
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
