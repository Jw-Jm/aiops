package openbao

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectedTokenAcceptsKubernetesAtomicWriterLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "..generation"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "..generation", "token"), []byte("synthetic-test-jwt"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..generation", filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..data/token", filepath.Join(root, "token")); err != nil {
		t.Fatal(err)
	}
	if token, err := readProjectedServiceAccountToken(filepath.Join(root, "token")); err != nil || token != "synthetic-test-jwt" {
		t.Fatal("valid Kubernetes projected token could not be read")
	}
}
