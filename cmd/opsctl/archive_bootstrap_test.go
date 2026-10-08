package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveBootstrapEntryRequiresExplicitInputs(t *testing.T) {
	err := run(context.Background(), []string{"bootstrap", "archive-bucket"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Archive bootstrap requires --profile and --secrets-file") {
		t.Fatalf("formal Archive bootstrap entry missing: %v", err)
	}
}

func TestPrivateOutputsCannotUseGitDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: external-worktree"), 0600); err != nil {
		t.Fatal(err)
	}
	if outsideGitTree(filepath.Join(dir, ".")) {
		t.Fatal("output parent skipped its own Git marker")
	}
}
