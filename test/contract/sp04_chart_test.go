package contract_test

import (
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSP04ChartUsesExistingWorkerAndSeparateCredentials(t *testing.T) {
	values := map[string]any{"workloadsEnabled": true, "runtime": map[string]any{"oidcIssuerURL": "https://keycloak.example.invalid", "profile": "core"}, "components": map[string]any{}}
	values["global"] = map[string]any{"imagePullPolicy": "Never"}
	for _, name := range []string{"api", "worker", "web"} {
		values["components"].(map[string]any)[name] = map[string]any{"image": "registry.example.invalid/platform/" + name + "@sha256:0000000000000000000000000000000000000000000000000000000000000000"}
	}
	values["sp04"] = map[string]any{"enabled": true, "allowedWorkerCIDRs": []string{"10.0.0.0/24"}, "archiveBackendLogicalID": "archive-qualified", "kubernetesAPI": map[string]any{"localCollector": true, "cidrs": []string{"10.96.0.1/32"}}, "leaseNames": []string{"sp04-graph"}, "clusters": []any{map[string]any{"Tenant": "tenant-a", "ClusterUID": "cluster-a", "SourceID": "source-a", "SourceRevision": 1, "BackendLogicalID": "cluster-a", "Endpoint": "https://10.96.0.1:443", "TokenFile": "/var/run/secrets/ops-platform/kubernetes/token", "CAFile": "/var/run/secrets/ops-platform/kubernetes/ca.pem", "LeaseNamespace": "ops-system", "LeaseName": "sp04-graph"}}}
	path := filepath.Join(t.TempDir(), "values.yaml")
	raw, _ := yaml.Marshal(values)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("helm", "template", "sp04", "../../deploy/charts/ops-platform", "--namespace", "ops-system", "-f", path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("render: %s %v", output, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	deployments := 0
	foundRuntime := false
	foundIngress := false
	for {
		var obj map[string]any
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if obj == nil {
			continue
		}
		kind, _ := obj["kind"].(string)
		meta, _ := obj["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if kind == "ConfigMap" && name == "ops-sp04-runtime" {
			data := obj["data"].(map[string]any)
			for _, process := range []string{"api", "worker"} {
				var cfg map[string]any
				if json.Unmarshal([]byte(data[process+".json"].(string)), &cfg) != nil {
					t.Fatal("runtime JSON malformed")
				}
				if process == "api" {
					if _, exists := cfg["Sources"]; exists {
						t.Fatal("API received source credentials/config")
					}
				}
			}
			foundRuntime = true
		}
		if kind == "Deployment" {
			deployments++
			pod := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			for _, c := range pod["containers"].([]any) {
				if c.(map[string]any)["imagePullPolicy"] != "Never" {
					t.Fatal("offline Chart permits image pulls")
				}
			}
			if name == "ops-worker" {
				spec := obj["spec"].(map[string]any)
				if spec["replicas"] != 2 {
					t.Fatal("QPS requires two Workers")
				}
				encoded, _ := json.Marshal(spec)
				if !bytes.Contains(encoded, []byte("SP04_OWNER_ENDPOINT")) || !bytes.Contains(encoded, []byte("graph-mtls")) {
					t.Fatal("Worker route/listener not wired")
				}
			}
		}
		if kind == "ClusterRole" {
			rules, _ := json.Marshal(obj["rules"])
			for _, forbidden := range []string{"kubevirt", "datavolumes", "secrets", "create", "delete"} {
				if bytes.Contains(rules, []byte(forbidden)) {
					t.Fatal("collector exceeded readonly nonvirtual scope")
				}
			}
		}
		if kind == "NetworkPolicy" && name == "ops-sp04-worker-graph-ingress" {
			encoded, _ := json.Marshal(obj["spec"])
			if !bytes.Contains(encoded, []byte("api")) || !bytes.Contains(encoded, []byte("8082")) {
				t.Fatal("internal graph ingress missing")
			}
			foundIngress = true
		}
	}
	if deployments != 3 || !foundRuntime || !foundIngress {
		t.Fatalf("third service or incomplete wiring: deployments=%d runtime=%v ingress=%v", deployments, foundRuntime, foundIngress)
	}
	command.Args = append(command.Args, "--set", "sp04.workerReplicas=3")
	if _, err := command.CombinedOutput(); err == nil {
		t.Fatal("unbudgeted third Worker accepted")
	}
}
