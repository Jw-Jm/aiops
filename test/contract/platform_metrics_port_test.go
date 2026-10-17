package contract_test

import (
	"fmt"
	"os/exec"
	"testing"
)

func TestPlatformMetricsListenerMatchesDeclaredPort(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom=%v", custom), func(t *testing.T) {
			args := []string{"template", "metrics-port", "../../deploy/charts/ops-platform", "--set", "workloadsEnabled=true", "--set", "runtime.profile=core", "--set", "runtime.oidcIssuerURL=https://keycloak.example.invalid"}
			for _, name := range []string{"api", "worker", "web"} {
				args = append(args, "--set", "components."+name+".image=registry.example.invalid/platform/test@sha256:0000000000000000000000000000000000000000000000000000000000000000")
			}
			expected := map[string]int{"api": 9090, "worker": 9090}
			if custom {
				expected["api"], expected["worker"] = 9190, 9191
				for name, port := range expected {
					args = append(args, "--set", fmt.Sprintf("components.%s.metricsPort=%d", name, port))
				}
			}
			output, err := exec.Command("helm", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("render metrics ports: %v: %s", err, output)
			}
			found := map[string]bool{}
			for _, r := range parseRenderedResources(t, output) {
				component := r.Metadata.Labels["ops.platform.io/component"]
				port, wanted := expected[component]
				if r.Kind != "Deployment" || !wanted {
					continue
				}
				found[component] = true
				for _, c := range r.Spec.Template.Spec.Containers {
					declared := 0
					for _, p := range c.Ports {
						if p.Name == "metrics" {
							declared = p.ContainerPort
						}
					}
					address := ""
					for _, e := range c.Env {
						if e.Name == "PLATFORM_METRICS_ADDR" {
							address = e.Value
						}
					}
					if declared != port || address != fmt.Sprintf(":%d", port) {
						t.Errorf("%s metrics port=%d listener=%q, expected %d", component, declared, address, port)
					}
					if component == "worker" {
						for _, p := range []*renderedHTTPProbe{c.StartupProbe, c.ReadinessProbe, c.LivenessProbe} {
							if p == nil || p.HTTPGet.Port != "metrics" {
								t.Errorf("worker probe does not target metrics listener: %+v", p)
							}
						}
					}
				}
			}
			if !found["api"] || !found["worker"] {
				t.Fatalf("missing workloads: %v", found)
			}
		})
	}
}
