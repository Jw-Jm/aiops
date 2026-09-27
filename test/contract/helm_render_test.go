package contract_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var immutableImage = regexp.MustCompile(`^[^\s@]+@sha256:[a-f0-9]{64}$`)

type renderedResource struct {
	Kind                         string `yaml:"kind"`
	AutomountServiceAccountToken *bool  `yaml:"automountServiceAccountToken"`
	Metadata                     struct {
		Name   string            `yaml:"name"`
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Type                 string `yaml:"type"`
		Replicas             int32  `yaml:"replicas"`
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
				SecurityContext struct {
					RunAsNonRoot bool `yaml:"runAsNonRoot"`
				} `yaml:"securityContext"`
				Containers []struct {
					Image string `yaml:"image"`
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
						Name string `yaml:"name"`
					} `yaml:"volumeMounts"`
				} `yaml:"containers"`
				Volumes []struct {
					Name string `yaml:"name"`
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
		})
	}
}

func TestHelmRenderKeycloakUsesSelectedPostgresEndpointAndSecret(t *testing.T) {
	cmd := exec.Command("helm", "template", "ops", filepath.Join("..", "..", "deploy", "charts", "ops-dependencies"),
		"--namespace", "default",
		"--set", "components.postgresql.mode=external",
		"--set-string", "components.postgresql.endpoint=postgresql://database.example:5432",
		"--set-string", "components.postgresql.auth.existingSecret=external-database-auth",
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
				if env.Name == "KC_DB_URL" && env.Value != "jdbc:postgresql://database.example:5432/ops" {
					t.Errorf("KC_DB_URL = %q, want selected external endpoint", env.Value)
				}
				if (env.Name == "KC_DB_USERNAME" || env.Name == "KC_DB_PASSWORD") && env.ValueFrom.SecretKeyRef.Name != "external-database-auth" {
					t.Errorf("%s Secret reference = %q, want external-database-auth", env.Name, env.ValueFrom.SecretKeyRef.Name)
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
		want = map[string]int{"Service": 4, "StatefulSet": 3, "Deployment": 1, "ConfigMap": 1}
	case "all-external":
		want = map[string]int{"ConfigMap": 4, "Secret": 4}
	case "external-postgresql-bundled-keycloak":
		want = map[string]int{"Service": 2, "StatefulSet": 1, "Deployment": 1, "ConfigMap": 3, "Secret": 2}
	case "bundled-postgresql-external-keycloak":
		want = map[string]int{"Service": 2, "StatefulSet": 2, "ConfigMap": 2, "Secret": 2}
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
