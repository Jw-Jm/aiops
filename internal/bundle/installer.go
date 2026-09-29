package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/profile"
)

type CommandRunner func(context.Context, string, ...string) ([]byte, error)

// Install only executes local verified Charts. It never updates repositories,
// downloads dependencies, pulls images, or automatically deletes resources.
func Install(ctx context.Context, manifest Manifest, trust TrustRoot, p profile.ResolvedProfile, runtime RuntimeImporter, run CommandRunner) (ImportReport, error) {
	v, err := prepare(ctx, manifest, trust)
	if err != nil {
		return ImportReport{}, fmt.Errorf("checkpoint=verify-bundle: %w", err)
	}
	defer v.close()
	images, err := planImages(v, p)
	if err != nil {
		return ImportReport{}, fmt.Errorf("checkpoint=validate-materials: %w", err)
	}
	if run == nil {
		return ImportReport{}, errors.New("command runner is required")
	}
	plans, err := planCharts(ctx, v, p, images, run)
	if err != nil {
		return ImportReport{}, fmt.Errorf("checkpoint=plan-charts: %w", err)
	}
	report, err := importImages(ctx, v.manifest.BundleID, p, images, runtime)
	if err != nil {
		return report, err
	}
	// Install the same release's policies before any dependency starts. The
	// final platform upgrade uses the same authenticated Chart and values;
	// only the policy-only switch changes. No foreign resource is adopted.
	platform := plans[len(plans)-1]
	if _, err := run(ctx, "helm", "upgrade", "--install", platform.release, platform.chart,
		"--kube-context", p.Kubernetes.Context, "--namespace", "ops-system",
		"--values", platform.values, "--set", "networkPolicyOnly=true",
		"--wait", "--timeout", "5m"); err != nil {
		return report, fmt.Errorf("checkpoint=install-network-policy release=%s: %w", platform.release, err)
	}
	if err := verifyBootstrapEgress(ctx, p, images, run); err != nil {
		return report, fmt.Errorf("checkpoint=network-policy-enforcement: %w", err)
	}
	for _, plan := range plans {
		args := []string{"upgrade", "--install", plan.release, plan.chart,
			"--kube-context", p.Kubernetes.Context, "--namespace", "ops-system",
			"--values", plan.values, "--wait", "--timeout", "5m"}
		if plan.release == "ops-platform" {
			args = append(args, "--set", "networkPolicyOnly=false")
		}
		if len(plan.rendererArgs) > 0 {
			executable, err := os.Executable()
			if err != nil {
				return report, fmt.Errorf("checkpoint=post-renderer: %w", err)
			}
			args = append(args, "--post-renderer", executable)
			for _, arg := range plan.rendererArgs {
				args = append(args, "--post-renderer-args", arg)
			}
		}
		if _, err := run(ctx, "helm", args...); err != nil {
			return report, fmt.Errorf("checkpoint=install-chart release=%s: %w", plan.release, err)
		}
	}
	// Helm waits for readiness, and the core bootstrap still requires OpenBao to
	// be initialized/unsealed. Do not treat the TLS readiness override as health.
	if _, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", "ops-system",
		"rollout", "status", "deployment/ops-api", "--timeout=90s"); err != nil {
		return report, fmt.Errorf("checkpoint=health: %w", err)
	}
	if _, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", "ops-system",
		"rollout", "status", "deployment/ops-worker", "--timeout=90s"); err != nil {
		return report, fmt.Errorf("checkpoint=health: %w", err)
	}
	return report, nil
}

type chartPlan struct {
	chart, release, values string
	rendererArgs           []string
}

var releaseNamePattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)

var offlineResourceKinds = map[string]bool{
	"Service": true, "ServiceAccount": true, "ConfigMap": true, "Secret": true,
	"Deployment": true, "StatefulSet": true, "NetworkPolicy": true, "ClusterRoleBinding": true,
}

