package tools

import (
	"encoding/json"
	"ops-platform/internal/contract"
	"strings"
	"testing"
)

func TestAssistantResponseDoesNotChangeLegacyToolContract(t *testing.T) {
	out := Result{SchemaVersion: "tool-response/v3", Tool: "get_assistant_context", State: "succeeded", Data: map[string]any{"message": "已脱敏"}, EvidenceRefs: []string{}, DegradedSources: []string{}}
	clean, err := Sanitize(out, 64<<10)
	if err != nil || clean.SchemaVersion != "tool-response/v3" {
		t.Fatal("assistant response version lost")
	}
	raw, _ := json.Marshal(clean)
	if err = ValidateOutput(raw); err != nil {
		t.Fatal(err)
	}
	if contract.Validate("https://ops.local/schemas/tool-response/v2", raw) == nil {
		t.Fatal("legacy contract accepts a v3 response")
	}
	clean.SchemaVersion = "tool-response/v2"
	raw, _ = json.Marshal(clean)
	if ValidateOutput(raw) == nil {
		t.Fatal("legacy tool catalog broadened")
	}
	clean.Tool = "get_incident_context"
	raw, _ = json.Marshal(clean)
	if err = ValidateOutput(raw); err != nil {
		t.Fatal("legacy response invalid", err)
	}
	clean.SchemaVersion = "tool-response/v3"
	clean.Tool = "query_kubevirt"
	raw, _ = json.Marshal(clean)
	if ValidateOutput(raw) == nil {
		t.Fatal("deferred virtualization entered v3 output catalog")
	}
}

func TestAssistantMCPInputsCarryClosedEmbeddedIntent(t *testing.T) {
	defs, err := CatalogFor("investigation-job/v3", "data_query", "audit", "assistant-tools/v1")
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range defs {
		raw, _ := json.Marshal(def.Schema)
		if strings.Contains(string(raw), `"$ref"`) {
			t.Fatal("provider received an unresolved reference")
		}
		if def.Name == "execute_analysis_query" {
			intent := def.Schema["properties"].(map[string]any)["intent"].(map[string]any)
			if intent["additionalProperties"] != false || intent["properties"].(map[string]any)["datasetId"] == nil {
				t.Fatal("intent was not expanded into the tool contract")
			}
		}
	}
	if _, err = inlineAssistantSchema(map[string]any{"$ref": "https://example.invalid/schema"}); err == nil {
		t.Fatal("external schema reference accepted")
	}
}

func TestOperationalCatalogKeepsV1AndPublishesV2OnlyTools(t *testing.T) {
	for _, version := range []string{"assistant-tools/v1", "assistant-tools/v2"} {
		defs, err := CatalogFor("investigation-job/v3", "resource_troubleshooting", "operational", version)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range defs {
			if d.Name == "resource_history" {
				found = true
			}
			if d.Name == "resource_findings" {
				_, window := d.Schema["properties"].(map[string]any)["windowRole"]
				if window != (version == "assistant-tools/v2") {
					t.Fatal("v1 was broadened")
				}
			}
		}
		if found != (version == "assistant-tools/v2") {
			t.Fatal(version)
		}
	}
}
