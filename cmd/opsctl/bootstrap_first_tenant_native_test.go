package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func validNativeFirstTenant() nativeFirstTenantInput {
	return nativeFirstTenantInput{
		DatabaseURL:      "postgresql://identity_bootstrap_login@127.0.0.1:15432/ops",
		TenantSlug:       "primary",
		TenantDisplay:    "Primary Tenant",
		AdminLoginName:   "root-admin",
		AdminDisplayName: "Root Administrator",
	}
}

func TestNativeFirstTenantInputRequiresCompleteExplicitFields(t *testing.T) {
	if err := validNativeFirstTenant().validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*nativeFirstTenantInput)
		want   string
	}{
		{"database", func(i *nativeFirstTenantInput) { i.DatabaseURL = "https://127.0.0.1/ops" }, "databaseUrl"},
		{"slug", func(i *nativeFirstTenantInput) { i.TenantSlug = " " }, "tenantSlug"},
		{"tenant display", func(i *nativeFirstTenantInput) { i.TenantDisplay = "" }, "tenantDisplayName"},
		{"login", func(i *nativeFirstTenantInput) { i.AdminLoginName = "Root Admin" }, "adminLoginName"},
		{"display", func(i *nativeFirstTenantInput) { i.AdminDisplayName = "" }, "adminDisplayName"},
	}
	for _, test := range cases {
		input := validNativeFirstTenant()
		test.mutate(&input)
		err := input.validate()
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s: error = %v", test.name, err)
		}
	}
}

func TestNativeFirstTenantBootstrapRequiresPrivateFiles(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runBootstrapFirstTenantNative(context.Background(), nil, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--input") {
		t.Fatalf("missing flags error = %v", err)
	}
	public := writePrivateFile(t, "input.json", `{"databaseUrl":"postgresql://u@127.0.0.1/ops","tenantSlug":"primary","tenantDisplayName":"Primary","adminLoginName":"root-admin","adminDisplayName":"Root"}`, 0o644)
	password := writePrivateFile(t, "password", "initial-password-value", 0o600)
	err = runBootstrapFirstTenantNative(context.Background(), []string{"--input", public, "--password-file", password}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("public input error = %v", err)
	}
	private := writePrivateFile(t, "input.json", `{"databaseUrl":"postgresql://u@127.0.0.1:1/ops","tenantSlug":"primary","tenantDisplayName":"Primary","adminLoginName":"root-admin","adminDisplayName":"Root","unknownField":"x"}`, 0o600)
	err = runBootstrapFirstTenantNative(context.Background(), []string{"--input", private, "--password-file", password}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("unknown field error = %v", err)
	}
}
