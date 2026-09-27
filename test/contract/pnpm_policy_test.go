package contract_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPNPMWorkspaceAllowsLockedEsbuildBuildScript(t *testing.T) {
	path := filepath.Join("..", "..", "web", "pnpm-workspace.yaml")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pnpm workspace policy %s: %v", path, err)
	}
	var policy struct {
		AllowBuilds map[string]bool `yaml:"allowBuilds"`
	}
	if err := yaml.Unmarshal(contents, &policy); err != nil {
		t.Fatalf("parse pnpm workspace policy: %v", err)
	}
	if !policy.AllowBuilds["esbuild"] {
		t.Fatal("pnpm workspace must explicitly allow the locked esbuild build script")
	}
}
