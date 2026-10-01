package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerificationTargetsRunRealSuitesAndIncludeSQLCArtifacts(t *testing.T) {
	encoded, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, entry := range []string{"internal/persistence/dbgen", "test-integration:", "test-security:", "./test/integration", "./test/security", "check-test-report.py"} {
		if !strings.Contains(text, entry) {
			t.Errorf("verification entry point missing %s", entry)
		}
	}
}

func TestReplayAndE2EAcceptanceRejectSkippedSuites(t *testing.T) {
	for _, target := range []string{"test-replay", "test-e2e"} {
		t.Run(target, func(t *testing.T) {
			directory := t.TempDir()
			fakeGo := filepath.Join(directory, "go")
			// A successful go test exit is insufficient when acceptance did not run.
			if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf '%s\\n' '{\"Action\":\"skip\",\"Package\":\"acceptance\",\"Test\":\"TestMissingEnvironment\"}'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), "make", "-C", "../..", target, "GO="+fakeGo)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "Acceptance suite did not fully execute") {
				t.Fatalf("%s accepted skipped validation: %v\n%s", target, err, output)
			}
		})
	}
}