// CleanupRelease removes a Helm release only after its recorded manifest and
// every still-present resource prove the expected release label. PVCs are
// deliberately outside the Helm resource cleanup path and are never deleted.
func CleanupRelease(ctx context.Context, p profile.ResolvedProfile, release string, run CommandRunner) error {
	if run == nil || p.Kubernetes.Context == "" || !releaseNamePattern.MatchString(release) {
		return errors.New("cleanup requires a Kubernetes context, release name, and command runner")
	}
	list, err := run(ctx, "helm", "list", "--kube-context", p.Kubernetes.Context, "--namespace", "ops-system", "--all", "--filter", "^"+release+"$", "--output", "json")
	if err != nil {
		return fmt.Errorf("checkpoint=cleanup-inspect-release release=%s: %w", release, err)
	}
	var releases []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(list, &releases); err != nil {
		return fmt.Errorf("decode Helm release inventory: %w", err)
	}
	found := false
	for _, item := range releases {
		found = found || item.Name == release
	}
	if !found {
		return nil
	}
	manifest, err := run(ctx, "helm", "get", "manifest", release, "--kube-context", p.Kubernetes.Context, "--namespace", "ops-system")
	if err != nil {
		return fmt.Errorf("checkpoint=cleanup-read-release release=%s: %w", release, err)
	}
	if err := validateReleaseOwnership(ctx, manifest, release, p, run); err != nil {
		return fmt.Errorf("checkpoint=cleanup-ownership release=%s: %w", release, err)
	}
	if _, err := run(ctx, "helm", "uninstall", release, "--kube-context", p.Kubernetes.Context,
		"--namespace", "ops-system", "--wait", "--timeout", "5m"); err != nil {
		return fmt.Errorf("checkpoint=cleanup-release release=%s: %w", release, err)
	}
	return nil
}

