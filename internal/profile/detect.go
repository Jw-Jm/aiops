package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var imageDigestPattern = regexp.MustCompile(`(?:^|@)(sha256:[0-9a-f]{64})$`)

func Detect(input InputProfile, discovery Discovery) (InputProfile, error) {
	if input.Selected == "" {
		input.Selected = "core"
	}
	if err := input.ValidateTemplate(); err != nil {
		return InputProfile{}, err
	}
	for name, component := range input.Components {
		if !isEnabled(component, input.Selected) {
			component.Mode = "disabled"
			component.DisabledReason = "not selected by profile " + input.Selected
			input.Components[name] = component
			continue
		}
		if component.Mode == "disabled" {
			continue
		}
		candidates := discovery.Components[name]
		if len(candidates) > 1 {
			return InputProfile{}, profileConflict(name, "multiple candidate instances were discovered")
		}
		if len(candidates) == 1 && !candidates[0].Compatible && candidates[0].Compatibility != "unverified" {
			return InputProfile{}, profileConflict(name, "a candidate exists but its version, health, or required capability is incompatible or unknown")
		}
		switch component.Mode {
		case "detect":
			if len(candidates) == 1 {
				setCandidate(&component, candidates[0])
				component.Mode = "external"
			} else {
				component.Mode = "bundled"
				component.Evidence = []string{"read-only discovery found no compatible existing instance"}
			}
		case "bundled":
			if len(candidates) != 0 {
				return InputProfile{}, profileConflict(name, "profile explicitly selects bundled while an existing instance is present")
			}
			component.Evidence = []string{"read-only discovery found no existing instance"}
		case "external":
			if len(candidates) != 1 {
				return InputProfile{}, profileConflict(name, "profile selects external but there is not exactly one compatible existing instance")
			}
			setCandidate(&component, candidates[0])
		default:
			return InputProfile{}, profileConflict(name, "component mode must be detect, external, bundled, or disabled")
		}
		input.Components[name] = component
	}
	input.Kind = "detected"
	input.Context = discovery.Kubernetes.Context
	input.Architecture = discovery.Kubernetes.Architecture
	input.Kubernetes = discovery.Kubernetes
	input.Runtime.ImageImporter = discovery.Runtime.ImageImporter
	if input.Discovery == nil {
		input.Discovery = &DiscoveryEvidence{}
	}
	input.Discovery.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	input.Discovery.Context = discovery.Kubernetes.Context
	input.Discovery.ClusterUID = discovery.Kubernetes.ClusterUID
	input.Discovery.ServerVersion = discovery.Kubernetes.ServerVersion
	if err := validateProfileSchema(input); err != nil {
		return InputProfile{}, err
	}
	return input, nil
}

