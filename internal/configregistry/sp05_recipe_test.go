package configregistry

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSP05RegistryRejectsSchemaValidButUnexecutableRecipe(t *testing.T) {
	raw, err := os.ReadFile("../../recipes/node-failure/v1/recipe.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateContent(KindRecipe, raw); err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if json.Unmarshal(raw, &content) != nil {
		t.Fatal("frozen declaration")
	}
	content["requiredEvidence"].([]any)[0].(map[string]any)["predicate"] = "ranking-is-causation/v1"
	changed, _ := json.Marshal(content)
	if ValidateContent(KindRecipe, changed) == nil {
		t.Fatal("registry accepted a predicate no consumer can execute")
	}
}

func TestSP05RegistryAcceptsExplicitHistoricalAndNodeV2Predicates(t *testing.T) {
	for _, path := range []string{"dimm-failure/v1", "pvc-csi-failure/v1", "node-failure/v1", "node-failure/v2"} {
		raw, err := os.ReadFile("../../recipes/" + path + "/recipe.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateContent(KindRecipe, raw); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}
