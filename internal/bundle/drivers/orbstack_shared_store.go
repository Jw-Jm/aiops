package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"ops-platform/internal/bundle"
	"ops-platform/internal/profile"
)

type Runner func(context.Context, string, ...string) ([]byte, error)

func Run(ctx context.Context, program string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w: %.4096s", program, err, output)
	}
	return output, nil
}

type OrbStackSharedStore struct{ Run Runner }

func (d OrbStackSharedStore) execute(ctx context.Context, program string, args ...string) ([]byte, error) {
	if d.Run != nil {
		return d.Run(ctx, program, args...)
	}
	return Run(ctx, program, args...)
}

// Probe proves a running Kubernetes container is visible through the explicitly
// selected OrbStack Docker engine. Matching architecture alone is insufficient.
func (d OrbStackSharedStore) Probe(ctx context.Context, p profile.ResolvedProfile) (string, error) {
	if p.Kubernetes.Distribution != "orbstack" || p.Kubernetes.Context != "orbstack" || p.Architecture != "arm64" {
		return "", errors.New("CAPABILITY_DISABLED: this driver requires OrbStack arm64")
	}
	contextBytes, err := d.execute(ctx, "docker", "context", "inspect", "orbstack")
	if err != nil {
		return "", err
	}
	var contexts []struct {
		Name      string
		Endpoints map[string]struct{ Host string }
	}
	if json.Unmarshal(contextBytes, &contexts) != nil || len(contexts) != 1 || contexts[0].Name != "orbstack" || !strings.HasPrefix(contexts[0].Endpoints["docker"].Host, "unix://") {
		return "", errors.New("CAPABILITY_DISABLED: no explicit local OrbStack Docker endpoint")
	}
	info, err := d.execute(ctx, "docker", "--context", "orbstack", "info", "--format", "{{.OSType}}/{{.Architecture}}")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(info)) != "linux/aarch64" && strings.TrimSpace(string(info)) != "linux/arm64" {
		return "", errors.New("CAPABILITY_DISABLED: Docker engine architecture differs")
	}
	raw, err := d.execute(ctx, "kubectl", "--context", "orbstack", "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(raw)) != p.Kubernetes.ClusterUID {
		return "", errors.New("PROFILE_COMPONENT_CONFLICT: current cluster UID differs from resolved profile")
	}
	raw, err = d.execute(ctx, "kubectl", "--context", "orbstack", "get", "--raw", "/version")
	if err != nil {
		return "", err
	}
	var server struct {
		GitVersion string `json:"gitVersion"`
		Platform   string `json:"platform"`
	}
	if json.Unmarshal(raw, &server) != nil || server.GitVersion != p.Kubernetes.ServerVersion || server.Platform != "linux/arm64" {
		return "", errors.New("PROFILE_COMPONENT_CONFLICT: current Kubernetes version/platform differs")
	}
	raw, err = d.execute(ctx, "kubectl", "--context", "orbstack", "get", "pods", "--all-namespaces", "-o", "json")
	if err != nil {
		return "", err
	}
	var pods struct {
		Items []struct {
			Status struct {
				ContainerStatuses []struct {
					ContainerID string `json:"containerID"`
					State       struct {
						Running *json.RawMessage `json:"running"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &pods); err != nil {
		return "", err
	}
	for _, pod := range pods.Items {
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Running == nil || !strings.HasPrefix(status.ContainerID, "docker://") {
				continue
			}
			running, err := d.execute(ctx, "docker", "--context", "orbstack", "container", "inspect", "--format", "{{.State.Running}}", strings.TrimPrefix(status.ContainerID, "docker://"))
			if err == nil && strings.TrimSpace(string(running)) == "true" {
				return "orbstack_shared_store", nil
			}
		}
	}
	return "", errors.New("CAPABILITY_DISABLED: no running Kubernetes container was proven visible in the OrbStack Docker engine")
}

func (d OrbStackSharedStore) Import(ctx context.Context, image bundle.ImageArtifact) error {
	_, err := d.execute(ctx, "docker", "--context", "orbstack", "image", "load", "--platform", image.Architecture, "--input", image.Path)
	return err
}

func (d OrbStackSharedStore) Verify(ctx context.Context, image bundle.ImageArtifact) error {
	raw, err := d.execute(ctx, "docker", "--context", "orbstack", "image", "inspect", image.Reference)
	if err != nil {
		return err
	}
	var images []struct {
		OS           string
		Architecture string
		RepoDigests  []string
	}
	if err := json.Unmarshal(raw, &images); err != nil {
		return err
	}
	if len(images) != 1 || images[0].OS+"/"+images[0].Architecture != image.Architecture {
		return errors.New("imported image architecture differs")
	}
	for _, ref := range images[0].RepoDigests {
		if canonicalDockerReference(ref) == canonicalDockerReference(image.Reference) {
			return nil
		}
	}
	return errors.New("imported image exact repository digest was not retained; refusing online pull or tag substitution")
}

// Docker reports Docker Hub official repositories using their short name,
// even when the locked reference includes docker.io/library/. Only normalize
// those equivalent repository names; the digest remains part of the comparison.
func canonicalDockerReference(reference string) string {
	for _, prefix := range []string{"docker.io/", "index.docker.io/"} {
		reference = strings.TrimPrefix(reference, prefix)
	}
	repository, digest, ok := strings.Cut(reference, "@")
	if !ok {
		return reference
	}
	first, _, hasSlash := strings.Cut(repository, "/")
	if !hasSlash || (!strings.ContainsAny(first, ".:") && first != "localhost") {
		repository = strings.TrimPrefix(repository, "library/")
		return "docker.io/" + repository + "@" + digest
	}
	return repository + "@" + digest
}
