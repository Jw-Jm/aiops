package contract

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectionNonVirtualReplayEntryPoint(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "test/fixtures/upstream-inspection/replay-nonvirtual.py")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("executable nonvirtual replay is missing: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs/poc/inspection-reuse-lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Replay struct {
			Runner  lockedFile `json:"runner"`
			Test    string     `json:"test"`
			Network string     `json:"network"`
			Pull    string     `json:"pull"`
		} `json:"nonVirtualReplay"`
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Replay.Test != "TestInspectionNonVirtualUpstreamReplay" || lock.Replay.Network != "none" || lock.Replay.Pull != "never" || lock.Replay.Runner.Path != "test/fixtures/upstream-inspection/replay-nonvirtual.py" {
		t.Fatal("nonvirtual replay entry point or network constraints are not locked")
	}
	if err := verifyLockedFile(root, lock.Replay.Runner.Path, lock.Replay.Runner.SHA256); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kubevirt-observability-rules", "kubevirt-must-gather", "kubernetes-mcp-kubevirt-toolset"} {
		cmd := exec.CommandContext(t.Context(), "python3", script, "--only", name)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "invalid choice") {
			t.Fatalf("deferred replay %s was not rejected: %v %s", name, err, output)
		}
	}
	cmd := exec.CommandContext(t.Context(), "python3", script, "--only", "coroot-community-check", "--sources", t.TempDir(), "--output", t.TempDir(), "--check-inputs")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "source") {
		t.Fatalf("missing upstream source was not rejected: %v %s", err, output)
	}
}

func TestInspectionNonVirtualUpstreamReplay(t *testing.T) {
	if os.Getenv("OPS_INSPECTION_REPLAY") != "1" {
		t.Skip("set OPS_INSPECTION_REPLAY=1 with prepared source and Keep wheels for actual upstream replay")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	source := os.Getenv("OPS_INSPECTION_SOURCE_DIR")
	wheels := os.Getenv("OPS_INSPECTION_KEEP_WHEELS")
	if source == "" || wheels == "" {
		t.Fatal("OPS_INSPECTION_SOURCE_DIR and OPS_INSPECTION_KEEP_WHEELS are required; no download fallback")
	}
	output := os.Getenv("OPS_INSPECTION_REPLAY_OUTPUT")
	if output == "" {
		output = t.TempDir()
	}
	cmd := exec.CommandContext(t.Context(), "python3", filepath.Join(root, "test/fixtures/upstream-inspection/replay-nonvirtual.py"), "--sources", source, "--keep-wheels", wheels, "--output", output)
	cmd.Dir = root
	result, err := cmd.CombinedOutput()
	t.Logf("command=%s %q\n%s", cmd.Path, cmd.Args[1:], result)
	if err != nil {
		t.Fatalf("nonvirtual upstream replay: %v", err)
	}
}