func Discover(ctx context.Context, contextName, kubectlPath, catalogPath string) (Discovery, error) {
	if strings.TrimSpace(contextName) == "" {
		return Discovery{}, errors.New("Kubernetes context is required")
	}
	if kubectlPath == "" {
		kubectlPath = "kubectl"
	}
	reader := kubectlReader{path: kubectlPath, context: contextName}
	versionBytes, err := reader.raw(ctx, "/version")
	if err != nil {
		return Discovery{}, fmt.Errorf("KUBERNETES_UNAVAILABLE: context %q is not readable: %w", contextName, err)
	}
	var version struct {
		GitVersion string `json:"gitVersion"`
		Platform   string `json:"platform"`
	}
	if err := json.Unmarshal(versionBytes, &version); err != nil {
		return Discovery{}, fmt.Errorf("decode Kubernetes version: %w", err)
	}
	if version.GitVersion == "" || version.Platform == "" {
		return Discovery{}, errors.New("KUBERNETES_UNAVAILABLE: server version is missing gitVersion or platform")
	}
	nodes, err := reader.getJSON(ctx, "nodes", false)
	if err != nil {
		return Discovery{}, fmt.Errorf("read Kubernetes nodes: %w", err)
	}
	namespaces, err := reader.getJSON(ctx, "namespaces", false)
	if err != nil {
		return Discovery{}, fmt.Errorf("read Kubernetes namespaces: %w", err)
	}
	storageClasses, err := reader.getJSON(ctx, "storageclasses", false)
	if err != nil {
		return Discovery{}, fmt.Errorf("read StorageClasses: %w", err)
	}
	crds, err := reader.getJSON(ctx, "crd", false)
	if err != nil {
		return Discovery{}, fmt.Errorf("read CRDs: %w", err)
	}
	resources, err := reader.getJSON(ctx, "deployments,statefulsets,daemonsets,pods,services", true)
	if err != nil {
		return Discovery{}, fmt.Errorf("read workloads and Services: %w", err)
	}
	result := Discovery{Components: make(map[string][]ComponentCandidate)}
	result.Kubernetes = KubernetesDiscovery{
		Distribution:  "kubernetes",
		ServerVersion: version.GitVersion,
		Architecture:  architectureFromPlatform(version.Platform),
		Context:       contextName,
	}
	if strings.Contains(strings.ToLower(contextName), "orbstack") {
		result.Kubernetes.Distribution = "orbstack"
	}
	if len(nodes) > 0 {
		if architecture := nodeArchitecture(nodes[0]); architecture != "" {
			result.Kubernetes.Architecture = architecture
		}
	}
	for _, namespace := range namespaces {
		meta := objectMap(namespace["metadata"])
		if meta["name"] == "kube-system" {
			result.Kubernetes.ClusterUID, _ = meta["uid"].(string)
			break
		}
	}
	if result.Kubernetes.ClusterUID == "" {
		return Discovery{}, errors.New("KUBERNETES_UNAVAILABLE: cluster identity cannot be derived from the kube-system Namespace UID")
	}
	for _, storageClass := range storageClasses {
		meta := objectMap(storageClass["metadata"])
		annotations := objectMap(meta["annotations"])
		if annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			result.Kubernetes.StorageClass, _ = meta["name"].(string)
			break
		}
	}
	for _, crd := range crds {
		meta := objectMap(crd["metadata"])
		if name, ok := meta["name"].(string); ok {
			result.Kubernetes.CRDs = append(result.Kubernetes.CRDs, name)
		}
	}
	result.Kubernetes.KubeVirt = "unverified"
	result.Components = discoverComponents(ctx, resources, reader)
	enrichVictoriaCandidates(ctx, reader, result.Components)
	crdSet := make(map[string]bool, len(result.Kubernetes.CRDs))
	for _, name := range result.Kubernetes.CRDs {
		crdSet[name] = true
	}
	if crdSet["kubevirts.kubevirt.io"] {
		result.Kubernetes.KubeVirtInstalled = true
		instances, err := reader.getJSON(ctx, "kubevirts.kubevirt.io", true)
		if err != nil {
			return Discovery{}, fmt.Errorf("read existing KubeVirt resources: %w", err)
		}
		result.Kubernetes.KubeVirtVersion = resourceVersion(instances, "observedKubeVirtVersion", "targetKubeVirtVersion", "kubevirtVersion")
		result.Components["kubevirt"] = append(result.Components["kubevirt"], operatorCandidates(resources, instances, "kubevirt")...)
	}
	if crdSet["cdis.cdi.kubevirt.io"] {
		result.Kubernetes.CDIInstalled = true
		instances, err := reader.getJSON(ctx, "cdis.cdi.kubevirt.io", true)
		if err != nil {
			return Discovery{}, fmt.Errorf("read existing CDI resources: %w", err)
		}
		result.Kubernetes.CDIVersion = resourceVersion(instances, "observedVersion", "operatorVersion", "targetVersion")
		result.Components["cdi"] = append(result.Components["cdi"], operatorCandidates(resources, instances, "cdi")...)
	}
	locks, err := loadComponentLocks(catalogPath, result.Kubernetes.Architecture)
	if err != nil {
		return Discovery{}, err
	}
	result.Locks = locks
	completeDigestOnlyCandidateVersions(result.Components, locks)
	if result.Kubernetes.Distribution == "orbstack" {
		result.Runtime.ImageImporter = "unverified"
	}
	return result, nil
}

func completeDigestOnlyCandidateVersions(components map[string][]ComponentCandidate, locks map[string]ComponentLock) {
	const missingVersionEvidence = "image version or runtime digest is unavailable"
	for name, candidates := range components {
		lock, ok := locks[name]
		if !ok || !versionPattern.MatchString(lock.Version) || !digestPattern.MatchString(lock.Digest) {
			continue
		}
		lockImage, _, lockHasDigest := strings.Cut(lock.Image, "@")
		if !lockHasDigest {
			continue
		}
		for index := range candidates {
			candidate := &candidates[index]
			if candidate.Version != "" || candidate.Digest != lock.Digest || !strings.Contains(candidate.Image, "@") {
				continue
			}
			image, _, hasDigest := strings.Cut(candidate.Image, "@")
			if !hasDigest || image != lockImage {
				continue
			}
			missingVersion := false
			remainingEvidence := candidate.Evidence[:0]
			for _, evidence := range candidate.Evidence {
				if evidence == missingVersionEvidence {
					missingVersion = true
					continue
				}
				remainingEvidence = append(remainingEvidence, evidence)
			}
			if !missingVersion {
				continue
			}
			candidate.Evidence = append(remainingEvidence, fmt.Sprintf("exact version %s resolved from Component Catalog by runtime image digest %s", lock.Version, lock.Digest))
			candidate.Version = lock.Version
			candidate.Compatible = candidate.Endpoint != "" && digestPattern.MatchString(candidate.Digest)
		}
		components[name] = candidates
	}
}

