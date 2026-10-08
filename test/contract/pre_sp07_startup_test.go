package contract_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func currentStartupObjects(t *testing.T, policyOnly bool) []map[string]any {
	return currentStartupObjectsWithPolicy(t, policyOnly, "explicit-policy")
}

func currentStartupObjectsWithPolicy(t *testing.T, policyOnly bool, policyName string) []map[string]any {
	return currentStartupObjectsWithOptions(t, policyOnly, policyName, false)
}

func currentStartupObjectsWithOptions(t *testing.T, policyOnly bool, policyName string, bootstrapAPIOnly bool) []map[string]any {
	t.Helper()
	values := map[string]any{
		"bootstrapAPIOnly":  bootstrapAPIOnly,
		"networkPolicyOnly": policyOnly, "workloadsEnabled": true,
		"workloadIdentity": map[string]any{"enabled": true, "mode": "openbao-kubernetes"},
		"runtime":          map[string]any{"oidcIssuerURL": "https://issuer.example.invalid", "profile": "core", "openbaoCABundle": "public CA"},
		"networkPolicy":    map[string]any{"managedDependencyReleases": []string{"managed-dependencies"}},
		"sp04":             map[string]any{"enabled": true, "allowedWorkerCIDRs": []string{"10.42.0.0/24"}, "archiveBackendLogicalID": "admitted-archive", "kubernetesAPI": map[string]any{"cidrs": []string{"192.0.2.20/32", "2001:db8::20/128"}, "port": 26443}},
		"sp05":             map[string]any{"enabled": true},
		"sp06":             map[string]any{"enabled": true, "policyName": policyName, "tenants": []string{"explicit-tenant"}, "modelCIDRs": []string{"192.0.2.10/32"}},
		"components":       map[string]any{},
	}
	for _, name := range []string{"api", "worker", "web", "investigator"} {
		values["components"].(map[string]any)[name] = map[string]any{"image": "registry.example.invalid/" + name + "@sha256:" + strings.Repeat("0", 64)}
	}
	input := filepath.Join(t.TempDir(), "values.yaml")
	raw, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err = exec.Command("helm", "template", "current", "../../deploy/charts/ops-platform", "--namespace", "current", "-f", input).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, raw)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var objects []map[string]any
	for {
		var obj map[string]any
		if err := decoder.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if obj != nil {
			objects = append(objects, obj)
		}
	}
	return objects
}

func TestCurrentAPIBootstrapCannotStartCollectorsOrInvestigator(t *testing.T) {
	deployments := map[string]bool{}
	for _, obj := range currentStartupObjectsWithOptions(t, false, "explicit-policy", true) {
		if obj["kind"] == "Deployment" {
			deployments[obj["metadata"].(map[string]any)["name"].(string)] = true
		}
	}
	if !deployments["ops-api"] || len(deployments) != 1 {
		t.Fatalf("API initialization started business consumers before formal registration: %v", deployments)
	}
}

