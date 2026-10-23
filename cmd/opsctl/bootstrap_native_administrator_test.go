package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeAdministratorInputRejectsAmbiguityAndNonPostgresTargets(t *testing.T) {
	base := nativeAdministratorInput{
		DatabaseURL: "postgresql://identity_bootstrap_login@127.0.0.1:15432/ops",
		LoginName:   "root-admin",
		DisplayName: "Root Administrator",
	}
	none := base
	if err := none.validate(); err == nil || !strings.Contains(err.Error(), "tenantSlug") {
		t.Fatalf("missing tenant error = %v", err)
	}
	both := base
	both.TenantSlug = "primary"
	both.TenantID = "018f0f2b-91c2-7d42-a8dc-f719c5987201"
	if err := both.validate(); err == nil {
		t.Fatal("both tenant selectors accepted")
	}
	slug := base
	slug.TenantSlug = "primary"
	if err := slug.validate(); err != nil {
		t.Fatalf("tenant slug rejected: %v", err)
	}
	id := base
	id.TenantID = "018f0f2b-91c2-7d42-a8dc-f719c5987201"
	if err := id.validate(); err != nil {
		t.Fatalf("tenant id rejected: %v", err)
	}
	http := base
	http.TenantSlug = "primary"
	http.DatabaseURL = "https://127.0.0.1/database"
	if err := http.validate(); err == nil {
		t.Fatal("non-PostgreSQL database URL accepted")
	}
}

func writePrivateFile(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrivatePasswordFileRejectsModesNewlinesAndBounds(t *testing.T) {
	private := writePrivateFile(t, "password", "initial-password-value", 0o600)
	value, err := readPrivatePasswordFile(private)
	if err != nil || value != "initial-password-value" {
		t.Fatalf("private password read value=%q err=%v", value, err)
	}
	shared := writePrivateFile(t, "shared", "initial-password-value", 0o644)
	if _, err := readPrivatePasswordFile(shared); err == nil {
		t.Fatal("shared password file accepted")
	}
	newline := writePrivateFile(t, "newline", "initial-password-value\n", 0o600)
	if _, err := readPrivatePasswordFile(newline); err == nil {
		t.Fatal("password file with newline accepted")
	}
	short := writePrivateFile(t, "short", "short-passw", 0o600)
	if _, err := readPrivatePasswordFile(short); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestNativeAdministratorCommandRequiresPrivateFlagsAndDoesNotEchoPassword(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runBootstrapNativeAdministrator(context.Background(), []string{}, &stdout, &stderr); err == nil {
		t.Fatal("command accepted missing flags")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout wrote without execution: %q", stdout.String())
	}
	password := "initial-password-value"
	input := `{"databaseUrl":"postgresql://identity_bootstrap_login@127.0.0.1:1/ops","tenantSlug":"primary","loginName":"root-admin","displayName":"Root Administrator","password":"` + password + `"}`
	privateInput := writePrivateFile(t, "input.json", input, 0o600)
	privatePassword := writePrivateFile(t, "password", password, 0o600)
	stdout.Reset()
	stderr.Reset()
	if err := runBootstrapNativeAdministrator(context.Background(), []string{"--input", privateInput, "--password-file", privatePassword}, &stdout, &stderr); err == nil {
		t.Fatal("unknown input field accepted")
	}
	if strings.Contains(stdout.String()+stderr.String(), password) {
		t.Fatal("rejected command output leaked password")
	}
}
