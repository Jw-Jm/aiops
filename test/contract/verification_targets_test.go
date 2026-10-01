package contract

import (
	"os"
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