func resourceVersion(instances []map[string]any, fields ...string) string {
	for _, instance := range instances {
		status := objectMap(instance["status"])
		for _, field := range fields {
			if value, ok := status[field].(string); ok && value != "" {
				return value
			}
		}
	}
	return ""
}

func operatorCandidates(resources, instances []map[string]any, component string) []ComponentCandidate {
	nameMatcher := "virt-operator"
	if component == "cdi" {
		nameMatcher = "cdi-operator"
	}
	candidate := ComponentCandidate{Compatible: true, Compatibility: "unverified", Endpoint: "https://kubernetes.default.svc"}
	for _, instance := range instances {
		meta := objectMap(instance["metadata"])
		candidate.Name, _ = meta["name"].(string)
		candidate.Namespace, _ = meta["namespace"].(string)
		candidate.Evidence = append(candidate.Evidence, fmt.Sprintf("existing %s API object %s/%s", component, candidate.Namespace, candidate.Name))
	}
	for _, resource := range resources {
		if objectKind(resource) != "Pod" {
			continue
		}
		podMeta := objectMap(resource["metadata"])
		containerStatuses, _ := objectMap(resource["status"])["containerStatuses"].([]any)
		for _, rawStatus := range containerStatuses {
			status := objectMap(rawStatus)
			containerName, _ := status["name"].(string)
			container := findContainer(resource, containerName)
			image, _ := container["image"].(string)
			if !strings.Contains(strings.ToLower(image), nameMatcher) {
				continue
			}
			imageID, _ := status["imageID"].(string)
			candidate.Version = imageTag(image)
			candidate.Digest = digestFromImageID(imageID)
			candidate.Image = imageDigestReference(image, imageID)
			candidate.Evidence = append(candidate.Evidence, fmt.Sprintf("operator Pod %s/%s imageID %s", podMeta["namespace"], podMeta["name"], imageID))
			break
		}
		if candidate.Digest != "" {
			break
		}
	}
	for _, instance := range instances {
		if version := resourceVersion([]map[string]any{instance}, "observedKubeVirtVersion", "targetKubeVirtVersion", "kubevirtVersion", "observedVersion", "operatorVersion", "targetVersion"); version != "" {
			candidate.Version = version
			candidate.Evidence = append(candidate.Evidence, "version observed from resource status")
			break
		}
	}
	if len(instances) == 0 {
		candidate.Name = component + "-crd"
		candidate.Evidence = append(candidate.Evidence, "API CRD exists but no custom resource is present")
	}
	return []ComponentCandidate{candidate}
}

type kubectlReader struct {
	path    string
	context string
}

func (reader kubectlReader) raw(ctx context.Context, resourcePath string) ([]byte, error) {
	return reader.run(ctx, "get", "--raw="+resourcePath)
}

func (reader kubectlReader) getJSON(ctx context.Context, resources string, allNamespaces bool) ([]map[string]any, error) {
	args := []string{"get", resources, "--output=json"}
	if allNamespaces {
		args = append(args, "--all-namespaces")
	}
	data, err := reader.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("decode %s JSON: %w", resources, err)
	}
	return list.Items, nil
}

