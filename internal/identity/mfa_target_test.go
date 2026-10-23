package identity

import (
	"strings"
	"testing"
)

func TestAdministrativeTargetBindsProposedRequestWithoutInventedRevision(t *testing.T) {
	target := map[string]any{"kind": "administrative_write", "write": map[string]any{"method": "POST", "path": "/api/v1/admin/registry-drafts", "bodyDigest": "sha256:" + strings.Repeat("a", 64)}}
	for _, operation := range []string{"user_administration", "source_administration", "cluster_administration", "registry_administration", "execution_profile_administration"} {
		if err := validateTarget(operation, target); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
	}
	for _, operation := range []string{"command_execution", "mfa_enrollment", "mfa_reset", "recovery_code_regenerate"} {
		if err := validateTarget(operation, target); err == nil {
			t.Fatalf("%s borrowed administrative target", operation)
		}
	}
	for _, field := range []string{"method", "path", "bodyDigest"} {
		saved := target["write"].(map[string]any)[field]
		target["write"].(map[string]any)[field] = "invalid"
		if err := validateTarget("registry_administration", target); err == nil {
			t.Fatalf("accepted malformed %s", field)
		}
		target["write"].(map[string]any)[field] = saved
	}
	target["revision"] = 1
	if err := validateTarget("registry_administration", target); err == nil {
		t.Fatal("accepted invented revision")
	}
}
