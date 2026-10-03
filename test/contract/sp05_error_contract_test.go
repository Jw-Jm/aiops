package contract_test

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"reflect"
	"testing"
)

// The frozen middleware contract is mirrored for offline response validation;
// this proves the mirror cannot silently enlarge or weaken its public enum.
func TestLegacyErrorValidationSchemaMatchesFrozenOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi/platform-v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	shape := schemas["ErrorEnvelope"].(map[string]any)
	shape["properties"].(map[string]any)["requestId"] = schemas["Identifier"]
	normalized, err := json.Marshal(shape)
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if err = json.Unmarshal(normalized, &expected); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../api/schemas/error-envelope-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err = json.Unmarshal(raw, &actual); err != nil {
		t.Fatal(err)
	}
	delete(actual, "$id")
	delete(actual, "$schema")
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("legacy error mirror differs from frozen OpenAPI contract")
	}
}
