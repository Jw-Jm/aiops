package contract_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCurrentChartProjectsContinuousInvestigatorIdentity(t *testing.T) {
	values := map[string]any{
		"workloadsEnabled": true,
		"workloadIdentity": map[string]any{"enabled": true, "mode": "openbao-kubernetes"},
		"runtime":          map[string]any{"oidcIssuerURL": "https://issuer.example.invalid", "profile": "core", "openbaoCABundle": "independently provisioned public CA"},
		"components":       map[string]any{},
		"sp04":             map[string]any{"enabled": true, "allowedWorkerCIDRs": []string{"10.42.0.0/24"}, "archiveBackendLogicalID": "admitted-archive", "kubernetesAPI": map[string]any{"cidrs": []string{"192.0.2.20/32"}, "port": 26443}},
		"sp05":             map[string]any{"enabled": true},
		"sp06":             map[string]any{"enabled": true, "tenants": []string{"explicit-tenant"}, "modelCIDRs": []string{"192.168.139.1/32"}},
	}
	for _, name := range []string{"api", "worker", "web", "investigator"} {
		values["components"].(map[string]any)[name] = map[string]any{"image": "registry.example.invalid/" + name + "@sha256:0000000000000000000000000000000000000000000000000000000000000000"}
	}
	path := filepath.Join(t.TempDir(), "values.yaml")
	raw, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("helm", "template", "current", "../../deploy/charts/ops-platform", "--namespace", "current", "-f", path)
	raw, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, raw)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	configs, investigator, policy := 0, false, false
	for {
		var obj map[string]any
		if err := decoder.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if obj == nil {
			continue
		}
		meta := obj["metadata"].(map[string]any)
		if obj["kind"] == "ConfigMap" && (meta["name"] == "ops-sp04-runtime" || meta["name"] == "ops-sp06-runtime") {
			for _, value := range obj["data"].(map[string]any) {
				var cfg map[string]any
				if err := json.Unmarshal([]byte(value.(string)), &cfg); err != nil {
					t.Fatal(err)
				}
				mode := cfg["identityMode"]
				if meta["name"] == "ops-sp04-runtime" {
					mode = cfg["IdentityMode"]
				}
				if mode != "openbao-kubernetes" {
					t.Fatal("static identity reached current runtime")
				}
				configs++
			}
		}
		if obj["kind"] == "Deployment" && meta["name"] == "ops-investigator" {
			pod := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			if pod["automountServiceAccountToken"] != false || pod["serviceAccountName"] != "ops-investigator" {
				t.Fatal("investigator identity is not separated")
			}
			encoded, _ := json.Marshal(pod)
			for _, required := range []string{"OPENBAO_PROJECTED_TOKEN_FILE", "OPENBAO_CA_FILE", "serviceAccountToken", `"audience":"openbao"`} {
				if !bytes.Contains(encoded, []byte(required)) {
					t.Fatalf("missing %s", required)
				}
			}
			for _, forbidden := range []string{"DATABASE_URL", "sp04-sources", "archive-credentials"} {
				if bytes.Contains(encoded, []byte(forbidden)) {
					t.Fatalf("investigator received %s", forbidden)
				}
			}
			investigator = true
		}
		if obj["kind"] == "NetworkPolicy" && meta["name"] == "ops-sp06-investigator" {
			encoded, _ := json.Marshal(obj["spec"])
			if !bytes.Contains(encoded, []byte(`"ops.platform.io/component":"openbao"`)) || !bytes.Contains(encoded, []byte(`"port":8200`)) {
				t.Fatal("PKI egress absent")
			}
			for _, forbidden := range []string{"5432", "8333", "8428", "9428", "0.0.0.0/0", "::/0"} {
				if bytes.Contains(encoded, []byte(forbidden)) {
					t.Fatalf("investigator egress widened to %s", forbidden)
				}
			}
			policy = true
		}
	}
	if configs != 5 || !investigator || !policy {
		t.Fatalf("incomplete current wiring: %d %v %v", configs, investigator, policy)
	}
}
