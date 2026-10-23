package contract_test

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

// Rendering is local-only; it proves no Kubernetes cold start or OpenBao policy.
func TestNativeAuthChartHasNoOIDCDependencyAndProjectsMFADuty(t *testing.T) {
	runtime := map[string]any{"humanAuthMode": "native", "profile": "core", "openbaoCABundle": "independently provisioned CA", "nativeAuth": map[string]any{"tenantId": "018f0f2b-91c2-7d42-a8dc-f719c5987201", "publicOrigin": "https://ops.example.invalid:30443", "mfaTransitKey": "ops-native-mfa"}}
	values := map[string]any{"workloadsEnabled": true, "runtime": runtime, "components": map[string]any{}, "sp08": map[string]any{"enabled": true, "publicOrigin": "https://ops.example.invalid:30443", "tlsSecretName": "web-tls", "ingressCIDRs": []string{"192.0.2.0/24"}}}
	for _, name := range []string{"api", "worker", "web"} {
		values["components"].(map[string]any)[name] = map[string]any{"enabled": true, "image": "registry.example.invalid/" + name + "@sha256:" + strings.Repeat("0", 64)}
	}
	render := func() ([]byte, error) {
		raw, err := yaml.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "values.yaml")
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return exec.Command("helm", "template", "native", "../../deploy/charts/ops-platform", "--namespace", "native", "-f", path).CombinedOutput()
	}
	for _, sp06 := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-investigator", true: "with-investigator"}[sp06], func(t *testing.T) {
			if sp06 {
				values["sp04"] = map[string]any{"enabled": true, "allowedWorkerCIDRs": []string{"10.42.0.0/24"}, "archiveBackendLogicalID": "admitted-archive"}
				values["sp05"] = map[string]any{"enabled": true}
				values["sp06"] = map[string]any{"enabled": true, "tenants": []string{"explicit-tenant"}, "modelCIDRs": []string{"192.0.2.21/32"}}
				values["components"].(map[string]any)["investigator"] = map[string]any{"image": "registry.example.invalid/investigator@sha256:" + strings.Repeat("0", 64)}
			}
			output, err := render()
			if err != nil {
				t.Fatalf("native render: %v\n%s", err, output)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(output))
			seen := map[string]bool{}
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
				name := obj["metadata"].(map[string]any)["name"].(string)
				if obj["kind"] == "ConfigMap" && name == "ops-web-runtime" {
					data := obj["data"].(map[string]any)
					var config map[string]any
					if err = json.Unmarshal([]byte(data["runtime-config.json"].(string)), &config); err != nil {
						t.Fatal(err)
					}
					if config["auth"].(map[string]any)["mode"] != "native" || config["oidc"] != nil {
						t.Fatal("Web requires OIDC in native mode")
					}
					server := data["server.conf"].(string)
					if strings.Contains(server, "ops-oidc") || strings.Contains(server, "proxy_ssl") || strings.Contains(server, "${") || !strings.Contains(server, "location ^~ /oidc/ { return 404; }") {
						t.Fatal("native proxy retains identity dependency or unresolved variables")
					}
					seen[name] = true
				}
				if obj["kind"] != "Deployment" || (name != "ops-api" && name != "ops-worker" && name != "ops-web") {
					continue
				}
				seen[name] = true
				pod := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
				if pod["automountServiceAccountToken"] != false {
					t.Fatal("unrestricted service account token")
				}
				c := pod["containers"].([]any)[0].(map[string]any)
				env := map[string]string{}
				if raw, ok := c["env"].([]any); ok {
					for _, item := range raw {
						entry := item.(map[string]any)
						key := entry["name"].(string)
						if _, exists := env[key]; exists {
							t.Fatalf("duplicate env %s in %s", key, name)
						}
						env[key], _ = entry["value"].(string)
					}
				}
				if _, ok := env["OIDC_ISSUER_URL"]; ok {
					t.Fatal("OIDC issuer leaked into native deployment")
				}
				if name == "ops-web" {
					encoded, _ := json.Marshal(pod)
					if bytes.Contains(encoded, []byte("oidc-ca")) || bytes.Contains(encoded, []byte("serviceAccountToken")) {
						t.Fatal("Web has obsolete CA or workload token")
					}
					continue
				}
				if env["HUMAN_AUTH_MODE"] != "native" || env["NATIVE_AUTH_TENANT_ID"] == "" || env["OPENBAO_PROJECTED_TOKEN_FILE"] != "/var/run/secrets/ops-platform/openbao/token" {
					t.Fatal("API/Worker native or projected identity config missing")
				}
				projected := false
				for _, item := range pod["volumes"].([]any) {
					volume := item.(map[string]any)
					if volume["name"] == "openbao-identity" {
						token := volume["projected"].(map[string]any)["sources"].([]any)[0].(map[string]any)["serviceAccountToken"].(map[string]any)
						projected = token["audience"] == "openbao" && token["expirationSeconds"] == 3600
					}
				}
				if !projected {
					t.Fatal("bounded OpenBao audience token not projected")
				}
				if name == "ops-api" {
					if env["OPENBAO_MFA_TRANSIT_KEY"] != "ops-native-mfa" || env["NATIVE_AUTH_PUBLIC_ORIGIN"] != "https://ops.example.invalid:30443" || env["OPENBAO_CA_FILE"] != "/etc/ops/openbao-ca.pem" {
						t.Fatal("native MFA machine identity not wired")
					}
				} else if env["OPENBAO_MFA_TRANSIT_KEY"] != "" {
					t.Fatal("Worker received MFA Transit duty")
				}
			}
			for _, name := range []string{"ops-api", "ops-worker", "ops-web", "ops-web-runtime"} {
				if !seen[name] {
					t.Fatalf("missing %s", name)
				}
			}
		})
	}
	native := runtime["nativeAuth"].(map[string]any)
	for _, bad := range []struct{ key, value string }{{"tenantId", "not-a-uuid"}, {"tenantId", "00000000-0000-0000-0000-000000000000"}, {"publicOrigin", "http://ops.example.invalid"}, {"publicOrigin", "https://different.example.invalid"}, {"publicOrigin", "https://ops.example.invalid:65536"}, {"mfaTransitKey", ""}, {"mfaTransitKey", "key; injected"}} {
		previous := native[bad.key]
		native[bad.key] = bad.value
		if _, err := render(); err == nil {
			t.Fatalf("accepted invalid native %s", bad.key)
		}
		native[bad.key] = previous
	}
	runtime["openbaoCABundle"] = ""
	if _, err := render(); err == nil {
		t.Fatal("accepted missing independent CA")
	}
	runtime["openbaoCABundle"] = "independent CA"
	values["workloadIdentity"] = map[string]any{"enabled": false}
	if _, err := render(); err == nil {
		t.Fatal("accepted missing projected identity")
	}
}

