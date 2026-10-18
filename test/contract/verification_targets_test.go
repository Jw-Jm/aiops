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

// Run fake tools only in an isolated copy so entry-point tests cannot overwrite
// a concurrent real acceptance run's evidence.
func verificationTargetFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	for _, name := range []string{"Makefile", "scripts/check-test-report.py"} {
		data, err := os.ReadFile(filepath.Join("../..", name))
		if err != nil {
			t.Fatal(err)
		}
		targetPath := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(targetPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func TestAcceptanceTargetsRejectSkippedEmptyAndFailedSuites(t *testing.T) {
	for _, target := range []string{"test-replay", "test-e2e", "test-integration", "test-security", "test-live-sources", "test-native", "test-capacity", "test-capacity-live", "test-browser-runtime"} {
		for _, result := range []string{"skip", "empty", "failure", "pass"} {
			t.Run(target+"/"+result, func(t *testing.T) {
				directory := verificationTargetFixture(t)
				fakeGo := filepath.Join(directory, "go")
				script := "#!/bin/sh\nprintf '%s\\n' '{\"Action\":\"pass\",\"Test\":\"TestFixture\"}'\n"
				switch result {
				case "skip":
					script = "#!/bin/sh\nprintf '%s\\n' '{\"Action\":\"skip\",\"Test\":\"TestMissingEnvironment\"}'\n"
				case "empty":
					script = "#!/bin/sh\nexit 0\n"
				case "failure":
					script += "exit 23\n"
				}
				if err := os.WriteFile(fakeGo, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				command := exec.CommandContext(t.Context(), "make", "-C", directory, target, "GO="+fakeGo)
				command.Env = append(os.Environ(), "OPS_PERFORMANCE_EXEMPTION=")
				output, err := command.CombinedOutput()
				if (err == nil) != (result == "pass") {
					t.Fatalf("%s accepted %s or rejected complete suite: %v\n%s", target, result, err, output)
				}
				if (result == "skip" || result == "empty") && !strings.Contains(string(output), "Acceptance suite did not fully execute") {
					t.Fatalf("missing explicit acceptance failure: %s", output)
				}
			})
		}
	}
}

func TestLocalTargetsSelectOnlyIndependentSuites(t *testing.T) {
	directory := verificationTargetFixture(t)
	for _, target := range []string{"test-unit", "test-contract", "test-local", "check-local"} {
		command := exec.CommandContext(t.Context(), "make", "-n", "-C", directory, target)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(output))
		}
		text := string(output)
		for _, forbidden := range []string{"./test/integration", "./test/e2e", "test:e2e", "go test -timeout=30m ./...", "OPS_GRAPH_REPLAY=1"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s runs external suite %s: %s", target, forbidden, text)
			}
		}
		if target == "test-unit" && (!strings.Contains(text, "./cmd/... ./internal/... ./gen/...") || !strings.Contains(text, "-skip '^(TestUpstreamConformance") || strings.Contains(text, "pnpm")) {
			t.Fatalf("unit target must select Go packages and exclude load/native probes: %s", text)
		}
		if target == "test-local" {
			for _, required := range []string{"./test/contract", "pnpm --dir web test", "uv run --offline --locked --group dev python -m pytest", "unittest discover -s scripts/assistant-runtime", "./test/security"} {
				if !strings.Contains(text, required) {
					t.Fatalf("local aggregate missing %s: %s", required, text)
				}
			}
		}
	}
}

func TestCapacityTargetsRejectPerformanceWaivers(t *testing.T) {
	for _, target := range []string{"test-capacity", "test-capacity-live"} {
		command := exec.CommandContext(t.Context(), "make", "-C", verificationTargetFixture(t), target, "GO=/must-not-run")
		command.Env = append(os.Environ(), "OPS_PERFORMANCE_EXEMPTION=pre-sp07-user-20261004")
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "capacity acceptance cannot use OPS_PERFORMANCE_EXEMPTION") {
			t.Fatalf("%s accepted waived capacity or attempted to run: %v: %s", target, err, output)
		}
	}
}

func TestBrowserEntryPointRejectsSkippedEmptyFailedAndStaleReports(t *testing.T) {
	for _, result := range []string{"skip", "empty", "failure", "unexpected", "flaky", "errors", "stale", "pass"} {
		t.Run(result, func(t *testing.T) {
			directory := verificationTargetFixture(t)
			report := filepath.Join(directory, "browser.json")
			if err := os.WriteFile(report, []byte(`{"stats":{"expected":1}}`), 0600); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nprintf '%s' \"$OPS_FAKE_BROWSER_REPORT\" > \"$OPS_WEB_QA_REPORT\"\n"
			stats := `{"stats":{"expected":1}}`
			switch result {
			case "skip":
				stats = `{"stats":{"expected":1,"skipped":1}}`
			case "empty":
				stats = `{"stats":{"expected":0}}`
			case "failure":
				script += "exit 23\n"
			case "unexpected":
				stats = `{"stats":{"expected":1,"unexpected":1}}`
			case "flaky":
				stats = `{"stats":{"expected":1,"flaky":1}}`
			case "errors":
				stats = `{"stats":{"expected":1},"errors":[{"message":"worker failed"}]}`
			case "stale":
				script = "#!/bin/sh\nexit 0\n"
			}
			if err := os.WriteFile(filepath.Join(directory, "pnpm"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), "make", "-C", directory, "test-web-e2e")
			command.Env = append(os.Environ(), "PATH="+directory+":"+os.Getenv("PATH"), "OPS_WEB_QA_INPUT=owned-fixture", "OPS_WEB_QA_REPORT="+report, "OPS_FAKE_BROWSER_REPORT="+stats)
			output, err := command.CombinedOutput()
			if (err == nil) != (result == "pass") {
				t.Fatalf("browser accepted %s or rejected complete run: %v\n%s", result, err, output)
			}
		})
	}
}

func TestFormattingDoesNotModifyLockedThirdPartyMaterialSources(t *testing.T) {
	directory := t.TempDir()
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "Makefile"), makefile, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cmd", "internal", "gen", "test", "artifacts/locked-upstream"} {
		if err := os.MkdirAll(filepath.Join(directory, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"cmd/firstparty.go", "artifacts/locked-upstream/upstream.go"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("package fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report := filepath.Join(directory, "format-arguments.txt")
	if err := os.WriteFile(filepath.Join(directory, "gofmt"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OPS_FORMAT_ARGUMENTS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "make", "-C", directory, "fmt")
	command.Env = append(os.Environ(), "PATH="+directory+":"+os.Getenv("PATH"), "OPS_FORMAT_ARGUMENTS="+report)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("make fmt: %v: %s", err, output)
	}
	arguments, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(arguments), "cmd/firstparty.go") || strings.Contains(string(arguments), "locked-upstream") {
		t.Fatalf("formatter must cover first-party code and preserve locked source materials: %s", arguments)
	}
}
