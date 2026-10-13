package contract_test

import (
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestAssistantChartFreezesAllRuntimeProfiles(t *testing.T) {
	args := []string{"template", "assistant", "../../deploy/charts/ops-platform", "--namespace", "ops-system", "--show-only", "templates/sp06.yaml"}
	for _, value := range []string{"sp04.enabled=true", "sp04.allowedWorkerCIDRs[0]=127.0.0.1/32", "sp04.archiveBackendLogicalID=assistant-fixture", "sp04.kubernetesAPI.localCollector=true", "sp05.enabled=true", "sp06.enabled=true", "sp06.tenants[0]=11111111-1111-4111-8111-111111111111", "sp06.modelCIDRs[0]=127.0.0.1/32", "bootstrapAPIOnly=true", "sp06.assistant.enabled=true", "sp06.assistant.modelProfileVersion=assistant-fixture/v1"} {
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
				if config["assistantModelProfileVersion"] != "assistant-fixture/v1" {
					t.Fatal("investigator profile mismatch")
				}
			} else {
				binding := config["assistant"].(map[string]any)
				if binding["enabled"] != true || binding["modelProfileVersion"] != "assistant-fixture/v1" {
					t.Fatal("API/worker profile mismatch")
				}
			}
		}
		found = true
	}
	if !found {
		t.Fatal("assistant runtime missing")
	}
	args = append(args, "--set", "sp06.assistant.modelProfileVersion=")
	output, err = exec.Command("helm", args...).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "frozen model profile") {
		t.Fatal("assistant enabled without a profile")
	}
}
