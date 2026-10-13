package contract_test

import (
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"io"
	"ops-platform/internal/modelprofile"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAssistantChartFreezesAllRuntimeProfiles(t *testing.T) {
	b, err := os.ReadFile("../fixtures/assistant/model-profile.fixture.json")
	var material modelprofile.Profile
	if err != nil || json.Unmarshal(b, &material) != nil {
		t.Fatal("profile fixture")
	}
	material.AdmissionState = "admitted" // Synthetic contract material, never an actual admission receipt.
	material.BaseURL = "http://ops-model.ops-system.svc.cluster.local:11434/v1"
	reference := material.Reference()
	file := t.TempDir() + "/assistant-values.json"
	values, _ := json.Marshal(map[string]any{"sp06": map[string]any{"model": map[string]any{"timeout": material.TimeoutSeconds}, "assistant": map[string]any{"profile": material}}})
	if os.WriteFile(file, values, 0600) != nil {
		t.Fatal("values fixture")
	}
	args := []string{"-f", file, "template", "assistant", "../../deploy/charts/ops-platform", "--namespace", "ops-system", "--show-only", "templates/sp06.yaml"}
	for _, value := range []string{"sp04.enabled=true", "sp04.allowedWorkerCIDRs[0]=127.0.0.1/32", "sp04.archiveBackendLogicalID=assistant-fixture", "sp04.kubernetesAPI.localCollector=true", "sp05.enabled=true", "sp06.enabled=true", "sp06.tenants[0]=11111111-1111-4111-8111-111111111111", "sp06.modelCIDRs[0]=127.0.0.1/32", "sp06.model.base_url=" + material.BaseURL, "bootstrapAPIOnly=true", "sp06.assistant.enabled=true", "sp06.assistant.modelProfileVersion=" + reference} {
		args = append(args, "--set", value)
	}
	output, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %s", output)
	}
	dec := yaml.NewDecoder(bytes.NewReader(output))
	found := false
	for {
		var resource map[string]any
		err = dec.Decode(&resource)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if resource["kind"] != "ConfigMap" {
			continue
		}
		data := resource["data"].(map[string]any)
		for _, name := range []string{"api", "worker", "investigator"} {
			var config map[string]any
			if json.Unmarshal([]byte(data[name+".json"].(string)), &config) != nil {
				t.Fatal("invalid runtime JSON")
			}
			if name == "investigator" {
				if config["assistantModelProfileVersion"] != reference {
					t.Fatal("investigator profile mismatch")
				}
			} else {
				binding := config["assistant"].(map[string]any)
				if binding["enabled"] != true || binding["modelProfileVersion"] != reference {
					t.Fatal("API/worker profile mismatch")
				}
			}
		}
		found = true
	}
	if !found {
		t.Fatal("assistant runtime missing")
	}
	for _, rejected := range []struct{ value, message string }{
		{"sp06.assistant.profile.admissionState=fixture_only", "fixtures cannot be deployed"},
		{"sp06.assistant.profile.admissionState=pending", "admitted profile"},
		{"sp06.model.timeout=60", "timeout must match"},
		{"sp06.model.base_url=http://other.ops-system.svc.cluster.local:11434/v1", "approved model egress"},
	} {
		bad := append(append([]string(nil), args...), "--set", rejected.value)
		output, err = exec.Command("helm", bad...).CombinedOutput()
		if err == nil || !strings.Contains(string(output), rejected.message) {
			t.Fatalf("unsafe assistant deployment accepted: %s: %s", rejected.value, output)
		}
	}
	args = append(args, "--set", "sp06.assistant.modelProfileVersion=")
	output, err = exec.Command("helm", args...).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "frozen model profile") {
		t.Fatal("assistant enabled without a profile")
	}
}
