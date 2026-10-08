package contract_test

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOperatorRoleStatusContractPreservesScopeAndRequiresRevision(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi/platform-v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	path, ok := document["paths"].(map[string]any)["/api/v1/admin/role-bindings/{bindingId}/status"].(map[string]any)
	if !ok {
		t.Fatal("formal operator revocation/restoration endpoint is absent")
	}
	operation := path["patch"].(map[string]any)
	if operation["operationId"] != "setOperatorRoleBindingStatus" || operation["x-idempotency"] != "required" || operation["x-step-up"] != "required" {
		t.Fatal("role status mutation is missing its explicit administrative guards")
	}
	schema := document["components"].(map[string]any)["schemas"].(map[string]any)["OperatorRoleBindingStatusUpdateRequest"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	if schema["additionalProperties"] != false || len(properties) != 2 || properties["expectedRevision"] == nil || properties["status"] == nil {
		t.Fatal("status-only operation can alter identity, role or scope")
	}
	revision := properties["expectedRevision"].(map[string]any)
	if revision["type"] != "integer" || revision["minimum"] != 1 {
		t.Fatal("unfenced role status update")
	}
	statuses := properties["status"].(map[string]any)["enum"].([]any)
	if len(statuses) != 2 || statuses[0] != "active" || statuses[1] != "disabled" {
		t.Fatal("role status mutation introduces an execution state")
	}
	if len(schema["required"].([]any)) != 2 || document["info"].(map[string]any)["version"] != "1.4.0" {
		t.Fatal("revision/status requirements or additive Contract version missing")
	}
}