func TestProjectedAPIPKIWithoutInvestigatorKeepsOpenBaoConfiguration(t *testing.T) {
	values := map[string]any{
		"workloadsEnabled": true,
		"workloadIdentity": map[string]any{"enabled": true, "mode": "openbao-kubernetes"},
		"runtime":          map[string]any{"profile": "core", "oidcIssuerURL": "https://identity.example.invalid/realms/ops", "openbaoCABundle": "independent public CA"},
		"sp04":             map[string]any{"enabled": true, "allowedWorkerCIDRs": []string{"10.42.0.0/24"}, "archiveBackendLogicalID": "admitted-archive"},
		"components":       map[string]any{},
	}
	for _, name := range []string{"api", "worker", "web"} {
		values["components"].(map[string]any)[name] = map[string]any{"image": "registry.example.invalid/" + name + "@sha256:" + strings.Repeat("0", 64)}
	}
	raw, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("helm", "template", "pki", "../../deploy/charts/ops-platform", "--namespace", "pki", "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, output)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	seen := false
	for {
		var obj map[string]any
		if err := decoder.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if obj == nil || obj["kind"] != "Deployment" || obj["metadata"].(map[string]any)["name"] != "ops-api" {
			continue
		}
		seen = true
		pod := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
		container := pod["containers"].([]any)[0].(map[string]any)
		env := map[string]string{}
		for _, item := range container["env"].([]any) {
			entry := item.(map[string]any)
			name := entry["name"].(string)
			if _, exists := env[name]; exists {
				t.Fatalf("duplicate env %s", name)
			}
			env[name], _ = entry["value"].(string)
		}
		for _, key := range []string{"OPENBAO_ADDR", "OPENBAO_CA_FILE", "OPENBAO_PROJECTED_TOKEN_FILE", "OIDC_ISSUER_URL"} {
			if env[key] == "" {
				t.Errorf("projected PKI API missing %s without SP06", key)
			}
		}
		if env["OPENBAO_MFA_TRANSIT_KEY"] != "" {
			t.Fatal("OIDC API received native MFA duty")
		}
	}
	if !seen {
		t.Fatal("API deployment missing")
	}
}
