package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileResolveRejectsRepositoryTemplate(t *testing.T) {
	templatePath := filepath.Join("..", "..", "deploy", "profiles", "dev-orbstack.yaml")
	if _, err := os.Stat(templatePath); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"profile", "resolve", "-f", templatePath, "-o", filepath.Join(t.TempDir(), "resolved.yaml")}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "PROFILE_INPUT_NOT_DETECTED") {
		t.Fatalf("resolve error = %v, want PROFILE_INPUT_NOT_DETECTED", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("resolve unexpectedly wrote output: %s", stdout.String())
	}
}