func (reader kubectlReader) run(ctx context.Context, args ...string) ([]byte, error) {
	commandArgs := []string{"--context", reader.context}
	commandArgs = append(commandArgs, args...)
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, reader.path, commandArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func setCandidate(component *ComponentInput, candidate ComponentCandidate) {
	component.Version = candidate.Version
	component.Digest = candidate.Digest
	component.Image = candidate.Image
	component.Endpoint = candidate.Endpoint
	component.Namespace = candidate.Namespace
	component.Name = candidate.Name
	component.Compatibility = candidate.Compatibility
	if component.Compatibility == "" && candidate.Compatible {
		component.Compatibility = "supported"
	}
	component.Evidence = append([]string(nil), candidate.Evidence...)
}

func discoverComponents(ctx context.Context, resources []map[string]any, reader kubectlReader) map[string][]ComponentCandidate {
	byName := make(map[string][]ComponentCandidate)
	for _, service := range resources {
		if objectKind(service) != "Service" {
			continue
		}
		component := componentForService(service)
		if component == "" {
			continue
		}
		candidate := serviceCandidate(service, component, resources)
		port, protocol := servicePort(service, component)
		if port == 0 {
			candidate.Compatible = false
			candidate.Evidence = append(candidate.Evidence, "Service has no recognized component port")
		} else {
			candidate.Endpoint = componentEndpoint(component, candidate.Name, candidate.Namespace, port, protocol)
			candidate.Evidence = append(candidate.Evidence, fmt.Sprintf("Service %s/%s port %d", candidate.Namespace, candidate.Name, port))
		}
		if candidate.Version == "" || candidate.Digest == "" {
			candidate.Compatible = false
			candidate.Evidence = append(candidate.Evidence, "image version or runtime digest is unavailable")
		}
		if candidate.Version != "" && !versionPattern.MatchString(candidate.Version) {
			candidate.Compatible = false
			candidate.Evidence = append(candidate.Evidence, "image version is not an exact release")
		}
		if candidate.Digest != "" && !digestPattern.MatchString(candidate.Digest) {
			candidate.Compatible = false
			candidate.Evidence = append(candidate.Evidence, "runtime image ID does not contain a sha256 digest")
		}
		byName[component] = append(byName[component], candidate)
	}
	return byName
}

func componentForService(service map[string]any) string {
	metadata := objectMap(service["metadata"])
	name, _ := metadata["name"].(string)
	identity := strings.ToLower(name + " " + labelsToText(objectMap(metadata["labels"])))
	switch {
	case strings.Contains(identity, "vmalert"):
		return "vmalert"
	case strings.Contains(identity, "victoria-logs") || strings.Contains(identity, "victorialogs") || strings.Contains(identity, "vlogs"):
		return "victoriaLogs"
	case strings.Contains(strings.ToLower(name), "operator") || strings.Contains(identity, "vmagent"):
		return ""
	case strings.Contains(identity, "vmsingle") || strings.Contains(identity, "vmselect") || (strings.Contains(identity, "victoria-metrics") && (hasPort(service, 8428) || hasPort(service, 8429))):
		return "victoriaMetrics"
	case hasPort(service, 5432) && (strings.Contains(identity, "postgres") || strings.Contains(identity, "pgsql")):
		return "postgresql"
	case hasPort(service, 8080) && strings.Contains(identity, "keycloak"):
		return "keycloak"
	case hasPort(service, 8333) && strings.Contains(identity, "seaweed"):
		return "seaweedfs"
	case hasPort(service, 8200) && (strings.Contains(identity, "openbao") || strings.Contains(identity, "bao")):
		return "openbao"
	default:
		return ""
	}
}

func serviceCandidate(service map[string]any, component string, resources []map[string]any) ComponentCandidate {
	meta := objectMap(service["metadata"])
	namespace, _ := meta["namespace"].(string)
	name, _ := meta["name"].(string)
	candidate := ComponentCandidate{Namespace: namespace, Name: name, Compatible: true}
	selector := objectMap(objectMap(service["spec"])["selector"])
	for _, resource := range resources {
		if objectKind(resource) != "Pod" {
			continue
		}
		podMeta := objectMap(resource["metadata"])
		podNamespace, _ := podMeta["namespace"].(string)
		if podNamespace != namespace || !labelsMatch(selector, objectMap(podMeta["labels"])) {
			continue
		}
		status := objectMap(resource["status"])
		statusList, _ := status["containerStatuses"].([]any)
		for index, statusValue := range statusList {
			containerStatus := objectMap(statusValue)
			containerName, _ := containerStatus["name"].(string)
			imageID, _ := containerStatus["imageID"].(string)
			container := findContainer(resource, containerName)
			image, _ := container["image"].(string)
			if !imageMatchesComponent(image, component) {
				continue
			}
			candidate.Image = imageDigestReference(image, imageID)
			candidate.Version = imageTag(image)
			candidate.Digest = digestFromImageID(imageID)
			candidate.Evidence = append(candidate.Evidence, fmt.Sprintf("Pod %s/%s container[%d] imageID %s", namespace, podMeta["name"], index, imageID))
			break
		}
		if candidate.Digest != "" {
			break
		}
	}
	return candidate
}

func servicePort(service map[string]any, component string) (int, string) {
	ports, _ := objectMap(service["spec"])["ports"].([]any)
	preferred := map[string][]int{"victoriaMetrics": {8428, 8429}, "victoriaLogs": {9428}, "vmalert": {8880}, "postgresql": {5432}, "keycloak": {8080}, "seaweedfs": {8333}, "openbao": {8200}}
	for _, rawPort := range ports {
		if number, ok := asInt(objectMap(rawPort)["port"]); ok && containsInt(preferred[component], number) {
			protocol := "http"
			if component == "openbao" {
				protocol = "https"
			}
			return number, protocol
		}
	}
	return 0, ""
}

func containsInt(values []int, expected int) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func hasPort(service map[string]any, port int) bool {
	ports, _ := objectMap(service["spec"])["ports"].([]any)
	for _, rawPort := range ports {
		if number, ok := asInt(objectMap(rawPort)["port"]); ok && number == port {
			return true
		}
	}
	return false
}

func componentEndpoint(component, service, namespace string, port int, protocol string) string {
	if component == "postgresql" {
		return fmt.Sprintf("postgresql://%s.%s.svc.cluster.local:%d", service, namespace, port)
	}
	return fmt.Sprintf("%s://%s.%s.svc.cluster.local:%d", protocol, service, namespace, port)
}

func imageMatchesComponent(image, component string) bool {
	value := strings.ToLower(image)
	switch component {
	case "victoriaMetrics":
		return strings.Contains(value, "victoriametrics/victoria-metrics") && !strings.Contains(value, "vmagent")
	case "victoriaLogs":
		return strings.Contains(value, "victoria-logs")
	case "vmalert":
		return strings.Contains(value, "vmalert")
	case "postgresql":
		return strings.Contains(value, "postgres")
	case "keycloak":
		return strings.Contains(value, "keycloak")
	case "seaweedfs":
		return strings.Contains(value, "seaweedfs")
	case "openbao":
		return strings.Contains(value, "openbao")
	default:
		return false
	}
}

func imageDigestReference(image, imageID string) string {
	digest := digestFromImageID(imageID)
	if digest == "" {
		return ""
	}
	name := image
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at]
	}
	if colon := strings.LastIndex(name, ":"); colon > strings.LastIndex(name, "/") {
		name = name[:colon]
	}
	return name + "@" + digest
}

