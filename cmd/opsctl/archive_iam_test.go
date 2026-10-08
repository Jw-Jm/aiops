package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/bootstrap"
	"ops-platform/internal/integrations/s3"
)

func TestArchiveIAMPrivateOutputAndExplicitDutyBoundary(t *testing.T) {
	role := func(name string) s3.RoleCredential {
		return s3.RoleCredential{AccessKey: "explicit-" + name, SecretKey: "private-" + name + "-credential"}
	}
	input := bootstrap.ArchiveIAMInput{SchemaVersion: 1, Bucket: "explicit-archive", BootstrapAdmin: role("admin"), Credentials: s3.TenantCredentials{SchemaVersion: "ops-archive-credentials/v2", Tenants: []s3.TenantCredential{{TenantID: uuid.New(), Roles: &s3.TenantRoles{Write: role("write"), Read: role("read"), Protect: role("protect"), Cleanup: role("cleanup")}}}}}
	dir := t.TempDir()
	config := filepath.Join(dir, "private.json")
	output := filepath.Join(dir, "new-iam.json")
	raw, _ := json.Marshal(input)
	if err := os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"bootstrap", "archive-iam", "--secrets-file", config, "--output", output}
	if err := run(context.Background(), args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{input.BootstrapAdmin.SecretKey, input.Credentials.Tenants[0].Roles.Write.SecretKey} {
		if bytes.Contains(stdout.Bytes(), []byte(secret)) || bytes.Contains(stderr.Bytes(), []byte(secret)) {
			t.Fatal("public preparation receipt leaked credential")
		}
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(output)
	if info.Mode().Perm() != 0600 {
		t.Fatal("private output permissions widened")
	}
	if err := run(context.Background(), args, &stdout, &stderr); err == nil {
		t.Fatal("existing private IAM output overwritten")
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(before, after) {
		t.Fatal("existing recovery material changed")
	}
	input.Credentials.Tenants[0].Roles.Cleanup = input.Credentials.Tenants[0].Roles.Write
	if _, err := bootstrap.BuildArchiveIAM(input); err == nil {
		t.Fatal("write/cleanup credentials reused")
	}
	input.Credentials.SchemaVersion = "ops-archive-credentials/v1"
	if _, err := bootstrap.BuildArchiveIAM(input); err == nil {
		t.Fatal("legacy combined runtime duty prepared for current install")
	}
}
