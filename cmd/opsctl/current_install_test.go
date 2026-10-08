package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// This failure must precede Profile reads, signature import, Helm and DB access.
// Otherwise a nominal current install can silently deliver a disabled runtime.
func TestCurrentInstallRequiresExplicitBusinessValues(t *testing.T) {
	err := run(context.Background(), []string{"install", "--profile", "core", "--resolved", "missing-profile.yaml", "--bundle", "missing-bundle", "--key", "missing-trust.pem", "--offline"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--business-values") {
		t.Fatalf("missing current business configuration was not rejected before environment access: %v", err)
	}
}

func TestCurrentInstallRequiresExplicitInitializationStage(t *testing.T) {
	err := run(context.Background(), []string{"install", "--profile", "core", "--resolved", "missing-profile.yaml", "--bundle", "missing-bundle", "--key", "missing-trust.pem", "--business-values", "missing-business.yaml", "--offline"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--stage") {
		t.Fatalf("unphased current install reached environment access: %v", err)
	}
}

func TestCurrentInstallAcceptsExplicitAPIBootstrapStage(t *testing.T) {
	err := run(context.Background(), []string{"install", "--profile", "core", "--resolved", "missing-profile.yaml", "--bundle", "missing-bundle", "--key", "missing-trust.pem", "--business-values", "missing-business.yaml", "--stage", "bootstrap-api", "--offline"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "business values file unavailable") {
		t.Fatalf("formal API bootstrap stage is unavailable: %v", err)
	}
}

func TestGraphLeaseBootstrapHasFormalCLIEntry(t *testing.T) {
	err := run(context.Background(), []string{"bootstrap", "graph-leases"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--profile") {
		t.Fatalf("formal Graph Lease initialization unavailable: %v", err)
	}
}

func TestDatabaseBootstrapHasFormalCLIEntry(t *testing.T) {
	err := run(context.Background(), []string{"bootstrap", "database-logins"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--secrets-file") {
		t.Fatalf("database initialization is not exposed by the formal CLI: %v", err)
	}
}

func TestOIDCBootstrapHasFormalCLIEntry(t *testing.T) {
	err := run(context.Background(), []string{"bootstrap", "oidc-realm"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--profile") {
		t.Fatalf("OIDC initialization is not exposed by the formal CLI: %v", err)
	}
}
