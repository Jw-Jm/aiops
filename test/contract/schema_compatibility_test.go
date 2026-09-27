package contract_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	platformcontract "ops-platform/internal/contract"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestJSONSchemas(t *testing.T) {
	const schemaBase = "https://ops.local/schemas/"
	valid := []struct {
		name string
		id   string
	}{
		{"finding-envelope", schemaBase + "finding-envelope/v1"},
		{"finding-envelope-percent-encoded-id", schemaBase + "finding-envelope/v1"},
		{"incident", schemaBase + "incident/v1"},
		{"evidence", schemaBase + "evidence/v1"},
		{"diagnostic-graph", schemaBase + "diagnostic-graph/v1"},
		{"rca-result", schemaBase + "rca-result/v1"},
		{"invocation-context", schemaBase + "invocation-context/v1"},
		{"action-plan-v2", schemaBase + "action-plan/v2"},
		{"command-execution", schemaBase + "command-execution/v1"},
		{"audit-record", schemaBase + "audit-record/v1"},
		{"finding-event", schemaBase + "finding-event/v1"},
		{"incident-event", schemaBase + "incident-event/v1"},
	}
	for _, test := range valid {
		t.Run("valid/"+test.name, func(t *testing.T) {
			payload := fixture(t, "valid", test.name+".json")
			if err := platformcontract.Validate(test.id, payload); err != nil {
				t.Fatalf("Validate(%q, %s): %v", test.id, test.name, err)
			}
		})
	}

	invalid := []struct {
		name string
		id   string
	}{
		{"missing-schema-version", schemaBase + "finding-envelope/v1"},
		{"unknown-severity", schemaBase + "finding-envelope/v1"},
		{"missing-evidence-data-class", schemaBase + "evidence/v1"},
		{"cross-tenant-id", schemaBase + "finding-envelope/v1"},
		{"invalid-canonical-id", schemaBase + "finding-envelope/v1"},
		{"unknown-root-field", schemaBase + "finding-envelope/v1"},
		{"oversized-text", schemaBase + "incident/v1"},
		{"unbounded-graph-budget", schemaBase + "diagnostic-graph/v1"},
		{"action-plan-v1", schemaBase + "action-plan/v2"},
	}
	for _, test := range invalid {
		t.Run("invalid/"+test.name, func(t *testing.T) {
			payload := fixture(t, "invalid", test.name+".json")
			if err := platformcontract.Validate(test.id, payload); err == nil {
				t.Fatalf("Validate(%q, %s) unexpectedly succeeded", test.id, test.name)
			}
		})
	}
}

func TestSchemaCompatibility(t *testing.T) {
	const schemaID = "https://ops.local/schemas/finding-envelope/v1"
	schemaPath := filepath.Join("..", "..", "api", "schemas", "finding-envelope-v1.schema.json")
	commonPath := filepath.Join("..", "..", "api", "schemas", "common.schema.json")
	baseline, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read baseline schema: %v", err)
	}
	common, err := os.ReadFile(commonPath)
	if err != nil {
		t.Fatalf("read common schema: %v", err)
	}
	payload := fixture(t, "valid", "finding-envelope.json")

	var base map[string]any
	if err := json.Unmarshal(baseline, &base); err != nil {
		t.Fatalf("decode baseline schema: %v", err)
	}
	additive := cloneJSONMap(t, base)
	additive["properties"].(map[string]any)["compatibilityProbe"] = map[string]any{
		"type": "string", "maxLength": float64(32),
	}
	if err := validateWithSchema(schemaID, common, encode(t, additive), payload); err != nil {
		t.Fatalf("adding an optional property rejected the baseline payload: %v", err)
	}

	removed := cloneJSONMap(t, base)
	delete(removed["properties"].(map[string]any), "status")
	if err := validateWithSchema(schemaID, common, encode(t, removed), payload); err == nil {
		t.Fatal("deleting the status property did not reject the baseline payload")
	}
}

