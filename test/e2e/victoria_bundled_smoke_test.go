package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ops-platform/internal/profile"
)

func TestVictoriaBundledSmoke(t *testing.T) {
	if os.Getenv("OPS_ORBSTACK_VICTORIA_DISCOVERY") != "1" {
		t.Skip("set OPS_ORBSTACK_VICTORIA_DISCOVERY=1 to probe existing OrbStack sources")
	}

	before := monitoringWorkloadCount(t)
	discovery, err := profile.Discover(context.Background(), "orbstack", "kubectl", "../../bundle/component-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	input, err := profile.ReadProfileFile("../../deploy/profiles/dev-orbstack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	input.Components = map[string]profile.ComponentInput{
		"victoriaMetrics": {Mode: "detect"},
		"victoriaLogs":    {Mode: "detect"},
		"vmalert":         {Mode: "detect", Endpoint: "http://ops-vmalert.ops-system.svc.cluster.local:8880"},
	}
	detected, err := profile.Detect(input, discovery)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := profile.Resolve(context.Background(), detected, discovery)
	if err != nil {
		t.Fatal(err)
	}
	for name, wantVersion := range map[string]string{"victoriaMetrics": "v1.116.0", "victoriaLogs": "v1.52.0"} {
		component := resolved.Components[name]
		if component.Mode != "external" || component.Version != wantVersion || component.Endpoint == "" || logicalSourceID(component) == "" {
			t.Fatalf("%s was not resolved from the existing service: %#v", name, component)
		}
		joined := strings.Join(component.Evidence, "\n")
		if !strings.Contains(joined, "metrics=available") && name == "victoriaMetrics" {
			t.Fatalf("metrics capability evidence missing for %s: %s", name, joined)
		}
		if !strings.Contains(joined, "logs=available") && name == "victoriaLogs" {
			t.Fatalf("logs capability evidence missing for %s: %s", name, joined)
		}
	}
	wantVMAlertMode := "bundled"
	if candidates := discovery.Components["vmalert"]; len(candidates) == 1 {
		wantVMAlertMode = "external"
		if resolved.Components["vmalert"].ObjectUID != candidates[0].ObjectUID {
			t.Fatal("vmalert discovery identity was not retained")
		}
	}
	if resolved.Components["vmalert"].Mode != wantVMAlertMode {
		t.Fatalf("vmalert mode = %q, want %s from actual read-only discovery", resolved.Components["vmalert"].Mode, wantVMAlertMode)
	}
	wantInstallable := true
	for _, component := range resolved.Components {
		if component.Mode == "bundled" && component.AdmissionState != "qualified" {
			wantInstallable = false
		}
	}
	if resolved.Installable != wantInstallable {
		t.Fatalf("installable=%v, want %v from actual bundled component admission", resolved.Installable, wantInstallable)
	}
	if after := monitoringWorkloadCount(t); after != before {
		t.Fatalf("monitoring workload count changed during read-only discovery: %d -> %d", before, after)
	}
}

func TestVictoriaBundledOfflineInstall(t *testing.T) {
	if os.Getenv("OPS_ORBSTACK_VICTORIA_BUNDLED_SMOKE") != "1" {
		t.Skip("set OPS_ORBSTACK_VICTORIA_BUNDLED_SMOKE=1 to install the digest-pinned charts in an isolated offline namespace")
	}

	const namespace = "ops-task24-victoria"
	if output, err := runTask24Command("kubectl", "--context", "orbstack", "get", "namespace", namespace, "-o", "name"); err == nil {
		t.Fatalf("refusing to use pre-existing namespace %s: %s", namespace, strings.TrimSpace(output))
	} else if !strings.Contains(err.Error(), "NotFound") && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cannot verify namespace absence: %v", err)
	}
	if _, err := runTask24Command("kubectl", "--context", "orbstack", "create", "namespace", namespace); err != nil {
		t.Fatalf("create isolated namespace: %v", err)
	}
	if _, err := runTask24Command("kubectl", "--context", "orbstack", "label", "namespace", namespace, "ops.platform.io/test-release=sp02-task-2-4"); err != nil {
		t.Fatalf("label isolated namespace: %v", err)
	}
	t.Cleanup(func() {
		out, err := runTask24Command("kubectl", "--context", "orbstack", "get", "namespace", namespace, "-o", "json")
		var object struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		}
		if err != nil || json.Unmarshal([]byte(out), &object) != nil || object.Metadata.Labels["ops.platform.io/test-release"] != "sp02-task-2-4" {
			t.Errorf("refusing cleanup because namespace ownership label was not verified: %v", err)
			return
		}
		for _, release := range []string{"sp02-victoria-metrics", "sp02-victoria-logs", "sp02-vmalert"} {
			_, _ = runTask24Command("helm", "uninstall", release, "--wait", "--timeout", "60s", "--namespace", namespace)
		}
		_, err = runTask24Command("kubectl", "--context", "orbstack", "delete", "namespace", namespace, "--wait=true", "--timeout=120s")
		if err != nil {
			t.Errorf("clean isolated test namespace: %v", err)
		}
	})

	denyEgress := fmt.Sprintf("apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: sp02-victoria-deny-egress\n  namespace: %s\n  labels:\n    ops.platform.io/test-release: sp02-task-2-4\nspec:\n  podSelector: {}\n  policyTypes: [Egress]\n  egress: []\n", namespace)
	if _, err := runTask24WithInput(denyEgress, "kubectl", "--context", "orbstack", "apply", "-f", "-"); err != nil {
		t.Fatalf("deny namespace egress before starting bundled components: %v", err)
	}

	base := filepath.Join("..", "..", "deploy", "addons", "victoria")
	charts := filepath.Join(base, "charts")
	installs := []struct {
		release, chart, values string
		set                    []string
	}{
		{"sp02-victoria-metrics", "victoria-metrics-single-0.18.0.tgz", "metrics-values.yaml", []string{"--set", "server.persistentVolume.enabled=false"}},
		{"sp02-victoria-logs", "victoria-logs-single-0.13.9.tgz", "logs-values.yaml", []string{"--set", "server.persistentVolume.enabled=false"}},
		{"sp02-vmalert", "victoria-metrics-alert-0.18.0.tgz", "vmalert-values.yaml", []string{
			"--set-string", "server.datasource.url=http://ops-victoria-metrics." + namespace + ".svc.cluster.local:8428",
			"--set-string", "server.remote.write.url=http://ops-victoria-metrics." + namespace + ".svc.cluster.local:8428/api/v1/write",
		}},
	}
	for _, install := range installs {
		args := []string{"install", install.release, filepath.Join(charts, install.chart), "--kube-context", "orbstack", "--namespace", namespace, "--values", filepath.Join(base, install.values), "--wait", "--timeout", "5m"}
		args = append(args, install.set...)
		if output, err := runTask24Command("helm", args...); err != nil {
			t.Fatalf("install local offline chart %s: %v\n%s", install.release, err, output)
		}
	}

	for _, workload := range []struct{ service, port string }{{"ops-victoria-metrics", "8428"}, {"ops-victoria-logs", "9428"}, {"ops-vmalert", "8880"}} {
		path := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:%s/proxy/health", namespace, workload.service, workload.port)
		output, err := runTask24Command("kubectl", "--context", "orbstack", "get", "--raw="+path)
		if err != nil || strings.TrimSpace(output) != "OK" {
			t.Fatalf("%s health = %q, %v", workload.service, strings.TrimSpace(output), err)
		}
	}
	probes, err := profile.DiscoverSources(context.Background(), []profile.SourceCandidate{
		{Component: "victoriaMetrics", Endpoint: "http://ops-victoria-metrics." + namespace + ".svc.cluster.local:8428", Namespace: namespace, Name: "ops-victoria-metrics"},
		{Component: "victoriaLogs", Endpoint: "http://ops-victoria-logs." + namespace + ".svc.cluster.local:9428", Namespace: namespace, Name: "ops-victoria-logs"},
		{Component: "vmalert", Endpoint: "http://ops-vmalert." + namespace + ".svc.cluster.local:8880", Namespace: namespace, Name: "ops-vmalert"},
	}, task24ServiceProxy{namespace: namespace, kubeContext: "orbstack", services: map[string]string{
		"8428": "ops-victoria-metrics", "9428": "ops-victoria-logs", "8880": "ops-vmalert",
	}})
	if err != nil {
		t.Fatalf("probe all bundled upstream APIs: %v", err)
	}
	for component, wantVersion := range map[string]string{"victoriaMetrics": "v1.116.0", "victoriaLogs": "v1.52.0", "vmalert": "v1.116.0"} {
		if probes[component].Version != wantVersion || probes[component].LogicalID == "" {
			t.Errorf("bundled %s probe = %#v, want version %s and a logical ID", component, probes[component], wantVersion)
		}
	}
	workloads, err := runTask24Command("kubectl", "--context", "orbstack", "-n", namespace, "get", "deployments,statefulsets,daemonsets", "-o", "name")
	if err != nil {
		t.Fatal(err)
	}
	if count := len(strings.Fields(workloads)); count != 3 {
		t.Fatalf("isolated bundled workload count = %d, want exactly 3: %s", count, strings.TrimSpace(workloads))
	}
	for _, podSelector := range []string{"app.kubernetes.io/instance=sp02-victoria-metrics", "app.kubernetes.io/instance=sp02-victoria-logs", "app.kubernetes.io/instance=sp02-vmalert"} {
		output, err := runTask24Command("kubectl", "--context", "orbstack", "-n", namespace, "get", "pods", "-l", podSelector, "-o", "jsonpath={.items[*].status.containerStatuses[*].imageID}")
		if err != nil || strings.TrimSpace(output) == "" {
			t.Fatalf("%s did not reach a running state with a local image: %q, %v", podSelector, output, err)
		}
	}
}

