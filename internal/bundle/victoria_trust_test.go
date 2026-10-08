package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/profile"
)

func nativeVictoriaBusiness(t *testing.T) (BusinessValues, profile.ResolvedProfile) {
	t.Helper()
	d := validBusinessDocument()
	p := importProfile()
	for _, name := range []string{"victoriametrics", "victorialogs"} {
		component, service, port := "victoriaMetrics", "ops-victoria-metrics", "8428"
		if name == "victorialogs" {
			component, service, port = "victoriaLogs", "ops-victoria-logs", "9428"
		}
		endpoint := "https://" + service + ".ops-system.svc.cluster.local:" + port
		p.Components[component] = profile.ResolvedComponent{Mode: "bundled", Namespace: "ops-system", Endpoint: endpoint}
		for _, x := range object(d, "sp04")["sources"].([]any) {
			source := x.(map[string]any)
			if textValue(source, "Name") == name {
				object(source, "Binding")["Endpoint"] = endpoint
			}
		}
	}
	return readBusinessTest(t, d), p
}

func TestCurrentVictoriaRequiresMatchingHTTPSAndCredentialProjections(t *testing.T) {
	b, p := nativeVictoriaBusiness(t)
	targets, err := b.VictoriaTrustTargets(p)
	if err != nil || len(targets) != 2 {
		t.Fatal("explicit bundled source identities rejected")
	}
	if err := preflightVictoriaTrust(context.Background(), p, b, func(context.Context, string, ...string) ([]byte, error) { return []byte("absent"), nil }); err == nil {
		t.Fatal("missing native TLS/authentication admitted")
	}
	if err := preflightVictoriaTrust(context.Background(), p, b, func(_ context.Context, program string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if program != "kubectl" || !strings.Contains(joined, "-n ops-system get secret") || !strings.Contains(joined, "go-template=") || strings.Contains(joined, "-o json") {
			t.Fatal("preflight requested private source values")
		}
		return []byte("present"), nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(BusinessValues, profile.ResolvedProfile){
		func(_ BusinessValues, p profile.ResolvedProfile) {
			c := p.Components["victoriaMetrics"]
			c.Endpoint = strings.Replace(c.Endpoint, "https://", "http://", 1)
			p.Components["victoriaMetrics"] = c
		},
		func(_ BusinessValues, p profile.ResolvedProfile) {
			c := p.Components["victoriaMetrics"]
			c.Namespace = "foreign-installation"
			p.Components["victoriaMetrics"] = c
		},
		func(b BusinessValues, _ profile.ResolvedProfile) {
			source := object(b.values, "sp04")["sources"].([]any)[0].(map[string]any)
			object(source, "Binding")["Endpoint"] = "https://wrong-source.example.invalid:8428"
		},
		func(b BusinessValues, _ profile.ResolvedProfile) {
			source := object(b.values, "sp04")["sources"].([]any)[0].(map[string]any)
			source["CredentialFile"] = source["CAFile"]
		},
	} {
		b, p := nativeVictoriaBusiness(t)
		mutate(b, p)
		if _, err := b.VictoriaTrustTargets(p); err == nil {
			t.Fatal("unbound native source trust accepted")
		}
	}
}

func TestOfficialVictoriaChartsCarryNativeTLSAndSeparatedClientTrust(t *testing.T) {
	_, p := nativeVictoriaBusiness(t)
	for _, item := range []struct{ name, component, chart string }{
		{"victoria-metrics", "victoriaMetrics", "victoria-metrics-single-0.18.0.tgz"},
		{"victoria-logs", "victoriaLogs", "victoria-logs-single-0.13.9.tgz"},
		{"vmalert", "vmalert", "victoria-metrics-alert-0.18.0.tgz"},
	} {
		t.Run(item.name, func(t *testing.T) {
			component := p.Components[item.component]
			component.Image = "registry.example.invalid/" + item.name + "@sha256:" + strings.Repeat("1", 64)
			component.Version = "v1.0.0"
			values, err := victoriaChartValues(item.name, component, p)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "values.yaml")
			raw, _ := yaml.Marshal(values)
			if os.WriteFile(file, raw, 0600) != nil {
				t.Fatal("values unavailable")
			}
			raw, err = exec.CommandContext(t.Context(), "helm", "template", "current", filepath.Join("../../deploy/addons/victoria/charts", item.chart), "-f", file, "--namespace", "ops-system").CombinedOutput()
			if err != nil {
				t.Fatalf("official chart render failed: %v", err)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(raw))
			found := false
			for {
				var resource map[string]any
				if err := decoder.Decode(&resource); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if resource["kind"] != "StatefulSet" && resource["kind"] != "Deployment" {
					continue
				}
				pod := object(object(object(resource, "spec"), "template"), "spec")
				encoded, _ := json.Marshal(pod)
				if !bytes.Contains(encoded, []byte("/etc/victoria-security")) || !bytes.Contains(encoded, []byte("OPS_")) {
					t.Fatal("native TLS/authentication projection missing")
				}
				containers := pod["containers"].([]any)
				container := containers[0].(map[string]any)
				if item.name != "vmalert" {
					args, _ := json.Marshal(container["args"])
					if bytes.Contains(args, []byte("-tls=false")) || !bytes.Contains(args, []byte("/etc/victoria-security/tls.crt")) || !bytes.Contains(args, []byte("/etc/victoria-security/tls.key")) {
						t.Fatal("official listener overrides native TLS identity")
					}
					if textValue(object(object(container, "readinessProbe"), "httpGet"), "scheme") != "HTTPS" || !bytes.Contains(encoded, []byte("file:///etc/victoria-security/password")) {
						t.Fatal("official TLS probe or native file credential missing")
					}
					probe := object(container, "livenessProbe")
					if probe["httpGet"] != nil && probe["tcpSocket"] != nil {
						t.Fatal("invalid dual-handler probe")
					}
				} else {
					if !bytes.Contains(encoded, []byte("datasource.tlsCAFile")) || !bytes.Contains(encoded, []byte("remoteWrite.basicAuth.passwordFile")) || bytes.Contains(encoded, []byte(`"key":"tls.key"`)) {
						t.Fatal("vmalert client trust or credential duty differs")
					}
				}
				found = true
			}
			if !found {
				t.Fatal("native workload missing")
			}
		})
	}
}
