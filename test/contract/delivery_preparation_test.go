package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Preparation checks bind the declared source baseline; they never qualify a
// Bundle, an image, a running version combination or a deployment.
func TestDeliveryPreparationSourceBaseline(t *testing.T) {
	read := func(path string) map[string]any {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("../..", path))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	raw, err := os.ReadFile("../../docs/release/delivery-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var baseline struct {
		SchemaVersion, Status, Scope string
		VersionBasis                 map[string]any
		SourceBinding                map[string]any
		Components                   []struct{ Name, Source, Material, BuildRecipe string }
		Contracts                    []struct{ Path, ID string }
		Migrations                   struct {
			First, Latest, Count int
			Direction, Rollback  string
		}
		FeatureDefaults     map[string]bool
		AssistantDefault    bool
		ConfigurationLimits map[string]any
		Qualification       map[string]bool
		Runbooks            []string
	}
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	if baseline.SchemaVersion != "delivery-preparation/v1" || baseline.Status != "preparation_only" || baseline.Scope != "nonvirtual-core" {
		t.Fatal("preparation must not claim release qualification")
	}
	for _, key := range []string{"deploymentPerformed", "upgradePerformed", "rollbackPerformed", "releaseAdmitted"} {
		if value, ok := baseline.Qualification[key]; !ok || value {
			t.Fatalf("preparation claims %s", key)
		}
	}
	if baseline.Qualification["newBundleBuiltOrSigned"] {
		candidate := baseline.SourceBinding["signedCandidate"].(map[string]any)
		record := read(candidate["evidence"].(string))
		if record["runtimeBundle"].(map[string]any)["builtAndSigned"] != true || record["toolsBundle"].(map[string]any)["previousTrustedInstallerVerification"] != "PASS" || record["releaseAdmitted"] != false {
			t.Fatal("signed candidate must have bounded preparation evidence without claiming release admission")
		}
	}
	for _, key := range []string{"finalCommit", "bundleId", "imageDigests"} {
		if value, ok := baseline.SourceBinding[key]; !ok || value != nil {
			t.Fatalf("unfrozen baseline invents %s", key)
		}
	}
	chart := read("deploy/charts/ops-platform/Chart.yaml")
	public := read("api/openapi/platform-v1.yaml")
	internal := read("api/internal/graph-v1.yaml")
	web := read("web/package.json")
	versions := map[string]any{"platformApp": chart["appVersion"], "chart": chart["version"], "publicAPI": public["info"].(map[string]any)["version"], "internalGraphAPI": internal["info"].(map[string]any)["version"], "webPackage": web["version"]}
	python, err := os.ReadFile("../../services/investigator/pyproject.toml")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^version = "([^"]+)"$`).FindSubmatch(python)
	if len(match) != 2 {
		t.Fatal("investigator package version unavailable")
	}
	versions["investigatorPackage"] = string(match[1])
	catalog := read("bundle/component-catalog.yaml")
	for _, raw := range catalog["components"].([]any) {
		component := raw.(map[string]any)
		if component["name"] == "holmesgpt" {
			versions["holmesgpt"] = component["version"]
		}
	}
	if !reflect.DeepEqual(versions, baseline.VersionBasis) {
		t.Fatalf("delivery version baseline drift: actual=%v declared=%v", versions, baseline.VersionBasis)
	}
	if len(baseline.Components) != 9 || len(baseline.Contracts) != 16 {
		t.Fatal("delivery component/contract inventory incomplete")
	}
	seen := map[string]bool{}
	for _, component := range baseline.Components {
		if seen[component.Name] || component.Material == "" {
			t.Fatal("duplicate component or missing existing Bundle material")
		}
		seen[component.Name] = true
		for _, path := range []string{component.Source, component.BuildRecipe} {
			if path != "" {
				if _, err := os.Stat(filepath.Join("../..", path)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, contract := range baseline.Contracts {
		if read(contract.Path)["$id"] != contract.ID {
			t.Fatalf("contract version drift: %s", contract.Path)
		}
	}
	for _, pair := range []struct{ file, version string }{{"output.schema.json", "investigation-result/v1"}, {"assistant-output.schema.json", "assistant-answer/v1"}} {
		if read("services/investigator/src/investigator/" + pair.file)["properties"].(map[string]any)["schemaVersion"].(map[string]any)["const"] != pair.version {
			t.Fatalf("Python output version drift: %s", pair.file)
		}
	}
	files, err := filepath.Glob("../../migrations/[0-9]*_*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != baseline.Migrations.Count || baseline.Migrations.First != 1 || baseline.Migrations.Latest != len(files) || baseline.Migrations.Direction != "forward_only" || baseline.Migrations.Rollback != "no_down_no_automatic_binary_downgrade" {
		t.Fatal("migration range/policy drift")
	}
	for i, path := range files {
		version, err := strconv.Atoi(strings.SplitN(filepath.Base(path), "_", 2)[0])
		if err != nil || version != i+1 {
			t.Fatalf("non-contiguous migration sequence: %s", path)
		}
	}
	values := read("deploy/charts/ops-platform/values.yaml")
	if values["workloadsEnabled"] != false || baseline.AssistantDefault || len(baseline.FeatureDefaults) != 5 {
		t.Fatal("capability default drift")
	}
	for feature, enabled := range baseline.FeatureDefaults {
		if enabled || values[feature].(map[string]any)["enabled"] != enabled {
			t.Fatalf("capability default drift: %s", feature)
		}
	}
	if values["sp06"].(map[string]any)["assistant"].(map[string]any)["enabled"] != baseline.AssistantDefault {
		t.Fatal("assistant default drift")
	}
	profile := read("deploy/profiles/kubernetes-containerd.yaml")
	for _, name := range []string{"kubevirt", "cdi"} {
		component := profile["components"].(map[string]any)[name].(map[string]any)
		if component["mode"] != "disabled" || component["compatibility"] != "unverified" {
			t.Fatal("virtualization deferral changed")
		}
	}
	business := read("api/schemas/installation-business-values-v1.schema.json")["properties"].(map[string]any)
	if _, present := business["sp08"]; !present || baseline.ConfigurationLimits["businessValuesWeb"] != true {
		t.Fatal("Web installation boundary changed; update delivery preparation")
	}
	for _, raw := range business["sp06"].(map[string]any)["oneOf"].([]any) {
		if _, present := raw.(map[string]any)["properties"].(map[string]any)["assistant"]; !present || baseline.ConfigurationLimits["businessValuesAssistant"] != true {
			t.Fatal("assistant installation boundary changed; update delivery preparation")
		}
	}
	webRecipe, err := os.ReadFile("../../build/images/web/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(webRecipe), "ld-musl-x86_64.so.1") || baseline.ConfigurationLimits["webImageRecipeArchitecture"] != "amd64" || baseline.ConfigurationLimits["mixedRevisionRuntimeVerified"] != false {
		t.Fatal("architecture/runtime compatibility boundary changed; update delivery preparation")
	}
	for _, path := range baseline.Runbooks {
		if _, err := os.Stat(filepath.Join("../..", path)); err != nil {
			t.Fatal(err)
		}
	}
}