func TestMCPToolSchemas(t *testing.T) {
	tools := []string{
		"get_incident_context",
		"get_resource_context",
		"get_resource_health",
		"get_dependencies",
		"get_impact_scope",
		"get_findings",
		"get_ranked_evidence",
		"get_evidence_conflicts",
		"get_recent_changes",
		"get_root_cause_candidates",
		"query_metrics",
		"query_logs",
		"query_deepflow",
		"query_kubernetes",
		"query_kubevirt",
		"query_hardware",
	}
	directory := filepath.Join("..", "..", "api", "mcp", "tools")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read MCP tool schemas: %v", err)
	}
	if len(entries) != len(tools) {
		t.Fatalf("expected %d approved MCP tool schemas, found %d", len(tools), len(entries))
	}

	for _, tool := range tools {
		tool := tool
		t.Run(tool, func(t *testing.T) {
			path := filepath.Join(directory, tool+"-v1.schema.json")
			schemaBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read tool schema: %v", err)
			}
			var schema map[string]any
			if err := json.Unmarshal(schemaBytes, &schema); err != nil {
				t.Fatalf("decode tool schema: %v", err)
			}
			metadata, ok := schema["x-tool-contract"].(map[string]any)
			if !ok {
				t.Fatal("missing x-tool-contract metadata")
			}
			if metadata["name"] != tool {
				t.Fatalf("metadata name = %v, want %q", metadata["name"], tool)
			}
			if metadata["inputSchema"].(map[string]any)["$ref"] != "https://ops.local/schemas/"+tool+"/v1" {
				t.Fatal("inputSchema metadata does not refer to the tool input schema")
			}
			if metadata["outputSchema"].(map[string]any)["$ref"] != "https://ops.local/schemas/"+tool+"/v1#output" {
				t.Fatal("outputSchema metadata does not refer to the tool output schema")
			}
			metadataBytes, err := json.Marshal(metadata)
			if err != nil {
				t.Fatalf("encode tool metadata: %v", err)
			}
			if err := platformcontract.Validate("https://ops.local/schemas/common/v1#/$defs/toolMetadata", metadataBytes); err != nil {
				t.Fatalf("validate tool metadata: %v", err)
			}
			if err := platformcontract.Validate("https://ops.local/schemas/"+tool+"/v1", fixture(t, "valid", "mcp-"+tool+".json")); err != nil {
				t.Fatalf("validate tool input: %v", err)
			}
			if err := platformcontract.Validate("https://ops.local/schemas/"+tool+"/v1#output", fixture(t, "valid", "mcp-output-"+tool+".json")); err != nil {
				t.Fatalf("validate tool output: %v", err)
			}
		})
	}
}

func TestMCPOutputRejectsCrossTenantCanonicalID(t *testing.T) {
	payload := fixture(t, "valid", "mcp-output-get_incident_context.json")
	var output map[string]any
	if err := json.Unmarshal(payload, &output); err != nil {
		t.Fatalf("decode MCP output fixture: %v", err)
	}
	data := output["data"].(map[string]any)
	incident := data["incident"].(map[string]any)
	incident["tenantId"] = "tenant-b"

	if err := platformcontract.Validate("https://ops.local/schemas/get_incident_context/v1#output", encode(t, output)); err == nil {
		t.Fatal("Validate accepted an MCP output whose Canonical ID tenant differs from its incident tenant")
	}
}

func fixture(t *testing.T, group, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "fixtures", "contracts", group, name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return contents
}

func cloneJSONMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	encoded := encode(t, value)
	var cloned map[string]any
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		t.Fatalf("clone JSON value: %v", err)
	}
	return cloned
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode JSON value: %v", err)
	}
	return encoded
}

func validateWithSchema(schemaID string, common, schema, payload []byte) error {
	compiler := jsonschema.NewCompiler()
	var commonDoc, schemaDoc, instance any
	for _, document := range []struct {
		name string
		data []byte
		dest *any
	}{
		{"common schema", common, &commonDoc},
		{"candidate schema", schema, &schemaDoc},
		{"payload", payload, &instance},
	} {
		if err := json.Unmarshal(document.data, document.dest); err != nil {
			return fmt.Errorf("decode %s: %w", document.name, err)
		}
	}
	if err := compiler.AddResource("https://ops.local/schemas/common/v1", commonDoc); err != nil {
		return fmt.Errorf("register common schema: %w", err)
	}
	if err := compiler.AddResource(schemaID, schemaDoc); err != nil {
		return fmt.Errorf("register candidate schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		return fmt.Errorf("compile candidate schema: %w", err)
	}
	return compiled.Validate(instance)
}
