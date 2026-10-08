package bundle

import (
	"bytes"
	"strings"
	"testing"

	artifacts "ops-platform/bundle"
	"ops-platform/internal/supplychain"

	"gopkg.in/yaml.v3"
)

func TestMetricsAdmissionBindsMeasuredImageAndNativeSourceScope(t *testing.T) {
	evidence, err := artifacts.ComponentEvidence()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := supplychain.LoadCatalogWithEvidence(bytes.NewReader(artifacts.ComponentCatalog()), evidence)
	if err != nil {
		t.Fatal(err)
	}
	component, ok := catalog.Component("metrics-server")
	if !ok || component.State != "qualified" || !component.DependencyClosureVerified {
		t.Fatal("exact Metrics image/source/license closure is not admitted")
	}
	if component.Version != "v0.8.0" || component.Commit != "d66279c6426c6581d9656fe3d42bc52db7c29597" || component.Digest != "sha256:8f49cf1b0688bb0eae18437882dbf6de2c7a2baac71b1492bc4eca25439a1bf2" || component.SourceArchiveSHA256 != "sha256:4dc2060e33613c35ae0f7c48aba1b813d986d82049f641966fd9eb5eefc49864" || len(component.DependencyClosure) != 100 {
		t.Fatal("Metrics admission differs from measured main source, 95 Go modules, compiler and four native source identities")
	}
	var native int
	for _, dependency := range component.DependencyClosure {
		if strings.HasPrefix(dependency.Name, "native:") {
			native++
			if !strings.HasPrefix(dependency.License, "LicenseRef-PreSP07-Metrics-") {
				t.Fatal("native publisher terms lack their finite reviewed scope")
			}
		}
	}
	if native != 4 || len(component.FileLicenses) == 0 || component.CorrespondingSourceBundleSHA256 == "" {
		t.Fatal("native notices or complete corresponding source missing")
	}
	for _, field := range []string{"digest", "correspondingSourceBundleSHA256"} {
		t.Run("different_"+field, func(t *testing.T) {
			var document map[string]any
			if err := yaml.Unmarshal(artifacts.ComponentCatalog(), &document); err != nil {
				t.Fatal(err)
			}
			for _, value := range document["components"].([]any) {
				entry := value.(map[string]any)
				if entry["name"] == "metrics-server" {
					entry[field] = "sha256:" + strings.Repeat("0", 64)
				}
			}
			raw, err := yaml.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := supplychain.LoadCatalogWithEvidence(bytes.NewReader(raw), evidence); err == nil {
				t.Fatal("native finite permission was reused for a different image or source closure")
			}
		})
	}
}