func imageTag(image string) string {
	name := image
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at]
	}
	colon := strings.LastIndex(name, ":")
	if colon <= strings.LastIndex(name, "/") {
		return ""
	}
	return name[colon+1:]
}

func digestFromImageID(imageID string) string {
	match := imageDigestPattern.FindStringSubmatch(strings.TrimPrefix(imageID, "docker-pullable://"))
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func findContainer(pod map[string]any, name string) map[string]any {
	containers, _ := objectMap(pod["spec"])["containers"].([]any)
	for _, raw := range containers {
		container := objectMap(raw)
		if container["name"] == name {
			return container
		}
	}
	return nil
}

func labelsMatch(selector, labels map[string]any) bool {
	if len(selector) == 0 {
		return false
	}
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func labelsToText(labels map[string]any) string {
	parts := make([]string, 0, len(labels))
	for key, value := range labels {
		parts = append(parts, key+"="+fmt.Sprint(value))
	}
	return strings.Join(parts, " ")
}

func objectMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func objectKind(value map[string]any) string {
	kind, _ := value["kind"].(string)
	return kind
}

func asInt(value any) (int, bool) {
	if number, ok := value.(float64); ok {
		return int(number), true
	}
	if number, ok := value.(int); ok {
		return number, true
	}
	return 0, false
}

func architectureFromPlatform(platform string) string {
	_, architecture, found := strings.Cut(platform, "/")
	if found {
		return architecture
	}
	return ""
}

func nodeArchitecture(node map[string]any) string {
	labels := objectMap(objectMap(node["metadata"])["labels"])
	architecture, _ := labels["kubernetes.io/arch"].(string)
	return architecture
}

func isEnabled(component ComponentInput, selected string) bool {
	if len(component.EnabledIn) == 0 {
		return true
	}
	for _, candidate := range component.EnabledIn {
		if candidate == selected {
			return true
		}
	}
	return false
}

func profileConflict(component, reason string) error {
	return fmt.Errorf("PROFILE_COMPONENT_CONFLICT: %s: %s", component, reason)
}