func validateReleaseOwnership(ctx context.Context, manifest []byte, release string, p profile.ResolvedProfile, run CommandRunner) error {
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	seen := 0
	for {
		var object map[string]any
		err := decoder.Decode(&object)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if len(object) == 0 {
			continue
		}
		seen++
		kind, _ := object["kind"].(string)
		metadata, _ := object["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		labels, _ := metadata["labels"].(map[string]any)
		if name == "" || labels["ops.platform.io/release"] != release {
			return fmt.Errorf("release manifest resource %s/%s lacks the expected release label", kind, name)
		}
		if !offlineResourceKinds[kind] {
			return fmt.Errorf("cleanup refuses resource kind %s", kind)
		}
		annotations, _ := metadata["annotations"].(map[string]any)
		if annotations["helm.sh/hook"] != nil {
			return errors.New("cleanup refuses Helm hooks")
		}
		if kind == "PersistentVolumeClaim" || kind == "PersistentVolume" {
			return fmt.Errorf("cleanup refuses to delete storage resource %s/%s", kind, name)
		}
		if namespace, _ := metadata["namespace"].(string); namespace != "" && namespace != "ops-system" {
			return fmt.Errorf("release resource %s/%s targets unexpected namespace %s", kind, name, namespace)
		}
		args := []string{"--context", p.Kubernetes.Context, "get", kind, name, "--ignore-not-found", "-o", "json"}
		if kind != "ClusterRoleBinding" {
			args = append(args, "-n", "ops-system")
		}
		current, err := run(ctx, "kubectl", args...)
		if err != nil {
			return fmt.Errorf("inspect %s/%s: %w", kind, name, err)
		}
		if len(bytes.TrimSpace(current)) == 0 {
			continue
		}
		var live map[string]any
		if err := json.Unmarshal(current, &live); err != nil {
			return fmt.Errorf("decode live %s/%s: %w", kind, name, err)
		}
		liveMetadata, _ := live["metadata"].(map[string]any)
		liveLabels, _ := liveMetadata["labels"].(map[string]any)
		if liveLabels["ops.platform.io/release"] != release {
			return fmt.Errorf("live resource %s/%s no longer carries the expected release label", kind, name)
		}
	}
	if seen == 0 {
		return errors.New("release manifest is empty")
	}
	return nil
}

func planCharts(ctx context.Context, v *verifiedPayload, p profile.ResolvedProfile, images []ImageArtifact, run CommandRunner) ([]chartPlan, error) {
	byName := map[string]Material{}
	imageMaterials := map[string]bool{}
	for _, m := range v.manifest.Materials {
		if m.Kind == "container-image" {
			imageMaterials[m.Name] = true
		}
		if m.Kind == "chart" {
			byName[m.Name] = m
		}
	}
	for _, name := range []string{"postgresql", "keycloak", "seaweedfs", "openbao", "victoria-metrics", "victoria-logs", "vmalert"} {
		if !imageMaterials[name] {
			return nil, fmt.Errorf("missing core or standard fallback OCI material %s", name)
		}
	}
	for _, name := range []string{"victoria-metrics-chart", "victoria-logs-chart", "vmalert-chart"} {
		if _, ok := byName[name]; !ok {
			return nil, fmt.Errorf("missing standard fallback chart material %s", name)
		}
	}
	names := []string{"ops-dependencies-chart"}
	for _, entry := range []struct{ profileName, materialName string }{{"victoriaMetrics", "victoria-metrics-chart"}, {"victoriaLogs", "victoria-logs-chart"}} {
		if p.Components[entry.profileName].Mode == "bundled" {
			names = append(names, entry.materialName)
		}
	}
	if p.Components["vmalert"].Mode == "bundled" {
		names = append(names, "vmalert-chart")
	}
	names = append(names, "ops-platform-chart")
	for _, name := range names {
		if _, ok := byName[name]; !ok {
			return nil, fmt.Errorf("missing local chart material %s", name)
		}
	}
	allowed := map[string]bool{}
	imageNames := map[string]string{}
	for _, image := range images {
		allowed[image.Reference] = true
		imageNames[image.Name] = image.Reference
	}
	for _, name := range []string{"platform-api", "platform-worker"} {
		if imageNames[name] == "" {
			return nil, fmt.Errorf("missing platform image %s", name)
		}
	}
	// A namespace is provided by the operator; do not adopt or recreate an
	// existing namespace and its PVCs during a smoke test.
	if _, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "namespace", "ops-system"); err != nil {
		return nil, err
	}
	plans := []chartPlan{}
	for _, name := range names {
		material := byName[name]
		chart := filepath.Join(v.path, filepath.FromSlash(material.PayloadRef))
		values := map[string]any{}
		var rendererComponent string
		var rendererImage, rendererVersion string
		release := strings.TrimSuffix(name, "-chart")
		if err := requireAbsentRelease(ctx, p, release, run); err != nil {
			return nil, err
		}
		switch name {
		case "victoria-metrics-chart", "victoria-logs-chart":
			componentName := "victoriaMetrics"
			if name == "victoria-logs-chart" {
				componentName = "victoriaLogs"
			}
			component := p.Components[componentName]
			rendererComponent = strings.TrimSuffix(name, "-chart")
			rendererImage, rendererVersion = component.Image, component.Version
			chartValues, err := victoriaChartValues(rendererComponent, component, p)
			if err != nil {
				return nil, err
			}
			values = chartValues
		case "ops-dependencies-chart":
			components := map[string]any{}
			for _, componentName := range []string{"postgresql", "keycloak", "seaweedfs", "openbao"} {
				component, ok := p.Components[componentName]
				if !ok || (component.Mode != "bundled" && component.Mode != "external") {
					return nil, fmt.Errorf("required core component %s is missing", componentName)
				}
				components[componentName] = map[string]any{"mode": component.Mode, "endpoint": component.Endpoint, "image": component.Image}
			}
			values["components"] = components
			values["global"] = map[string]any{"imagePullPolicy": "IfNotPresent"}
		case "ops-platform-chart":
			values["workloadsEnabled"] = true
			managed := []string{"ops-dependencies"}
			for _, entry := range []struct{ name, release string }{{"victoriaMetrics", "victoria-metrics"}, {"victoriaLogs", "victoria-logs"}, {"vmalert", "vmalert"}} {
				if p.Components[entry.name].Mode == "bundled" {
					managed = append(managed, entry.release)
				}
			}
			externalServices, err := externalServiceEgress(ctx, p, run)
			if err != nil {
				return nil, err
			}
			values["networkPolicy"] = map[string]any{"managedDependencyReleases": managed, "externalServices": externalServices}
			profileYAML, err := yaml.Marshal(p)
			if err != nil {
				return nil, err
			}
			values["runtime"] = map[string]any{"profile": string(profileYAML), "oidcIssuerURL": strings.TrimSuffix(p.Components["keycloak"].Endpoint, "/") + "/realms/ops"}
			values["components"] = map[string]any{
				"api":            map[string]any{"image": imageNames["platform-api"]},
				"worker":         map[string]any{"image": imageNames["platform-worker"]},
				"web":            map[string]any{"enabled": false},
				"investigator":   map[string]any{"enabled": false},
				"command-runner": map[string]any{"enabled": false},
			}
		case "vmalert-chart":
			component := p.Components["vmalert"]
			rendererComponent, rendererImage, rendererVersion = "vmalert", component.Image, component.Version
			chartValues, err := victoriaChartValues(rendererComponent, component, p)
			if err != nil {
				return nil, err
			}
			values = chartValues
		}
		encoded, err := yaml.Marshal(values)
		if err != nil {
			return nil, err
		}
		file := filepath.Join(v.root, name+"-values.yaml")
		if err := os.WriteFile(file, encoded, 0600); err != nil {
			return nil, err
		}
		rendered, err := run(ctx, "helm", "template", release, chart, "--namespace", "ops-system", "--values", file, "--include-crds")
		if err != nil {
			return nil, err
		}
		var rendererArgs []string
		if rendererComponent != "" {
			rendered, err = RenderOwnedChart(bytes.NewReader(rendered), release, rendererComponent, rendererImage, rendererVersion)
			if err != nil {
				return nil, err
			}
			rendererArgs = []string{"helm-render-owned", "--release", release, "--component", rendererComponent, "--image", rendererImage, "--version", rendererVersion}
		}
		if err := validateRenderedChart(ctx, rendered, allowed, p, release, run); err != nil {
			return nil, err
		}
		if name == "ops-platform-chart" {
			bootstrap, err := run(ctx, "helm", "template", release, chart, "--namespace", "ops-system", "--values", file, "--set", "networkPolicyOnly=true")
			if err != nil {
				return nil, err
			}
			if err := validatePolicyBootstrap(bootstrap); err != nil {
				return nil, err
			}
			if err := validateRenderedChart(ctx, bootstrap, allowed, p, release, run); err != nil {
				return nil, err
			}
		}
		plans = append(plans, chartPlan{chart: chart, release: release, values: file, rendererArgs: rendererArgs})
	}
	return plans, nil
}

