package contract

import (
	"bytes"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// archiveContainerSpec is the smallest rendered shape the memory budget check
// needs: the container environment, the container limits and the Secret
// references of the archive process.
type archiveContainerSpec struct {
	Name  string `yaml:"name"`
	Image string `yaml:"image"`
	Env   []struct {
		Name      string `yaml:"name"`
		Value     string `yaml:"value"`
		ValueFrom struct {
			SecretKeyRef struct {
				Name string `yaml:"name"`
				Key  string `yaml:"key"`
			} `yaml:"secretKeyRef"`
		} `yaml:"valueFrom"`
	} `yaml:"env"`
	Resources struct {
		Limits struct {
			Memory string `yaml:"memory"`
		} `yaml:"limits"`
	} `yaml:"resources"`
	SecurityContext struct {
		ReadOnlyRootFilesystem bool `yaml:"readOnlyRootFilesystem"`
	} `yaml:"securityContext"`
	VolumeMounts []struct {
		Name      string `yaml:"name"`
		MountPath string `yaml:"mountPath"`
	} `yaml:"volumeMounts"`
}

// archiveMemoryResource is the rendered archive workload.
type archiveMemoryResource struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				Containers []archiveContainerSpec `yaml:"containers"`
				Volumes    []struct {
					Name   string `yaml:"name"`
					Secret *struct {
						SecretName string `yaml:"secretName"`
					} `yaml:"secret"`
				} `yaml:"volumes"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func renderArchiveResources(t *testing.T, args ...string) []archiveMemoryResource {
	t.Helper()
	full := append([]string{"template", "archive-budget", "../../deploy/charts/ops-dependencies", "--namespace", "ops-contract"}, args...)
	output, err := exec.CommandContext(t.Context(), "helm", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("render archive chart: %v\n%s", err, output)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	resources := []archiveMemoryResource{}
	for {
		var resource archiveMemoryResource
		if err := decoder.Decode(&resource); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode rendered archive resource: %v", err)
		}
		if resource.Kind != "" {
			resources = append(resources, resource)
		}
	}
	return resources
}

func archiveContainer(t *testing.T, resources []archiveMemoryResource) (archiveMemoryResource, archiveContainerSpec) {
	t.Helper()
	for _, resource := range resources {
		if resource.Kind != "StatefulSet" || resource.Metadata.Name != "ops-seaweedfs" {
			continue
		}
		if len(resource.Spec.Template.Spec.Containers) != 1 {
			t.Fatal("unexpected archive container layout")
		}
		return resource, resource.Spec.Template.Spec.Containers[0]
	}
	t.Fatal("bundled archive StatefulSet was not rendered")
	return archiveMemoryResource{}, archiveContainerSpec{}
}

// memoryBytes converts a Kubernetes memory quantity to bytes. Only the units
// the archive chart actually uses are accepted.
func memoryBytes(t *testing.T, quantity string) int64 {
	t.Helper()
	quantity = strings.TrimSpace(quantity)
	if quantity == "" {
		t.Fatal("memory quantity is empty")
	}
	multiplier := int64(1)
	// The Go runtime accepts the binary form ("640MiB"); the Kubernetes
	// resource quantity uses the short form ("640Mi"). Both are accepted here.
	for _, unit := range []struct {
		suffix string
		factor int64
	}{
		{"GiB", 1024 * 1024 * 1024},
		{"MiB", 1024 * 1024},
		{"KiB", 1024},
		{"Gi", 1024 * 1024 * 1024},
		{"Mi", 1024 * 1024},
		{"Ki", 1024},
	} {
		if strings.HasSuffix(quantity, unit.suffix) {
			multiplier = unit.factor
			quantity = strings.TrimSuffix(quantity, unit.suffix)
			break
		}
	}
	value, err := strconv.ParseInt(quantity, 10, 64)
	if err != nil {
		t.Fatalf("memory quantity %q is not an integer", quantity)
	}
	return value * multiplier
}

// TestBundledArchiveBoundsTheGoMemoryBudget pins the archive memory budget in
// the authoritative chart. Without an explicit GOMEMLIMIT the archive process
// grows its page cache past the fixed container limit and the archive reads
// stall on constant refaults.
func TestBundledArchiveBoundsTheGoMemoryBudget(t *testing.T) {
	resources := renderArchiveResources(t)
	statefulSet, container := archiveContainer(t, resources)

	limit := container.Resources.Limits.Memory
	if limit == "" {
		t.Fatal("bundled archive container has no fixed memory limit")
	}
	budget := ""
	for _, env := range container.Env {
		if env.Name == "GOMEMLIMIT" {
			budget = env.Value
		}
	}
	if budget == "" {
		t.Fatal("bundled archive container has no GOMEMLIMIT bound")
	}
	if memoryBytes(t, budget) >= memoryBytes(t, limit) {
		t.Fatalf("GOMEMLIMIT %s must stay below the container limit %s", budget, limit)
	}

	// The memory bound must not weaken the other archive constraints.
	if !container.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatal("bundled archive container lost its read-only root filesystem")
	}
	secrets := map[string]string{}
	for _, env := range container.Env {
		if env.ValueFrom.SecretKeyRef.Name != "" {
			secrets[env.Name] = env.ValueFrom.SecretKeyRef.Name
		}
	}
	if secrets["AWS_ACCESS_KEY_ID"] == "" || secrets["AWS_SECRET_ACCESS_KEY"] == "" {
		t.Fatalf("bundled archive lost its authentication Secret reference: %v", secrets)
	}
	tlsSecret := ""
	for _, volume := range statefulSet.Spec.Template.Spec.Volumes {
		if volume.Secret != nil && volume.Secret.SecretName != "" {
			tlsSecret = volume.Secret.SecretName
		}
	}
	if tlsSecret == "" {
		t.Fatal("bundled archive lost its TLS Secret volume")
	}
	if !strings.Contains(container.Image, "@sha256:") {
		t.Fatalf("bundled archive image is not digest pinned: %s", container.Image)
	}
}

// TestExternalArchiveDoesNotDeployTheArchiveContainer keeps the external mode
// free of the archive container, so the memory budget is only ever applied to
// the process this chart owns.
func TestExternalArchiveDoesNotDeployTheArchiveContainer(t *testing.T) {
	resources := renderArchiveResources(t,
		"--set", "components.seaweedfs.mode=external",
		"--set-string", "components.seaweedfs.endpoint=https://archive.example.invalid:8333",
	)
	for _, resource := range resources {
		if resource.Kind == "StatefulSet" && resource.Metadata.Name == "ops-seaweedfs" {
			t.Fatal("external archive mode deployed the bundled archive container")
		}
	}
}
