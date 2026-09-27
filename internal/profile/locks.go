package profile

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

var componentImages = map[string]string{
	"victoria-metrics": "victoriametrics/victoria-metrics",
	"victoria-logs":    "victoriametrics/victoria-logs",
	"postgresql":       "docker.io/library/postgres",
	"keycloak":         "quay.io/keycloak/keycloak",
	"seaweedfs":        "docker.io/chrislusf/seaweedfs",
	"openbao":          "ghcr.io/openbao/openbao",
	"vmalert":          "docker.io/victoriametrics/vmalert",
}

func loadComponentLocks(catalogPath, architecture string) (map[string]ComponentLock, error) {
	if catalogPath == "" {
		catalogPath = "bundle/component-catalog.yaml"
	}
	contents, err := os.ReadFile(catalogPath)
	if err != nil {
		return nil, fmt.Errorf("read Component Catalog %q: %w", catalogPath, err)
	}
	var catalog struct {
		SchemaVersion int `yaml:"schemaVersion"`
		Components    []struct {
			Name          string `yaml:"name"`
			State         string `yaml:"state"`
			Version       string `yaml:"version"`
			Digest        string `yaml:"digest"`
			Architectures any    `yaml:"architectures"`
			ChartLock     struct {
				Name    string `yaml:"name"`
				Version string `yaml:"version"`
				Digest  string `yaml:"digest"`
				Source  string `yaml:"source"`
			} `yaml:"chartLock"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(contents, &catalog); err != nil {
		return nil, fmt.Errorf("decode Component Catalog: %w", err)
	}
	if catalog.SchemaVersion != 1 {
		return nil, fmt.Errorf("Component Catalog schemaVersion must be 1")
	}
	locks := make(map[string]ComponentLock)
	for _, component := range catalog.Components {
		profileName := map[string]string{"victoria-metrics": "victoriaMetrics", "victoria-logs": "victoriaLogs"}[component.Name]
		if profileName == "" {
			profileName = component.Name
		}
		image, isImage := componentImages[component.Name]
		if !isImage {
			continue
		}
		if component.Version == "pending" || component.Digest == "pending" {
			continue
		}
		if !versionPattern.MatchString(component.Version) || !digestPattern.MatchString(component.Digest) {
			return nil, fmt.Errorf("Component Catalog lock for %q has inexact version or invalid digest", component.Name)
		}
		if !architectureIncludes(component.Architectures, "linux/"+architecture) {
			return nil, fmt.Errorf("Component Catalog lock for %q does not include linux/%s", component.Name, architecture)
		}
		locks[profileName] = ComponentLock{
			Version:   component.Version,
			Digest:    component.Digest,
			Image:     image + "@" + component.Digest,
			State:     component.State,
			ChartName: component.ChartLock.Name, ChartVersion: component.ChartLock.Version,
			ChartDigest: component.ChartLock.Digest, ChartSource: component.ChartLock.Source,
		}
	}
	return locks, nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
}

func architectureIncludes(value any, expected string) bool {
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range values {
		if text, ok := item.(string); ok && strings.EqualFold(text, expected) {
			return true
		}
	}
	return false
}
