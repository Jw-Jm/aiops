package integration

import (
	"os"
	"strings"
	"testing"
)

// graph.Scope serializes its cluster identity as "clusterUid". A projection that
// reads "cluster" silently passes an empty cluster to platform.sp06_lock_policy,
// which then re-evaluates the wrong activation (or a tenant one). Static guard:
// the persisted-task projections must use the real wire key.
func TestNativeTaskProjectionsUseScopeWireKeys(t *testing.T) {
	for _, name := range []string{
		"00067_native_internal_task_authority.sql",
		"00068_native_incident_job_authority.sql",
	} {
		raw, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(raw)
		if strings.Contains(text, "effective_scope->>'cluster'") {
			t.Fatalf("%s reads the wrong scope wire key", name)
		}
		if !strings.Contains(text, "effective_scope->>'clusterUid'") {
			t.Fatalf("%s does not read the frozen cluster identity", name)
		}
		if !strings.Contains(text, "platform.sp06_lock_policy(") {
			t.Fatalf("%s does not re-check the effective policy", name)
		}
	}
	// The v2 projection is the only one that may read the provenance columns, and
	// it must require a native source explicitly.
	raw, err := os.ReadFile("../../migrations/00068_native_incident_job_authority.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, fragment := range []string{
		"CREATE FUNCTION platform.native_incident_task_shared(",
		"CREATE FUNCTION platform.native_incident_job_subject_shared(",
		"authorization_source = 'native'",
		"job.requesting_subject = account.subject",
		"job.authorization_version = account.authorization_version",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("migration 68 is missing %q", fragment)
		}
	}
}
