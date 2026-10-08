package contract_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/supplychain"
)

var immutableImage = regexp.MustCompile(`^[^\s@]+@sha256:[a-f0-9]{64}$`)
var immutableTaggedDigestImage = regexp.MustCompile(`^[^\s@]+:[^\s@]+@sha256:[a-f0-9]{64}$`)

func TestCoreNetworkBootstrapContainsNoWorkloads(t *testing.T) {
	command := exec.Command("helm", "template", "ops-platform", "../../deploy/charts/ops-platform", "--namespace", "ops-system", "--set", "networkPolicyOnly=true", "--set", "workloadsEnabled=true")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("render network bootstrap: %v: %s", err, output)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	policies := map[string]bool{}
	for {
		var resource renderedResource
		if err := decoder.Decode(&resource); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if resource.Kind == "" {
			continue
		}
		if resource.Kind != "NetworkPolicy" || resource.Metadata.Labels["ops.platform.io/release"] != "ops-platform" {
			t.Fatalf("bootstrap contains a workload or unowned resource: %s/%s", resource.Kind, resource.Metadata.Name)
		}
		policies[resource.Metadata.Name] = true
	}
	if !policies["ops-default-deny"] || !policies["ops-allow-internal-platform-traffic"] {
		t.Fatal("bootstrap is missing the deny or internal-traffic policy")
	}
}

func TestWorkloadsProjectOpenBaoTokenOnlyToServiceProcesses(t *testing.T) {
	command := exec.Command("helm", "template", "ops-platform", "../../deploy/charts/ops-platform", "--namespace", "ops-system",
		"--set", "workloadsEnabled=true",
		"--set", "components.api.image=registry.example.invalid/platform/api@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--set", "components.worker.image=registry.example.invalid/platform/worker@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--set", "components.web.image=registry.example.invalid/platform/web@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--set", "runtime.oidcIssuerURL=https://keycloak.example.invalid", "--set", "runtime.profile=core")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("render service workload identities: %v: %s", err, output)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	workloads := map[string]renderedResource{}
	for {
		var resource renderedResource
		if err := decoder.Decode(&resource); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if resource.Kind == "Deployment" {
			workloads[resource.Metadata.Labels["ops.platform.io/component"]] = resource
		}
	}
	for _, name := range []string{"api", "worker"} {
		resource, ok := workloads[name]
		if !ok || resource.Spec.Template.Spec.AutomountServiceAccountToken == nil || *resource.Spec.Template.Spec.AutomountServiceAccountToken {
			t.Fatalf("%s does not disable automatic ServiceAccount token mounting", name)
		}
		if !hasProjectedOpenBaoToken(resource) || !hasReadOnlyOpenBaoTokenMount(resource) {
			t.Fatalf("%s lacks its read-only, audience-bound OpenBao token projection", name)
		}
	}
	if hasProjectedOpenBaoToken(workloads["web"]) || hasReadOnlyOpenBaoTokenMount(workloads["web"]) {
		t.Fatal("web workload received a ServiceAccount token it does not use")
	}
}

