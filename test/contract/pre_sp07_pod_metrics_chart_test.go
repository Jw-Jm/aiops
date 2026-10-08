package contract_test

import (
	"bytes"
	"io"
	"os/exec"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCurrentSP05PodMetricsMinimalReadPermission(t *testing.T) {
	cmd := exec.Command("helm", "template", "metrics-check", "../../deploy/charts/ops-platform", "--namespace", "ops-test", "--set", "sp04.enabled=true", "--set", "sp04.allowedWorkerCIDRs[0]=10.0.0.0/24", "--set", "sp04.archiveBackendLogicalID=archive-test", "--set", "sp04.kubernetesAPI.localCollector=true", "--set", "sp05.enabled=true")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v %s", err, raw)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	found := false
	for {
		var object struct {
			Kind  string `yaml:"kind"`
			Rules []struct {
				Groups    []string `yaml:"apiGroups"`
				Resources []string `yaml:"resources"`
				Verbs     []string `yaml:"verbs"`
			} `yaml:"rules"`
		}
		if err := decoder.Decode(&object); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if object.Kind != "ClusterRole" {
			continue
		}
		for _, rule := range object.Rules {
			if len(rule.Groups) != 1 || rule.Groups[0] != "metrics.k8s.io" {
				continue
			}
			found = true
			if len(rule.Verbs) != 1 || rule.Verbs[0] != "get" || len(rule.Resources) != 2 || rule.Resources[0] != "nodes" || rule.Resources[1] != "pods" {
				t.Fatalf("bounded Node/Pod metric GET missing or expanded: %+v", rule)
			}
		}
	}
	if !found {
		t.Fatal("metrics read rule absent")
	}
}
