package contract

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"testing"
)

func TestSP06ReuseLockBindsAdmittedRuntime(t *testing.T) {
	raw, err := os.ReadFile("../../docs/poc/holmes-investigator-reuse-lock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Image  string `yaml:"imageDigest"`
		Source string `yaml:"correspondingSourceBundleSHA256"`
	}
	if yaml.Unmarshal(raw, &lock) != nil {
		t.Fatal("reuse lock")
	}
	raw, err = os.ReadFile("../../third_party/admission/sp06-holmesgpt-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var admission struct {
		Image  string `json:"imageDigest"`
		Source string `json:"correspondingSourceBundleSHA256"`
	}
	if json.Unmarshal(raw, &admission) != nil {
		t.Fatal("admission")
	}
	raw, err = os.ReadFile("../../bundle/component-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Components []struct {
			Name   string `yaml:"name"`
			Digest string `yaml:"digest"`
			Source string `yaml:"correspondingSourceBundleSHA256"`
		} `yaml:"components"`
	}
	if yaml.Unmarshal(raw, &catalog) != nil {
		t.Fatal("Catalog")
	}
	found := false
	for _, c := range catalog.Components {
		if c.Name == "holmesgpt" {
			found = true
			if lock.Image != c.Digest || lock.Source != c.Source {
				t.Fatal("reuse lock differs from Catalog")
			}
		}
	}
	if !found || lock.Image == "" || lock.Source == "" || lock.Image != admission.Image || lock.Source != admission.Source {
		t.Fatal("reuse lock differs from exact admitted material")
	}
}