func TestMetricsScrapeUsesResolvedCRDCapability(t *testing.T) {
	for _, mode := range []struct {
		name       string
		apiVersion string
		wantKind   string
	}{
		{name: "no-crd", wantKind: ""},
		{name: "victoria-metrics-operator", apiVersion: "operator.victoriametrics.com/v1beta1/VMServiceScrape", wantKind: "VMServiceScrape"},
		{name: "prometheus-operator", apiVersion: "monitoring.coreos.com/v1/ServiceMonitor", wantKind: "ServiceMonitor"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			command := exec.Command("helm", "template", "ops-platform", "../../deploy/charts/ops-platform", "--namespace", "ops-system",
				"--set", "workloadsEnabled=true",
				"--set", "components.api.image=registry.example.invalid/platform/api@sha256:0000000000000000000000000000000000000000000000000000000000000000",
				"--set", "components.worker.image=registry.example.invalid/platform/worker@sha256:0000000000000000000000000000000000000000000000000000000000000000",
				"--set", "components.web.image=registry.example.invalid/platform/web@sha256:0000000000000000000000000000000000000000000000000000000000000000",
				"--set", "runtime.oidcIssuerURL=https://keycloak.example.invalid", "--set", "runtime.profile=core")
			if mode.apiVersion != "" {
				command.Args = append(command.Args, "--api-versions", mode.apiVersion)
			}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("render metrics scrape resources: %v: %s", err, output)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(output))
			scrapeKinds := []string{}
			apiMetricsService := false
			for {
				var resource renderedResource
				if err := decoder.Decode(&resource); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if resource.Kind == "VMServiceScrape" || resource.Kind == "ServiceMonitor" {
					scrapeKinds = append(scrapeKinds, resource.Kind)
					if resource.Spec.Endpoints == nil || len(resource.Spec.Endpoints) != 1 || resource.Spec.Endpoints[0].Port != "metrics" || resource.Spec.Endpoints[0].Path != "/metrics" {
						t.Fatalf("scrape resource has an unexpected endpoint: %#v", resource.Spec.Endpoints)
					}
				}
				if resource.Kind == "Service" && resource.Metadata.Labels["ops.platform.io/component"] == "api" {
					for _, port := range resource.Spec.Ports {
						if port.Name == "metrics" && port.Port == 9090 && port.TargetPort == "metrics" {
							apiMetricsService = true
						}
					}
				}
			}
			if !apiMetricsService {
				t.Fatal("API Service does not expose its named metrics endpoint")
			}
			want := mode.wantKind
			if want == "" && len(scrapeKinds) != 0 || want != "" && (len(scrapeKinds) != 1 || scrapeKinds[0] != want) {
				t.Fatalf("scrape kinds = %v, want %q", scrapeKinds, want)
			}
		})
	}
}

func hasProjectedOpenBaoToken(resource renderedResource) bool {
	for _, volume := range resource.Spec.Template.Spec.Volumes {
		if volume.Projected == nil {
			continue
		}
		for _, source := range volume.Projected.Sources {
			if source.ServiceAccountToken != nil && source.ServiceAccountToken.Audience == "openbao" && source.ServiceAccountToken.Path == "token" && source.ServiceAccountToken.ExpirationSeconds == 3600 {
				return true
			}
		}
	}
	return false
}

func hasReadOnlyOpenBaoTokenMount(resource renderedResource) bool {
	for _, container := range resource.Spec.Template.Spec.Containers {
		for _, mount := range container.VolumeMounts {
			if mount.Name == "openbao-identity" && mount.MountPath == "/var/run/secrets/ops-platform/openbao" && mount.ReadOnly {
				return true
			}
		}
	}
	return false
}

