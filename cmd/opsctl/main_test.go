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

func TestOpenBaoRootAcceptsExtractedOfflineInstallerWithoutGit(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, name := range []string{"go.mod", "bundle/component-catalog.yaml", "deploy/profiles/kubernetes-containerd.yaml"} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("offline installer material"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := platformRepositoryRoot()
	if err != nil || actual != root {
		t.Fatalf("extracted installer rejected: %s %v", actual, err)
	}
}
