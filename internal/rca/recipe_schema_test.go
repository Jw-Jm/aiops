package rca

import (
	"encoding/json"
	"testing"
	"ops-platform/internal/contract"
)

// Every admitted builtin declaration must stay identical to the reviewed
// schema consts, including native post-check-only sets.
func TestBuiltinRecipesMatchSchemaContract(t *testing.T) {
	for _, name := range []string{"dimm-failure", "pvc-csi-failure", "node-failure", "pod-health", "deployment-health", "service-endpoints-health", "pod-image-pull-failure", "service-no-endpoints"} {
		r, err := Builtin(name)
		if err != nil {
			t.Fatalf("builtin %s: %v", name, err)
		}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := contract.Validate("https://ops.local/schemas/recipe-registry/v2", raw); err != nil {
			t.Fatalf("schema %s/%s: %v", name, r.Version, err)
		}
	}
	for _, name := range []string{"node-failure", "pvc-csi-failure"} {
		r, err := BuiltinVersion(name, "v2")
		if err != nil {
			t.Fatalf("builtin %s/v2: %v", name, err)
		}
		raw, _ := json.Marshal(r)
		if err := contract.Validate("https://ops.local/schemas/recipe-registry/v2", raw); err != nil {
			t.Fatalf("schema %s/v2: %v", name, err)
		}
	}
}