type renderedResource struct {
	Kind                         string `yaml:"kind"`
	AutomountServiceAccountToken *bool  `yaml:"automountServiceAccountToken"`
	Metadata                     struct {
		Name   string            `yaml:"name"`
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Type     string `yaml:"type"`
		Replicas int32  `yaml:"replicas"`
		Ports    []struct {
			Name       string `yaml:"name"`
			Port       int32  `yaml:"port"`
			TargetPort string `yaml:"targetPort"`
		} `yaml:"ports"`
		Endpoints []struct {
			Port     string `yaml:"port"`
			Path     string `yaml:"path"`
			Interval string `yaml:"interval"`
		} `yaml:"endpoints"`
		VolumeClaimTemplates []struct {
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Resources struct {
					Requests map[string]string `yaml:"requests"`
				} `yaml:"resources"`
			} `yaml:"spec"`
		} `yaml:"volumeClaimTemplates"`
		Template struct {
			Spec struct {
				AutomountServiceAccountToken *bool `yaml:"automountServiceAccountToken"`
				SecurityContext              struct {
					RunAsNonRoot bool `yaml:"runAsNonRoot"`
				} `yaml:"securityContext"`
				Containers []struct {
					Image string   `yaml:"image"`
					Args  []string `yaml:"args"`
					Env   []struct {
						Name      string `yaml:"name"`
						Value     string `yaml:"value"`
						ValueFrom struct {
							SecretKeyRef struct {
								Name string `yaml:"name"`
							} `yaml:"secretKeyRef"`
						} `yaml:"valueFrom"`
					} `yaml:"env"`
					SecurityContext struct {
						ReadOnlyRootFilesystem   bool `yaml:"readOnlyRootFilesystem"`
						AllowPrivilegeEscalation bool `yaml:"allowPrivilegeEscalation"`
					} `yaml:"securityContext"`
					VolumeMounts []struct {
						Name      string `yaml:"name"`
						MountPath string `yaml:"mountPath"`
						ReadOnly  bool   `yaml:"readOnly"`
					} `yaml:"volumeMounts"`
				} `yaml:"containers"`
				Volumes []struct {
					Name      string `yaml:"name"`
					Projected *struct {
						Sources []struct {
							ServiceAccountToken *struct {
								Path              string `yaml:"path"`
								Audience          string `yaml:"audience"`
								ExpirationSeconds int64  `yaml:"expirationSeconds"`
							} `yaml:"serviceAccountToken"`
						} `yaml:"sources"`
					} `yaml:"projected"`
				} `yaml:"volumes"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
	Data map[string]string `yaml:"data"`
}

func TestHelmRenderComponentModesAreExclusive(t *testing.T) {
	profiles := []struct {
		name  string
		modes map[string]string
	}{
		{name: "all-bundled", modes: modes("bundled", "bundled", "bundled", "bundled")},
		{name: "all-external", modes: modes("external", "external", "external", "external")},
		{name: "external-postgresql-bundled-keycloak", modes: modes("external", "bundled", "external", "bundled")},
		{name: "bundled-postgresql-external-keycloak", modes: modes("bundled", "external", "bundled", "external")},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			resources := renderDependencies(t, profile.modes)
			assertKindSnapshot(t, resources, profile.name)
			for _, component := range []string{"postgresql", "keycloak", "seaweedfs", "openbao"} {
				mode := profile.modes[component]
				workloadCount := countComponentWorkloads(resources, component)
				if mode == "bundled" && workloadCount == 0 {
					t.Errorf("bundled %s has no workload", component)
				}
				if mode == "external" && workloadCount != 0 {
					t.Errorf("external %s rendered %d workload(s)", component, workloadCount)
				}
				if mode == "external" && !hasExternalConnection(resources, component) {
					t.Errorf("external %s has no endpoint/credential-reference configuration", component)
				}
				if mode == "bundled" && hasExternalConnection(resources, component) {
					t.Errorf("bundled %s also rendered external connection configuration", component)
				}
			}
			assertImagesPinned(t, resources)
			assertWorkloadsHardened(t, resources)
			if profile.modes["seaweedfs"] == "bundled" {
				assertSeaweedDevelopmentVolumeBounds(t, resources)
			}
		})
	}
}

func TestVictoriaAddonChartsAndImagesAreDigestLocked(t *testing.T) {
	catalogBytes, err := os.ReadFile(filepath.Join("..", "..", "bundle", "component-catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := supplychain.LoadCatalogWithEvidence(bytes.NewReader(catalogBytes), os.DirFS(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	components := []struct {
		name       string
		chartFile  string
		valuesFile string
		image      string
	}{
		{"victoria-metrics", "victoria-metrics-single-0.18.0.tgz", "metrics-values.yaml", "victoriametrics/victoria-metrics:v1.116.0@sha256:b10c78f4bd9b52554b7f863ff416e480d931b1811f591049d166eea1fb247638"},
		{"victoria-logs", "victoria-logs-single-0.13.9.tgz", "logs-values.yaml", "victoriametrics/victoria-logs:v1.52.0@sha256:47b820890d64c4575a2a0a46415dcd8a4fd59a0f1fcd6a377693d7aea639442e"},
		{"vmalert", "victoria-metrics-alert-0.18.0.tgz", "vmalert-values.yaml", "victoriametrics/vmalert:v1.116.0@sha256:48e01bd36d098b9c8a1537d38235e0194013e38853196b446cb0eb1f17057311"},
	}
	for _, component := range components {
		t.Run(component.name, func(t *testing.T) {
			lock, ok := catalog.Component(component.name)
			if !ok || lock.ChartLock == nil {
				t.Fatalf("%s chart has no Component Catalog lock", component.name)
			}
			chartPath := filepath.Join("..", "..", "deploy", "addons", "victoria", "charts", component.chartFile)
			archive, err := os.ReadFile(chartPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("sha256:%x", sha256.Sum256(archive)); got != lock.ChartLock.Digest {
				t.Fatalf("chart archive digest = %s, catalog lock = %s", got, lock.ChartLock.Digest)
			}
			chartSource, err := url.Parse(lock.ChartLock.Source)
			if err != nil || filepath.Base(chartSource.Path) != component.chartFile {
				t.Fatalf("chart source %q does not identify %s", lock.ChartLock.Source, component.chartFile)
			}
			valuesPath := filepath.Join("..", "..", "deploy", "addons", "victoria", component.valuesFile)
			command := exec.Command("helm", "template", "sp02", chartPath, "--namespace", "ops-task24", "--values", valuesPath)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("render locked local chart: %v\n%s", err, output)
			}
			images := regexp.MustCompile(`(?m)^\s*image:\s*(\S+)`).FindAllSubmatch(output, -1)
			if len(images) != 1 || string(images[0][1]) != component.image {
				t.Fatalf("rendered images = %q, want exactly %q", images, component.image)
			}
			if !immutableTaggedDigestImage.MatchString(component.image) {
				t.Fatalf("catalog-locked image is not digest pinned: %s", component.image)
			}
		})
	}
}

func TestHelmRenderKeycloakUsesSelectedPostgresEndpointAndSecret(t *testing.T) {
	cmd := exec.Command("helm", "template", "ops", filepath.Join("..", "..", "deploy", "charts", "ops-dependencies"),
		"--namespace", "default",
		"--set", "components.postgresql.mode=external",
		"--set-string", "components.postgresql.endpoint=postgresql://database.example:5432",
		"--set-string", "components.postgresql.auth.existingSecret=external-database-auth",
		"--set-string", "components.keycloak.database.existingSecret=external-keycloak-auth",
		"--set", "components.keycloak.mode=bundled",
	)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template mixed PostgreSQL/Keycloak modes: %v\n%s", err, output.String())
	}
	resources := parseRenderedResources(t, output.Bytes())
	for _, resource := range resources {
		if resource.Kind != "Deployment" || resource.Metadata.Name != "ops-keycloak" {
			continue
		}
		for _, container := range resource.Spec.Template.Spec.Containers {
			for _, env := range container.Env {
				if env.Name == "KC_DB_URL" && env.Value != "jdbc:postgresql://database.example:5432/keycloak" {
					t.Errorf("KC_DB_URL = %q, want selected external endpoint", env.Value)
				}
				if (env.Name == "KC_DB_USERNAME" || env.Name == "KC_DB_PASSWORD") && env.ValueFrom.SecretKeyRef.Name != "external-keycloak-auth" {
					t.Errorf("%s Secret reference = %q, want dedicated external-keycloak-auth", env.Name, env.ValueFrom.SecretKeyRef.Name)
				}
			}
		}
		return
	}
	t.Fatal("bundled Keycloak Deployment not rendered")
}

func TestHelmRenderNeverPublishesCredentialValues(t *testing.T) {
	const passwordCanary = "never-render-this-password-7f2e"
	cmd := exec.Command("helm", "template", "ops", filepath.Join("..", "..", "deploy", "charts", "ops-dependencies"),
		"--namespace", "default",
		"--set-string", "components.postgresql.auth.password="+passwordCanary,
		"--set-string", "components.keycloak.auth.password="+passwordCanary,
	)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template with credential canary: %v\n%s", err, output.String())
	}
	if strings.Contains(output.String(), passwordCanary) {
		t.Fatal("render output contains a credential value")
	}
	resources := parseRenderedResources(t, output.Bytes())
	openbaoConfig := false
	for _, resource := range resources {
		if resource.Kind == "ConfigMap" {
			for key, value := range resource.Data {
				if strings.Contains(value, passwordCanary) {
					t.Errorf("ConfigMap %s key %s contains a credential value", resource.Metadata.Name, key)
				}
			}
			if resource.Metadata.Name == "ops-openbao-config" {
				config := resource.Data["config.hcl"]
				if !strings.Contains(config, "disable_mlock = true") || !strings.Contains(config, `storage "raft"`) || strings.Contains(config, "dev_mode") {
					t.Errorf("OpenBao config must use persistent Raft storage, disable mlock explicitly, and avoid dev mode")
				}
				openbaoConfig = true
			}
		}
	}
	if !openbaoConfig {
		t.Fatal("bundled OpenBao config is not rendered")
	}
}

func TestHelmRenderOpenBaoTokenReviewIdentity(t *testing.T) {
	cmd := exec.Command("helm", "template", "bao-contract", filepath.Join("..", "..", "deploy", "charts", "ops-dependencies"), "--namespace", "ops-contract")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("render OpenBao identity: %v", err)
	}
	type identityResource struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		AutomountServiceAccountToken *bool `yaml:"automountServiceAccountToken"`
		RoleRef                      struct {
			Kind string `yaml:"kind"`
			Name string `yaml:"name"`
		} `yaml:"roleRef"`
		Subjects []struct {
			Kind      string `yaml:"kind"`
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"subjects"`
		Spec struct {
			Template struct {
				Spec struct {
					ServiceAccountName           string `yaml:"serviceAccountName"`
					AutomountServiceAccountToken *bool  `yaml:"automountServiceAccountToken"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	serviceAccount, binding, statefulSet := false, false, false
	for {
		var resource identityResource
		if err := decoder.Decode(&resource); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode rendered OpenBao resource: %v", err)
		}
		switch resource.Kind {
		case "ServiceAccount":
			if resource.Metadata.Name == "ops-openbao-tokenreview" {
				serviceAccount = resource.AutomountServiceAccountToken != nil && *resource.AutomountServiceAccountToken
			}
		case "ClusterRoleBinding":
			if resource.Metadata.Name == "ops-contract-bao-contract-openbao-tokenreview" {
				binding = resource.RoleRef.Kind == "ClusterRole" && resource.RoleRef.Name == "system:auth-delegator" && len(resource.Subjects) == 1 && resource.Subjects[0].Kind == "ServiceAccount" && resource.Subjects[0].Name == "ops-openbao-tokenreview" && resource.Subjects[0].Namespace == "ops-contract"
			}
		case "StatefulSet":
			if resource.Metadata.Name == "ops-openbao" {
				statefulSet = resource.Spec.Template.Spec.ServiceAccountName == "ops-openbao-tokenreview" && resource.Spec.Template.Spec.AutomountServiceAccountToken != nil && *resource.Spec.Template.Spec.AutomountServiceAccountToken
			}
		}
	}
	if !serviceAccount || !binding || !statefulSet {
		t.Fatalf("OpenBao TokenReview identity incomplete: ServiceAccount=%v ClusterRoleBinding=%v StatefulSet=%v", serviceAccount, binding, statefulSet)
	}
}

func TestHelmRenderRejectsDetectionTemplatesAsInstallInput(t *testing.T) {
	cmd := exec.Command("helm", "template", "ops", filepath.Join("..", "..", "deploy", "charts", "ops-dependencies"),
		"--namespace", "default", "--set", "components.postgresql.mode=detect")
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("unresolved detect mode rendered successfully:\n%s", output)
	}
}

func TestHelmRenderPlatformSecuritySkeleton(t *testing.T) {
	cmd := exec.Command("helm", "template", "ops", filepath.Join("..", "..", "deploy", "charts", "ops-platform"), "--namespace", "default")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template platform chart: %v\n%s", err, output.String())
	}
	resources := parseRenderedResources(t, output.Bytes())
	for _, component := range []string{"api", "worker", "web", "investigator", "command-runner"} {
		if !hasKindComponent(resources, "ServiceAccount", component) {
			t.Errorf("platform chart has no %s ServiceAccount", component)
		}
		for _, resource := range resources {
			if resource.Kind == "ServiceAccount" && resource.Metadata.Labels["ops.platform.io/component"] == component && (resource.AutomountServiceAccountToken == nil || *resource.AutomountServiceAccountToken) {
				t.Errorf("%s ServiceAccount must disable automatic token mounting", component)
			}
		}
		if !hasKindComponent(resources, "Service", component) {
			t.Errorf("platform chart has no %s ClusterIP Service", component)
		}
		if !hasKindComponent(resources, "NetworkPolicy", component) && !hasKindComponent(resources, "NetworkPolicy", "default-deny") {
			t.Errorf("platform chart has no NetworkPolicy governing %s", component)
		}
		for _, resource := range resources {
			if resource.Kind == "Service" && resource.Metadata.Labels["ops.platform.io/component"] == component && resource.Spec.Type != "ClusterIP" {
				t.Errorf("%s Service type = %q, want ClusterIP", component, resource.Spec.Type)
			}
		}
	}
	assertDockerfilesPinnedAndNonRoot(t)
}

// The same namespace can contain a protected external OpenBao release. A
// component label alone must never make that existing workload a policy target.
func TestHelmNetworkPoliciesSelectOnlyManagedReleases(t *testing.T) {
	cmd := exec.Command("helm", "template", "ops-platform", filepath.Join("..", "..", "deploy", "charts", "ops-platform"), "--namespace", "ops-system")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	policies := 0
	for {
		var resource struct {
			Kind string `yaml:"kind"`
			Spec struct {
				PodSelector struct {
					MatchExpressions []struct {
						Key      string   `yaml:"key"`
						Operator string   `yaml:"operator"`
						Values   []string `yaml:"values"`
					} `yaml:"matchExpressions"`
				} `yaml:"podSelector"`
			} `yaml:"spec"`
		}
		if err := decoder.Decode(&resource); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if resource.Kind != "NetworkPolicy" {
			continue
		}
		policies++
		for _, release := range []string{"ops-platform", "ops-dependencies", "ops-core", "", "someone-else"} {
			selected := true
			for _, expression := range resource.Spec.PodSelector.MatchExpressions {
				value := map[string]string{"ops.platform.io/component": "openbao", "ops.platform.io/release": release}[expression.Key]
				if expression.Operator != "In" {
					t.Fatalf("unexpected selector operator %q", expression.Operator)
				}
				found := false
				for _, allowed := range expression.Values {
					found = found || allowed == value
				}
				selected = selected && found
			}
			want := release == "ops-platform" || release == "ops-dependencies"
			if selected != want {
				t.Errorf("NetworkPolicy selects release %q = %v, want %v", release, selected, want)
			}
		}
	}
	if policies != 2 {
		t.Fatalf("rendered %d policies, want deny and allow", policies)
	}
}

func renderDependencies(t *testing.T, selectedModes map[string]string) []renderedResource {
	t.Helper()
	args := []string{"template", "ops", filepath.Join("..", "..", "deploy", "charts", "ops-dependencies"), "--namespace", "default"}
	for component, mode := range selectedModes {
		args = append(args, "--set", "components."+component+".mode="+mode)
		if mode == "external" {
			args = append(args, "--set", "components."+component+".auth.existingSecret=external-"+component+"-credentials")
		}
	}
	cmd := exec.Command("helm", args...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm %v: %v\n%s", args, err, output.String())
	}
	return parseRenderedResources(t, output.Bytes())
}

func parseRenderedResources(t *testing.T, output []byte) []renderedResource {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	var resources []renderedResource
	for {
		var resource renderedResource
		if err := decoder.Decode(&resource); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered Kubernetes YAML: %v", err)
		}
		if resource.Kind != "" {
			resources = append(resources, resource)
		}
	}
	return resources
}

func assertImagesPinned(t *testing.T, resources []renderedResource) {
	t.Helper()
	for _, resource := range resources {
		for _, container := range resource.Spec.Template.Spec.Containers {
			if !immutableImage.MatchString(container.Image) {
				t.Errorf("%s/%s image is not immutable name@sha256:digest: %q", resource.Kind, resource.Metadata.Name, container.Image)
			}
		}
	}
}

func assertWorkloadsHardened(t *testing.T, resources []renderedResource) {
	t.Helper()
	for _, resource := range resources {
		if resource.Kind != "StatefulSet" && resource.Kind != "Deployment" && resource.Kind != "DaemonSet" && resource.Kind != "Job" {
			continue
		}
		if resource.Spec.Replicas != 1 {
			t.Errorf("%s/%s replicas = %d, want one development replica", resource.Kind, resource.Metadata.Name, resource.Spec.Replicas)
		}
		if !resource.Spec.Template.Spec.SecurityContext.RunAsNonRoot {
			t.Errorf("%s/%s pod does not require a non-root user", resource.Kind, resource.Metadata.Name)
		}
		volumeNames := map[string]bool{}
		for _, volume := range resource.Spec.Template.Spec.Volumes {
			volumeNames[volume.Name] = true
		}
		for _, claim := range resource.Spec.VolumeClaimTemplates {
			volumeNames[claim.Metadata.Name] = true
			if claim.Spec.Resources.Requests["storage"] != "2Gi" {
				t.Errorf("%s/%s PVC request = %q, want 2Gi development storage", resource.Kind, resource.Metadata.Name, claim.Spec.Resources.Requests["storage"])
			}
		}
		for _, container := range resource.Spec.Template.Spec.Containers {
			if !container.SecurityContext.ReadOnlyRootFilesystem {
				t.Errorf("%s/%s container root filesystem is writable", resource.Kind, resource.Metadata.Name)
			}
			if container.SecurityContext.AllowPrivilegeEscalation {
				t.Errorf("%s/%s container allows privilege escalation", resource.Kind, resource.Metadata.Name)
			}
			for _, mount := range container.VolumeMounts {
				if !volumeNames[mount.Name] {
					t.Errorf("%s/%s mounts undeclared volume %q", resource.Kind, resource.Metadata.Name, mount.Name)
				}
			}
		}
	}
}

func countComponentWorkloads(resources []renderedResource, component string) int {
	count := 0
	for _, resource := range resources {
		if (resource.Kind == "StatefulSet" || resource.Kind == "Deployment" || resource.Kind == "DaemonSet" || resource.Kind == "Job" || resource.Kind == "Pod") && resource.Metadata.Labels["ops.platform.io/component"] == component {
			count++
		}
	}
	return count
}

func hasExternalConnection(resources []renderedResource, component string) bool {
	for _, resource := range resources {
		if resource.Metadata.Labels["ops.platform.io/component"] == component &&
			(resource.Kind == "ConfigMap" || resource.Kind == "Secret") &&
			resource.Metadata.Labels["ops.platform.io/mode"] == "external" {
			return true
		}
	}
	return false
}

func hasKindComponent(resources []renderedResource, kind, component string) bool {
	for _, resource := range resources {
		if resource.Kind == kind && (resource.Metadata.Labels["ops.platform.io/component"] == component || resource.Metadata.Labels["ops.platform.io/policy"] == component) {
			return true
		}
	}
	return false
}

func modes(postgres, keycloak, seaweedfs, openbao string) map[string]string {
	return map[string]string{
		"postgresql": postgres,
		"keycloak":   keycloak,
		"seaweedfs":  seaweedfs,
		"openbao":    openbao,
	}
}

func assertKindSnapshot(t *testing.T, resources []renderedResource, profile string) {
	t.Helper()
	want := map[string]int{}
	switch profile {
	case "all-bundled":
		want = map[string]int{"Service": 4, "StatefulSet": 3, "Deployment": 1, "ConfigMap": 3, "ServiceAccount": 1, "ClusterRoleBinding": 1}
	case "all-external":
		want = map[string]int{"ConfigMap": 4, "Secret": 4}
	case "external-postgresql-bundled-keycloak":
		want = map[string]int{"Service": 2, "StatefulSet": 1, "Deployment": 1, "ConfigMap": 3, "Secret": 2, "ServiceAccount": 1, "ClusterRoleBinding": 1}
	case "bundled-postgresql-external-keycloak":
		want = map[string]int{"Service": 2, "StatefulSet": 2, "ConfigMap": 3, "Secret": 2}
	default:
		t.Fatalf("no resource snapshot is defined for %q", profile)
	}
	got := map[string]int{}
	for _, resource := range resources {
		got[resource.Kind]++
	}
	if len(got) != len(want) {
		t.Fatalf("rendered resource kinds = %v, want snapshot %v", got, want)
	}
	for kind, count := range want {
		if got[kind] != count {
			t.Errorf("rendered %s count = %d, want snapshot %d", kind, got[kind], count)
		}
	}
}

func assertSeaweedDevelopmentVolumeBounds(t *testing.T, resources []renderedResource) {
	t.Helper()
	foundConfig, foundStatefulSet := false, false
	for _, resource := range resources {
		if resource.Kind == "ConfigMap" && resource.Metadata.Name == "ops-seaweedfs-master-config" {
			foundConfig = true
			if resource.Data["master.toml"] != "[master.volume_growth]\ncopy_1 = 1\n" {
				t.Errorf("SeaweedFS development volume growth config = %q", resource.Data["master.toml"])
			}
		}
		if resource.Kind != "StatefulSet" || resource.Metadata.Name != "ops-seaweedfs" {
			continue
		}
		foundStatefulSet = true
		if len(resource.Spec.Template.Spec.Containers) != 1 {
			t.Errorf("SeaweedFS containers = %d, want one", len(resource.Spec.Template.Spec.Containers))
			continue
		}
		args := strings.Join(resource.Spec.Template.Spec.Containers[0].Args, " ")
		for _, required := range []string{"-config_dir=/etc/seaweedfs", "-volume.max=12", "-master.volumeSizeLimitMB=128", "-master.telemetry=false"} {
			if !strings.Contains(args, required) {
				t.Errorf("SeaweedFS args %q do not contain %q", args, required)
			}
		}
	}
	if !foundConfig || !foundStatefulSet {
		t.Errorf("SeaweedFS config/statefulset rendered: config=%v statefulset=%v", foundConfig, foundStatefulSet)
	}
}

func assertDockerfilesPinnedAndNonRoot(t *testing.T) {
	t.Helper()
	for _, name := range []string{"api", "worker", "web", "investigator", "command-runner"} {
		path := filepath.Join("..", "..", "build", "images", name, "Dockerfile")
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s Dockerfile: %v", name, err)
			continue
		}
		for _, line := range strings.Split(string(contents), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "FROM ") {
				fields := strings.Fields(line)
				if len(fields) < 2 || !immutableImage.MatchString(fields[1]) {
					t.Errorf("%s base image is not immutable name@sha256:digest: %q", name, line)
				}
			}
		}
		if !regexp.MustCompile(`(?m)^USER (1000:1000|10001:10001|65532:65532)$`).Match(contents) {
			t.Errorf("%s Dockerfile does not set a non-root numeric user", name)
		}
	}
}
