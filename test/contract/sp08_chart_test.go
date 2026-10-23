package contract_test

import (
	"bytes"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSP08WebChartTLSAndPermissionIsolation(t *testing.T) {
	values := map[string]any{
		"workloadsEnabled": true, "runtime": map[string]any{"oidcIssuerURL": "https://identity.example.invalid", "profile": "core"},
		"global":     map[string]any{"imagePullPolicy": "Never"},
		"components": map[string]any{},
		"sp08": map[string]any{"enabled": true, "publicOrigin": "https://ops.example.invalid:30443", "tlsSecretName": "web-tls", "oidcCAConfigMapName": "oidc-public-ca",
			"oidc":         map[string]any{"authority": "https://identity.example.invalid/realms/ops", "clientId": "public-pkce", "proxyHost": "identity.example.invalid"},
			"service":      map[string]any{"type": "NodePort", "port": 8443, "nodePort": 30443},
			"ingressCIDRs": []string{"192.0.2.0/24"},
		},
	}
	for _, name := range []string{"api", "worker", "web"} {
		values["components"].(map[string]any)[name] = map[string]any{"enabled": true, "image": "registry.example.invalid/" + name + "@sha256:" + strings.Repeat("0", 64)}
	}
	render := func(v map[string]any) ([]byte, error) {
		raw, err := yaml.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "values.yaml")
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return exec.Command("helm", "template", "sp08", "../../deploy/charts/ops-platform", "--namespace", "ops-system", "-f", path).CombinedOutput()
	}
	output, err := render(values)
	if err != nil {
		t.Fatalf("render: %v\n%s", err, output)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	webDeployments, webServices := 0, 0
	server := ""
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
		meta, _ := obj["metadata"].(map[string]any)
		if meta["name"] == "ops-web-runtime" {
			data := obj["data"].(map[string]any)
			server = data["server.conf"].(string)
			config := data["runtime-config.json"].(string)
			if strings.Contains(config, "password") || strings.Contains(config, "clientSecret") {
				t.Fatal("private identity material exposed")
			}
		}
		if meta["name"] != "ops-web" {
			continue
		}
		switch obj["kind"] {
		case "Deployment":
			webDeployments++
			spec := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			if spec["automountServiceAccountToken"] != false {
				t.Fatal("Web has a Kubernetes token")
			}
			podSecurity := spec["securityContext"].(map[string]any)
			if podSecurity["runAsNonRoot"] != true || podSecurity["runAsUser"] != 101 {
				t.Fatal("Web does not use its numeric non-root identity")
			}
			c := spec["containers"].([]any)[0].(map[string]any)
			security := c["securityContext"].(map[string]any)
			if security["readOnlyRootFilesystem"] != true || security["allowPrivilegeEscalation"] != false {
				t.Fatal("unsafe Web runtime")
			}
			if c["imagePullPolicy"] != "Never" {
				t.Fatal("offline deployment pulls images")
			}
			if len(spec["volumes"].([]any)) != 4 {
				t.Fatal("TLS and CA must be independent read-only mounts")
			}
		case "Service":
			webServices++
			spec := obj["spec"].(map[string]any)
			if spec["type"] != "NodePort" || spec["ports"].([]any)[0].(map[string]any)["nodePort"] != 30443 {
				t.Fatal("public access port is not explicit")
			}
		}
	}
	if webDeployments != 1 || webServices != 1 {
		t.Fatalf("duplicate/missing Web resources: %d %d", webDeployments, webServices)
	}
	for _, required := range []string{"proxy_buffering off", "proxy_cache off", "proxy_ssl_verify on", "proxy_ssl_server_name on", "Content-Security-Policy", "X-Content-Type-Options", "location = /healthz", "location /api/", "location /oidc/"} {
		if !strings.Contains(server, required) {
			t.Errorf("proxy protection missing: %s", required)
		}
	}
	for _, attack := range []struct{ key, value string }{
		{"publicOrigin", "https://ops.example.invalid/unsafe"},
		{"publicOrigin", "http://ops.example.invalid"},
		{"publicOrigin", "https://ops.example.invalid:65536"},
	} {
		original := values["sp08"].(map[string]any)[attack.key]
		values["sp08"].(map[string]any)[attack.key] = attack.value
		if output, err := render(values); err == nil {
			t.Fatalf("invalid origin accepted: %s", output)
		}
		values["sp08"].(map[string]any)[attack.key] = original
	}
	oidc := values["sp08"].(map[string]any)["oidc"].(map[string]any)
	oidc["proxyHost"] = "identity.example.invalid; injected"
	if output, err := render(values); err == nil {
		t.Fatalf("proxy config injection accepted: %s", output)
	}
	oidc["proxyHost"] = "identity.example.invalid"
	values["components"].(map[string]any)["web"].(map[string]any)["image"] = "registry.example.invalid/web:latest"
	if output, err := render(values); err == nil {
		t.Fatalf("mutable Web image accepted: %s", output)
	}
}
