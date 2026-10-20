package bundle

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPendingModelChartKeepsSP07WithoutInvestigatorOrModelEgress(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm required for actual chart rendering")
	}
	b := readBusinessTest(t, pendingModelDocument())
	image := "example.invalid/runner@sha256:" + strings.Repeat("a", 64)
	// Minimal chart fixture: installer admission of complete execution Profiles
	// remains covered separately; this test exercises stage rendering and defaults.
	b.values["sp07"] = map[string]any{"enabled": true, "gatewayPort": 8084, "policyName": "pending-command", "tenants": b.Tenants(), "admittedProfiles": []any{map[string]any{"toolImageDigest": image}}, "kubernetesEndpoint": "https://kubernetes.default.svc.cluster.local", "hosts": []any{}, "hostReports": []any{}, "kubernetesCredentials": []any{}}
	values := map[string]any{"workloadsEnabled": true, "runtime": map[string]any{"openbaoCABundle": "TEST-PUBLIC-CA", "oidcIssuerURL": "https://oidc.example.invalid/realm", "profile": "kind: test-profile"}, "observability": map[string]any{"scrapeKind": "none"}, "components": map[string]any{"api": map[string]any{"image": image}, "worker": map[string]any{"image": image}, "web": map[string]any{"enabled": false}}}
	if err := applyBusinessValues(values, b, map[string]string{"holmesgpt": image, "command-runner": image}); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"dependencies", "bootstrap-api", "business"} {
		t.Run(stage, func(t *testing.T) {
			values["networkPolicyOnly"] = stage == "dependencies"
			values["bootstrapAPIOnly"] = stage == "bootstrap-api"
			raw, err := yaml.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "values.yaml")
			if err = os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(helm, "template", "ops-platform", "../../deploy/charts/ops-platform", "--namespace", b.Namespace(), "--values", file).CombinedOutput()
			if err != nil {
				t.Fatalf("chart rendering: %v %s", err, out)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(out))
			hasAPI, hasWorker, hasSP07 := false, false, false
			for {
				var document map[string]any
				err := decoder.Decode(&document)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				name := textValue(object(document, "metadata"), "name")
				if document["kind"] == "Deployment" && name == "ops-investigator" {
					t.Fatal("pending investigator deployed")
				}
				if document["kind"] == "Deployment" && name == "ops-api" {
					hasAPI = true
				}
				if document["kind"] == "Deployment" && name == "ops-worker" {
					hasWorker = true
				}
				if name == "ops-sp06-runtime" && object(document, "data")["investigator.json"] != nil {
					t.Fatal("pending model runtime config rendered")
				}
				if name == "ops-sp07-runtime" {
					hasSP07 = true
				}
				if name == "ops-sp06-investigator" && document["kind"] == "NetworkPolicy" {
					policy, _ := json.Marshal(object(document, "spec")["egress"])
					if bytes.Contains(policy, []byte("ipBlock")) {
						t.Fatal("pending model egress opened")
					}
				}
			}
			if stage == "business" && (!hasAPI || !hasWorker || !hasSP07) {
				t.Fatal("independent API/Worker/SP07 missing")
			}
			if bytes.Contains(out, []byte("127.0.0.1:11434")) || bytes.Contains(out, []byte("ops-sp06-model-key")) {
				t.Fatal("Helm model defaults survived")
			}
		})
	}
}