type task24ServiceProxy struct {
	namespace   string
	kubeContext string
	services    map[string]string
}

func (proxy task24ServiceProxy) Do(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet {
		return nil, fmt.Errorf("Task 2.4 probe attempted a non-GET request")
	}
	port := request.URL.Port()
	service := proxy.services[port]
	if service == "" {
		return nil, fmt.Errorf("no isolated smoke Service for probe port %q", port)
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:%s/proxy%s", proxy.namespace, service, port, request.URL.RequestURI())
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "kubectl", "--context", proxy.kubeContext, "get", "--raw="+path)
	body, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kubectl Service proxy for %s failed: %w: %s", service, err, strings.TrimSpace(string(body)))
	}
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
}

func runTask24Command(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func runTask24WithInput(input, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func logicalSourceID(component profile.ResolvedComponent) string {
	if component.Namespace == "" || component.Name == "" {
		return ""
	}
	return component.Namespace + "/" + component.Name
}

func monitoringWorkloadCount(t *testing.T) int {
	t.Helper()
	command := exec.Command("kubectl", "--context", "orbstack", "-n", "monitoring", "get", "deployments,statefulsets,daemonsets", "-o", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl workload baseline: %v: %s", err, strings.TrimSpace(string(output)))
	}
	var result struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode workload baseline: %v", err)
	}
	return len(result.Items)
}