func TestCurrentRuntimeConfigurationChangeRollsWorkloads(t *testing.T) {
	checksums := func(policyName string) map[string]string {
		result := map[string]string{}
		for _, obj := range currentStartupObjectsWithPolicy(t, false, policyName) {
			if obj["kind"] != "Deployment" {
				continue
			}
			name := obj["metadata"].(map[string]any)["name"].(string)
			meta := obj["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)
			annotations, _ := meta["annotations"].(map[string]any)
			checksum, _ := annotations["checksum/runtime-config"].(string)
			result[name] = checksum
		}
		return result
	}
	before, after := checksums("explicit-policy-v1"), checksums("explicit-policy-v2")
	for _, name := range []string{"ops-api", "ops-worker", "ops-investigator"} {
		if len(before[name]) != 64 || len(after[name]) != 64 || before[name] == after[name] {
			t.Fatalf("%s does not roll when its runtime configuration changes", name)
		}
	}
}

func TestCurrentOpenBaoTokenReviewHasExactManagedEgress(t *testing.T) {
	for _, policyOnly := range []bool{true, false} {
		found := false
		for _, obj := range currentStartupObjects(t, policyOnly) {
			if obj["kind"] != "NetworkPolicy" || obj["metadata"].(map[string]any)["name"] != "ops-openbao-kubernetes-egress" {
				continue
			}
			found = true
			spec := obj["spec"].(map[string]any)
			selector := spec["podSelector"].(map[string]any)
			labels := selector["matchLabels"].(map[string]any)
			if labels["ops.platform.io/component"] != "openbao" {
				t.Fatal("TokenReview egress selects another component")
			}
			expressions := selector["matchExpressions"].([]any)
			if len(expressions) != 1 {
				t.Fatal("dependency release ownership absent")
			}
			expr := expressions[0].(map[string]any)
			if expr["key"] != "ops.platform.io/release" || expr["operator"] != "In" || len(expr["values"].([]any)) != 1 || expr["values"].([]any)[0] != "managed-dependencies" {
				t.Fatal("shared OpenBao could receive egress")
			}
			egress := spec["egress"].([]any)
			if len(egress) != 2 {
				t.Fatal("unexpected destinations")
			}
			for i, entry := range egress {
				entry := entry.(map[string]any)
				peers := entry["to"].([]any)
				ports := entry["ports"].([]any)
				want := []string{"192.0.2.20/32", "2001:db8::20/128"}[i]
				if len(peers) != 1 || peers[0].(map[string]any)["ipBlock"].(map[string]any)["cidr"] != want || len(ports) != 1 || ports[0].(map[string]any)["port"] != 26443 || ports[0].(map[string]any)["protocol"] != "TCP" {
					t.Fatal("TokenReview destination widened or incorrect")
				}
			}
		}
		if !found {
			t.Fatalf("OpenBao TokenReview egress absent (networkPolicyOnly=%v)", policyOnly)
		}
	}
}

func TestCurrentInvestigatorSecretMountsAreDisjointAfterRunSymlink(t *testing.T) {
	var keyFile, keyMount string
	var mounts []string
	for _, obj := range currentStartupObjects(t, false) {
		name := obj["metadata"].(map[string]any)["name"]
		if obj["kind"] == "ConfigMap" && name == "ops-sp06-runtime" {
			var cfg map[string]any
			if err := json.Unmarshal([]byte(obj["data"].(map[string]any)["investigator.json"].(string)), &cfg); err != nil {
				t.Fatal(err)
			}
			keyFile = cfg["modelAPIKeyFile"].(string)
		}
		if obj["kind"] != "Deployment" || name != "ops-investigator" {
			continue
		}
		pod := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
		container := pod["containers"].([]any)[0].(map[string]any)
		if container["securityContext"].(map[string]any)["readOnlyRootFilesystem"] != true {
			t.Fatal("root filesystem is writable")
		}
		for _, m := range container["volumeMounts"].([]any) {
			m := m.(map[string]any)
			original := m["mountPath"].(string)
			canonical := path.Clean(strings.Replace(original, "/var/run/", "/run/", 1))
			if m["name"] == "model-key" {
				keyMount = original
				if m["readOnly"] != true {
					t.Fatal("model credential is writable")
				}
			}
			if m["name"] == "openbao-identity" && (original != "/var/run/secrets/ops-platform/openbao" || m["readOnly"] != true) {
				t.Fatal("projected JWT identity changed")
			}
			for _, previous := range mounts {
				if canonical == previous || strings.HasPrefix(canonical, previous+"/") || strings.HasPrefix(previous, canonical+"/") {
					t.Fatalf("nested mounts after /var/run symlink: %s and %s", previous, canonical)
				}
			}
			mounts = append(mounts, canonical)
		}
	}
	if keyFile == "" || keyMount == "" || keyFile != keyMount+"/model-api-key" {
		t.Fatal("runtime credential path differs from mounted Secret")
	}
}