func validatePolicyBootstrap(rendered []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(rendered))
	count := 0
	for {
		var resource struct {
			Kind string `yaml:"kind"`
		}
		if err := decoder.Decode(&resource); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if resource.Kind == "" {
			continue
		}
		if resource.Kind != "NetworkPolicy" {
			return errors.New("policy bootstrap must contain only NetworkPolicy resources")
		}
		count++
	}
	if count == 0 {
		return errors.New("policy bootstrap has no NetworkPolicy resources")
	}
	return nil
}

func victoriaChartValues(name string, component profile.ResolvedComponent, p profile.ResolvedProfile) (map[string]any, error) {
	tagged, err := taggedDigest(component.Image, component.Version)
	if err != nil {
		return nil, err
	}
	separator := strings.LastIndex(tagged[:strings.Index(tagged, "@")], ":")
	server := map[string]any{
		"fullnameOverride": "ops-" + name, "replicaCount": 1,
		"image":              map[string]any{"repository": tagged[:separator], "tag": tagged[separator+1:], "pullPolicy": "IfNotPresent"},
		"resources":          map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "128Mi"}, "limits": map[string]any{"cpu": "1", "memory": "512Mi"}},
		"securityContext":    map[string]any{"enabled": true, "runAsNonRoot": true, "runAsUser": 65534, "readOnlyRootFilesystem": true, "allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []string{"ALL"}}},
		"podSecurityContext": map[string]any{"enabled": true, "runAsNonRoot": true, "runAsUser": 65534, "fsGroup": 65534},
	}
	values := map[string]any{"server": server, "serviceAccount": map[string]any{"create": false}, "alertmanager": map[string]any{"enabled": false}, "vector": map[string]any{"enabled": false}}
	if name == "vmalert" {
		endpoint := strings.TrimSuffix(p.Components["victoriaMetrics"].Endpoint, "/")
		server["config"] = map[string]any{"alerts": map[string]any{"groups": []any{}}}
		server["datasource"] = map[string]any{"url": endpoint}
		server["remote"] = map[string]any{"read": map[string]any{"url": endpoint}, "write": map[string]any{"url": endpoint + "/api/v1/write"}}
	} else {
		server["mode"] = "statefulSet"
		server["persistentVolume"] = map[string]any{"enabled": true, "size": "2Gi"}
		server["serviceMonitor"] = map[string]any{"enabled": false}
		server["scrape"] = map[string]any{"enabled": false}
		if name == "victoria-metrics" {
			server["retentionPeriod"] = 1
		}
	}
	return values, nil
}

func validateRenderedChart(ctx context.Context, rendered []byte, allowed map[string]bool, p profile.ResolvedProfile, release string, run CommandRunner) error {
	decoder := yaml.NewDecoder(bytes.NewReader(rendered))
	for {
		var object map[string]any
		err := decoder.Decode(&object)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(object) == 0 {
			continue
		}
		if err := checkRenderedImages(object, allowed); err != nil {
			return err
		}
		kind, _ := object["kind"].(string)
		metadata, _ := object["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		annotations, _ := metadata["annotations"].(map[string]any)
		labels, _ := metadata["labels"].(map[string]any)
		if release == "" || labels["ops.platform.io/release"] != release {
			return fmt.Errorf("chart resource %s/%s lacks the expected release label", kind, name)
		}
		if namespace, _ := metadata["namespace"].(string); namespace != "" && namespace != "ops-system" {
			return fmt.Errorf("offline core resource %s/%s targets unexpected namespace %s", kind, name, namespace)
		}
		if annotations["helm.sh/hook"] != nil {
			return errors.New("offline core charts must not execute unverified Helm hooks")
		}
		if name == "" || strings.ContainsAny(name, "/ \\") {
			return errors.New("chart resource has invalid metadata.name")
		}
		if !offlineResourceKinds[kind] {
			return fmt.Errorf("unsupported offline core resource kind %s", kind)
		}
		if err := checkSecretReferences(ctx, object, p, run); err != nil {
			return err
		}
		args := []string{"--context", p.Kubernetes.Context, "get", kind, name, "--ignore-not-found", "-o", "json"}
		if kind != "ClusterRoleBinding" {
			args = append(args, "-n", "ops-system")
		}
		existing, err := run(ctx, "kubectl", args...)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(existing)) != 0 {
			return fmt.Errorf("PROFILE_COMPONENT_CONFLICT: resource %s/%s already exists; fresh install refuses adoption or overwrite", kind, name)
		}
	}
}

func requireAbsentRelease(ctx context.Context, p profile.ResolvedProfile, release string, run CommandRunner) error {
	if !releaseNamePattern.MatchString(release) {
		return fmt.Errorf("invalid Helm release name %q", release)
	}
	out, err := run(ctx, "helm", "list", "--kube-context", p.Kubernetes.Context, "--namespace", "ops-system", "--all", "--filter", "^"+release+"$", "--output", "json")
	if err != nil {
		return fmt.Errorf("inspect Helm release %s: %w", release, err)
	}
	var existing []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &existing); err != nil {
		return fmt.Errorf("decode Helm release inventory: %w", err)
	}
	for _, item := range existing {
		if item.Name == release {
			return fmt.Errorf("PROFILE_COMPONENT_CONFLICT: Helm release %s already exists; refusing adoption or upgrade", release)
		}
	}
	return nil
}

func checkSecretReferences(ctx context.Context, value any, p profile.ResolvedProfile, run CommandRunner) error {
	switch object := value.(type) {
	case map[string]any:
		if object["kind"] == "Secret" {
			// The dependency Chart represents external credentials as metadata
			// references only. Actual secret bytes remain operator supplied.
			if object["type"] != "ops.platform.io/external-credential-reference" || object["data"] != nil || object["stringData"] != nil {
				return errors.New("offline charts may only create metadata-only external credential references")
			}
			metadata, _ := object["metadata"].(map[string]any)
			annotations, _ := metadata["annotations"].(map[string]any)
			name, _ := annotations["ops.platform.io/credential-secret-name"].(string)
			for _, annotation := range []string{"ops.platform.io/username-key", "ops.platform.io/password-key"} {
				key, _ := annotations[annotation].(string)
				ref := map[string]any{"secretKeyRef": map[string]any{"name": name, "key": key}}
				if err := checkSecretReferences(ctx, ref, p, run); err != nil {
					return err
				}
			}
		}
		if ref, ok := object["secretKeyRef"].(map[string]any); ok {
			name, _ := ref["name"].(string)
			key, _ := ref["key"].(string)
			if name == "" || key == "" || strings.ContainsAny(name+key, "\\\"\n\r") {
				return errors.New("invalid secret reference")
			}
			template := fmt.Sprintf("go-template={{if index .data %q}}present{{end}}", key)
			out, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", "ops-system", "get", "secret", name, "-o", template)
			if err != nil {
				return err
			}
			if string(out) != "present" {
				return fmt.Errorf("required secret %s key %s is missing", name, key)
			}
		}
		if ref, ok := object["secret"].(map[string]any); ok {
			name, _ := ref["secretName"].(string)
			if name == "" {
				return errors.New("invalid secret volume reference")
			}
			if _, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", "ops-system", "get", "secret", name, "-o", "name"); err != nil {
				return err
			}
		}
		for _, child := range object {
			if err := checkSecretReferences(ctx, child, p, run); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range object {
			if err := checkSecretReferences(ctx, child, p, run); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkRenderedImages(value any, allowed map[string]bool) error {
	switch object := value.(type) {
	case map[string]any:
		if image, ok := object["image"].(string); ok {
			if !allowed[image] || object["imagePullPolicy"] != "IfNotPresent" {
				return fmt.Errorf("rendered image %s is not in the verified Bundle or uses online pull policy", image)
			}
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := checkRenderedImages(object[key], allowed); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range object {
			if err := checkRenderedImages(child, allowed); err != nil {
				return err
			}
		}
	}
	return nil
}
