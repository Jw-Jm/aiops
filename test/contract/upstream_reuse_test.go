package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/supplychain"
)

type upstreamReuseLock struct {
	SchemaVersion        int                  `yaml:"schemaVersion"`
	Status               string               `yaml:"status"`
	Task                 string               `yaml:"task"`
	Capabilities         []upstreamCapability `yaml:"capabilities"`
	DeferredCapabilities []string             `yaml:"deferredCapabilities"`
}

type upstreamCapability struct {
	ID                        string               `yaml:"id"`
	Source                    string               `yaml:"source"`
	Version                   string               `yaml:"version"`
	Commit                    string               `yaml:"commit"`
	SourceArchiveSHA256       string               `yaml:"sourceArchiveSHA256"`
	License                   string               `yaml:"license"`
	LicenseEvidence           string               `yaml:"licenseEvidence"`
	LicenseEvidenceSHA256     string               `yaml:"licenseEvidenceSHA256"`
	State                     string               `yaml:"state"`
	Fixture                   string               `yaml:"fixture"`
	FixtureSHA256             string               `yaml:"fixtureSHA256"`
	Dependencies              []upstreamDependency `yaml:"dependencies"`
	Policy                    map[string]any       `yaml:"policy"`
	DisabledReason            string               `yaml:"disabledReason"`
	DependencyClosureVerified bool                 `yaml:"dependencyClosureVerified"`
	DependencyClosureEvidence struct {
		Path                  string `yaml:"path"`
		SHA256                string `yaml:"sha256"`
		Complete              bool   `yaml:"complete"`
		LicenseReviewComplete bool   `yaml:"licenseReviewComplete"`
		Scope                 string `yaml:"scope"`
		ModuleCount           int    `yaml:"moduleCount"`
		UnknownLicenses       int    `yaml:"unknownLicenses"`
	} `yaml:"dependencyClosureEvidence"`
	PlatformMapping struct {
		Input              string            `yaml:"input"`
		Target             string            `yaml:"target"`
		FieldMap           map[string]string `yaml:"fieldMap"`
		Fixture            string            `yaml:"fixture"`
		FixtureSHA         string            `yaml:"fixtureSHA256"`
		SourceFixture      lockedFile        `yaml:"sourceFixture"`
		ConformanceFixture lockedFile        `yaml:"conformanceFixture"`
		ConsumerBoundary   string            `yaml:"consumerBoundary"`
		DisabledReason     string            `yaml:"disabledReason"`
	} `yaml:"platformMapping"`
	OfflineReplay struct {
		Status              string        `yaml:"status"`
		NetworkMode         string        `yaml:"networkMode"`
		PullPolicy          string        `yaml:"pullPolicy"`
		DependencyDownloads bool          `yaml:"dependencyDownloads"`
		ExitCode            int           `yaml:"exitCode"`
		Command             []string      `yaml:"command"`
		ContainerImages     []lockedImage `yaml:"containerImages"`
		Log                 string        `yaml:"log"`
		LogSHA256           string        `yaml:"logSHA256"`
		AdditionalLogs      []lockedFile  `yaml:"additionalLogs"`
		ClosureLogs         []lockedFile  `yaml:"closureLogs"`
	} `yaml:"offlineReplay"`
	SelectedFiles    []lockedSelectedFile `yaml:"selectedFiles"`
	ConformanceFiles []lockedFile         `yaml:"conformanceFiles"`
}

type lockedFile struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

type lockedSelectedFile struct {
	SourcePath string `yaml:"sourcePath"`
	Path       string `yaml:"path"`
	SHA256     string `yaml:"sha256"`
	License    string `yaml:"license"`
}

type lockedImage struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	Digest  string `yaml:"digest"`
}

type upstreamDependency struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	Digest  string `yaml:"digest"`
	License string `yaml:"license"`
}

type dependencyClosureDocument struct {
	SchemaVersion int `json:"schemaVersion"`
	GeneratedFrom struct {
		GoImage     string                    `json:"goImage"`
		PythonImage string                    `json:"pythonImage"`
		Network     string                    `json:"network"`
		Records     []dependencyClosureRecord `json:"records"`
	} `json:"generatedFrom"`
}

type dependencyClosureRecord struct {
	ID                    string                     `json:"id"`
	Ecosystem             string                     `json:"ecosystem"`
	Scope                 string                     `json:"scope"`
	Network               string                     `json:"network"`
	InventoryComplete     bool                       `json:"inventoryComplete"`
	LicenseReviewComplete bool                       `json:"licenseReviewComplete"`
	UnknownLicenses       int                        `json:"unknownLicenses"`
	Modules               []selectedDependencyModule `json:"modules"`
}

type selectedDependencyModule struct {
	Ecosystem            string `json:"ecosystem"`
	Path                 string `json:"path"`
	Version              string `json:"version"`
	ArtifactType         string `json:"artifactType"`
	Artifact             string `json:"artifact"`
	ArtifactSHA256       string `json:"artifactSHA256"`
	SourceArtifact       string `json:"sourceArtifact"`
	SourceArtifactSHA256 string `json:"sourceArtifactSHA256"`
	License              string `json:"license"`
	LicenseFiles         []struct {
		Path    string `json:"path"`
		SHA256  string `json:"sha256"`
		License string `json:"license"`
	} `json:"licenseFiles"`
}

type platformMappingIndex struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Mappings      map[string]lockedFile `json:"mappings"`
}

type platformMappingDocument struct {
	SchemaVersion      int               `json:"schemaVersion"`
	ID                 string            `json:"id"`
	Input              string            `json:"input"`
	Target             string            `json:"target"`
	FieldMap           map[string]string `json:"fieldMap"`
	SourceFixture      lockedFile        `json:"sourceFixture"`
	ConformanceFixture lockedFile        `json:"conformanceFixture"`
	ConsumerBoundary   string            `json:"consumerBoundary"`
	DisabledReason     string            `json:"disabledReason"`
}

type thirdPartySource struct {
	Name                  string   `yaml:"name"`
	Status                string   `yaml:"status"`
	Source                string   `yaml:"source"`
	Version               string   `yaml:"version"`
	Commit                string   `yaml:"commit"`
	GitArchiveSHA256      string   `yaml:"gitArchiveSHA256"`
	License               string   `yaml:"license"`
	LicenseEvidence       string   `yaml:"licenseEvidence"`
	LicenseEvidenceSHA256 string   `yaml:"licenseEvidenceSHA256"`
	Use                   string   `yaml:"use"`
	Lock                  string   `yaml:"lock"`
	Fixtures              []string `yaml:"fixtures"`
}

func TestUpstreamReuseLockRejectsUnpinnedOrUnsafeBaselines(t *testing.T) {
	root := filepath.Join("..", "..")
	lockPath := filepath.Join(root, "docs", "poc", "inspection-reuse-lock.yaml")
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read upstream reuse lock: %v", err)
	}
	var lock upstreamReuseLock
	if err := yaml.Unmarshal(raw, &lock); err != nil {
		t.Fatalf("decode upstream reuse lock: %v", err)
	}
	if lock.SchemaVersion != 1 || lock.Task != "SP-02 Task 2.9" || lock.Status != "nonvirtual-review-ready" {
		t.Fatalf("unexpected lock header: schema=%d task=%q status=%q", lock.SchemaVersion, lock.Task, lock.Status)
	}
	deferred := stringSet(lock.DeferredCapabilities)
	for _, id := range []string{"kubevirt-observability-rules", "kubevirt-must-gather", "kubernetes-mcp-kubevirt-toolset"} {
		if !deferred[id] {
			t.Errorf("%s must remain explicitly deferred", id)
		}
	}

	capabilityIDs := []string{
		"k8sgpt-analyzer", "kubevirt-observability-rules", "kubevirt-must-gather",
		"kubernetes-mcp-kubevirt-toolset", "node-problem-detector-rules",
		"coroot-community-check", "keep-community-model", "metal3-bmo-model",
		"gofish-redfish", "ipmi-exporter", "smartctl-exporter",
	}
	nonVirtualIDs := []string{
		"k8sgpt-analyzer", "node-problem-detector-rules", "coroot-community-check",
		"keep-community-model", "metal3-bmo-model", "gofish-redfish",
		"ipmi-exporter", "smartctl-exporter",
	}
	byID := make(map[string]upstreamCapability, len(lock.Capabilities))
	for _, capability := range lock.Capabilities {
		if _, duplicate := byID[capability.ID]; duplicate {
			t.Fatalf("duplicate capability %q", capability.ID)
		}
		byID[capability.ID] = capability
	}
	shaPattern := regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitPattern := regexp.MustCompile(`^[0-9a-f]{40,64}$`)
	for _, id := range capabilityIDs {
		capability, ok := byID[id]
		if !ok {
			t.Errorf("missing locked upstream capability %q", id)
			continue
		}
		if capability.State != "disabled" && capability.State != "qualified" {
			t.Errorf("%s has invalid admission state %q", id, capability.State)
		}
		if capability.State == "disabled" && capability.DisabledReason == "" {
			t.Errorf("%s lacks disabled reason", id)
		}
		if capability.Source == "" || capability.Version == "" || capability.Version == "pending" || !commitPattern.MatchString(capability.Commit) {
			t.Errorf("%s lacks exact source, version, and commit", id)
		}
		if !shaPattern.MatchString(capability.SourceArchiveSHA256) {
			t.Errorf("%s source archive SHA-256 is not pinned", id)
		}
		if capability.License == "" || capability.License == "pending" || capability.LicenseEvidence == "" || !shaPattern.MatchString(capability.LicenseEvidenceSHA256) {
			t.Errorf("%s lacks file-level license evidence", id)
		} else if err := verifyLockedFile(root, capability.LicenseEvidence, capability.LicenseEvidenceSHA256); err != nil {
			t.Errorf("%s license evidence: %v", id, err)
		}
		if capability.Fixture == "" || !shaPattern.MatchString(capability.FixtureSHA256) {
			t.Errorf("%s lacks a locked upstream or conformance fixture", id)
		} else if err := verifyLockedFile(root, capability.Fixture, capability.FixtureSHA256); err != nil {
			t.Errorf("%s fixture: %v", id, err)
		}
		for _, file := range capability.SelectedFiles {
			if file.License == "" || !shaPattern.MatchString(file.SHA256) {
				t.Errorf("%s has unpinned selected file %s", id, file.Path)
				continue
			}
			if err := verifyLockedFile(root, file.Path, file.SHA256); err != nil {
				t.Errorf("%s selected file: %v", id, err)
			}
		}
		for _, file := range capability.ConformanceFiles {
			if !shaPattern.MatchString(file.SHA256) {
				t.Errorf("%s has unpinned conformance input %s", id, file.Path)
				continue
			}
			if err := verifyLockedFile(root, file.Path, file.SHA256); err != nil {
				t.Errorf("%s conformance input: %v", id, err)
			}
		}
		if capability.State == "qualified" && !capability.DependencyClosureVerified {
			t.Errorf("%s is qualified without a verified dependency inventory", id)
		}
		for _, dependency := range capability.Dependencies {
			if dependency.Name == "" || dependency.Version == "" || dependency.Version == "pending" || !shaPattern.MatchString(dependency.Digest) || dependency.License == "" || dependency.License == "pending" {
				t.Errorf("%s has an unpinned dependency: %+v", id, dependency)
			}
		}
	}
	for _, id := range lock.DeferredCapabilities {
		capability, ok := byID[id]
		if !ok || capability.State != "disabled" || getPolicyBool(capability.Policy, "enabledByDefault") || getPolicyBool(capability.Policy, "runtimeEnabled") {
			t.Errorf("deferred capability %s must remain disabled", id)
		}
	}

	closurePath := filepath.Join(root, "test", "fixtures", "upstream-inspection", "dependency-closures.json")
	closureBytes, err := os.ReadFile(closurePath)
	if err != nil {
		t.Fatalf("read selected-source dependency closures: %v", err)
	}
	var closure dependencyClosureDocument
	if err := json.Unmarshal(closureBytes, &closure); err != nil {
		t.Fatalf("decode dependency closure evidence: %v", err)
	}
	if closure.SchemaVersion != 1 || !strings.Contains(closure.GeneratedFrom.GoImage, "@sha256:") || !strings.Contains(closure.GeneratedFrom.PythonImage, "@sha256:") || !strings.Contains(closure.GeneratedFrom.Network, "network none") {
		t.Fatalf("dependency inventory is not tied to locked offline build images: %+v", closure.GeneratedFrom)
	}
	closureByID := make(map[string]dependencyClosureRecord, len(closure.GeneratedFrom.Records))
	for _, item := range closure.GeneratedFrom.Records {
		if _, duplicate := closureByID[item.ID]; duplicate {
			t.Fatalf("duplicate dependency closure %q", item.ID)
		}
		closureByID[item.ID] = item
	}
	for _, id := range nonVirtualIDs {
		capability := byID[id]
		if len(capability.SelectedFiles) == 0 {
			t.Errorf("%s lacks selected source files with file-level license declarations", id)
		}
		closureEvidence := capability.DependencyClosureEvidence
		if closureEvidence.Path == "" || !shaPattern.MatchString(closureEvidence.SHA256) {
			t.Errorf("%s lacks a hash-locked dependency closure", id)
			continue
		}
		if err := verifyLockedFile(root, closureEvidence.Path, closureEvidence.SHA256); err != nil {
			t.Errorf("%s dependency closure: %v", id, err)
		}
		record, ok := closureByID[id]
		if !ok {
			t.Errorf("%s has no dependency closure record", id)
			continue
		}
		if record.Network == "" || record.Scope == "" || !record.InventoryComplete || len(record.Modules) != closureEvidence.ModuleCount || closureEvidence.UnknownLicenses != record.UnknownLicenses || closureEvidence.Complete != record.InventoryComplete || closureEvidence.LicenseReviewComplete != record.LicenseReviewComplete {
			t.Errorf("%s dependency closure counts or status disagree with source evidence", id)
		}
		unknownLicenses := 0
		for _, module := range record.Modules {
			if module.Path == "" || module.Version == "" || module.ArtifactType == "" || !shaPattern.MatchString(module.ArtifactSHA256) || module.License == "" {
				t.Errorf("%s has an unpinned dependency artifact: %+v", id, module)
			}
			if module.License == "NOASSERTION" {
				unknownLicenses++
				if len(module.LicenseFiles) != 0 {
					t.Errorf("%s dependency %s claims license files while license is NOASSERTION", id, module.Path)
				}
				continue
			}
			if len(module.LicenseFiles) == 0 {
				t.Errorf("%s dependency %s has no file-level license evidence", id, module.Path)
			}
			for _, licenseFile := range module.LicenseFiles {
				if licenseFile.Path == "" || !strings.Contains(module.License, licenseFile.License) || !shaPattern.MatchString(licenseFile.SHA256) {
					t.Errorf("%s dependency %s has incomplete file license evidence: %+v", id, module.Path, licenseFile)
				}
			}
			if module.Ecosystem == "python" && (!shaPattern.MatchString(module.SourceArtifactSHA256) || module.SourceArtifact == "") {
				t.Errorf("%s Python dependency %s lacks its source distribution hash", id, module.Path)
			}
		}
		if unknownLicenses != record.UnknownLicenses {
			t.Errorf("%s records %d unknown licenses, found %d", id, record.UnknownLicenses, unknownLicenses)
		}
		if record.UnknownLicenses > 0 && (id != "k8sgpt-analyzer" || capability.State != "disabled" || record.LicenseReviewComplete) {
			t.Errorf("%s has unknown dependency licenses but is not held disabled", id)
		}
		if capability.State == "qualified" && (!record.LicenseReviewComplete || record.UnknownLicenses != 0) {
			t.Errorf("%s is qualified with an incomplete dependency license review", id)
		}
	}

	indexPath := filepath.Join(root, "test", "fixtures", "upstream-inspection", "platform-mappings", "index.json")
	indexBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read mapping index: %v", err)
	}
	var index platformMappingIndex
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		t.Fatalf("decode mapping index: %v", err)
	}
	if index.SchemaVersion != 1 {
		t.Fatalf("mapping index schemaVersion=%d, want 1", index.SchemaVersion)
	}
	for _, id := range nonVirtualIDs {
		capability := byID[id]
		mapping := capability.PlatformMapping
		if mapping.Input == "" || mapping.Target == "" || len(mapping.FieldMap) == 0 || mapping.Fixture == "" || !shaPattern.MatchString(mapping.FixtureSHA) || mapping.ConsumerBoundary == "" || mapping.DisabledReason == "" {
			t.Errorf("%s lacks a complete mapping and consumer boundary", id)
			continue
		}
		indexed, ok := index.Mappings[id]
		if !ok || indexed.Path != mapping.Fixture || indexed.SHA256 != mapping.FixtureSHA {
			t.Errorf("%s mapping index does not match its lock entry", id)
		}
		if err := verifyLockedFile(root, mapping.Fixture, mapping.FixtureSHA); err != nil {
			t.Errorf("%s mapping fixture: %v", id, err)
		}
		mappingBytes, err := os.ReadFile(filepath.Join(root, filepath.Clean(mapping.Fixture)))
		if err != nil {
			t.Errorf("%s read mapping fixture: %v", id, err)
			continue
		}
		var mappingDocument platformMappingDocument
		if err := json.Unmarshal(mappingBytes, &mappingDocument); err != nil {
			t.Errorf("%s decode mapping fixture: %v", id, err)
			continue
		}
		if mappingDocument.SchemaVersion != 1 || mappingDocument.ID != id || mappingDocument.Input != mapping.Input || mappingDocument.Target != mapping.Target || len(mappingDocument.FieldMap) == 0 || mappingDocument.ConsumerBoundary != mapping.ConsumerBoundary || mappingDocument.DisabledReason != mapping.DisabledReason || mappingDocument.SourceFixture != mapping.SourceFixture || mappingDocument.ConformanceFixture != mapping.ConformanceFixture {
			t.Errorf("%s mapping document and lock disagree", id)
		}
		for name, target := range mappingDocument.FieldMap {
			if name == "" || target == "" {
				t.Errorf("%s mapping contains an empty field projection", id)
			}
		}
		for _, fixture := range []lockedFile{mapping.SourceFixture, mapping.ConformanceFixture} {
			if fixture.Path == "" || !shaPattern.MatchString(fixture.SHA256) {
				t.Errorf("%s mapping has an unpinned source/conformance fixture", id)
				continue
			}
			if err := verifyLockedFile(root, fixture.Path, fixture.SHA256); err != nil {
				t.Errorf("%s mapping fixture: %v", id, err)
			}
		}
	}

	for _, id := range nonVirtualIDs {
		capability := byID[id]
		replay := capability.OfflineReplay
		if replay.Status != "pass" || replay.NetworkMode != "docker-network-none" || replay.PullPolicy != "never" || replay.DependencyDownloads || replay.ExitCode != 0 || len(replay.Command) == 0 || replay.Log == "" || !shaPattern.MatchString(replay.LogSHA256) {
			t.Errorf("%s lacks a successful no-network, no-pull fixture replay", id)
			continue
		}
		if err := verifyLockedFile(root, replay.Log, replay.LogSHA256); err != nil {
			t.Errorf("%s offline replay log: %v", id, err)
		}
		if len(replay.ContainerImages) == 0 {
			t.Errorf("%s replay lacks exact container image digests", id)
		}
		for _, image := range replay.ContainerImages {
			if image.Name == "" || image.Version == "" || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(image.Digest) {
				t.Errorf("%s replay uses an unpinned image: %+v", id, image)
			}
		}
		for _, log := range append(append([]lockedFile{}, replay.AdditionalLogs...), replay.ClosureLogs...) {
			if log.Path == "" || !shaPattern.MatchString(log.SHA256) {
				t.Errorf("%s replay has an unpinned supporting log", id)
				continue
			}
			if err := verifyLockedFile(root, log.Path, log.SHA256); err != nil {
				t.Errorf("%s supporting log: %v", id, err)
			}
		}
		if getPolicyBool(capability.Policy, "enabledByDefault") || getPolicyBool(capability.Policy, "runtimeEnabled") || getPolicyBool(capability.Policy, "fullRuntimeDeployed") {
			t.Errorf("%s must remain outside runtime admission", id)
		}
	}

	k8sgpt := byID["k8sgpt-analyzer"]
	if k8sgpt.State != "disabled" || getPolicyBool(k8sgpt.Policy, "llmEnabled") || getPolicyBool(k8sgpt.Policy, "explainEnabled") || getPolicyString(k8sgpt.Policy, "output") != "json" {
		t.Error("K8sGPT must remain disabled and use JSON without LLM or --explain")
	}
	k8sgptScript := filepath.Join(root, "test", "fixtures", "upstream-inspection", "verify-k8sgpt-cli.py")
	k8sgptScriptBytes, err := os.ReadFile(k8sgptScript)
	if err != nil {
		t.Fatalf("read deterministic K8sGPT CLI fixture: %v", err)
	}
	k8sgptScriptText := string(k8sgptScriptBytes)
	if !strings.Contains(k8sgptScriptText, `"--output", "json"`) || strings.Contains(k8sgptScriptText, `"--explain"`) || strings.Contains(k8sgptScriptText, `"--explain", "true"`) {
		t.Error("K8sGPT CLI fixture must request JSON output and must not invoke --explain")
	}
	for _, arg := range k8sgpt.OfflineReplay.Command {
		if strings.Contains(arg, "--explain") {
			t.Error("K8sGPT offline command must not include --explain")
		}
	}
	if getPolicyBool(byID["keep-community-model"].Policy, "eeIncluded") || getPolicyBool(byID["keep-community-model"].Policy, "fullRuntimeDeployed") {
		t.Error("Keep reuse must exclude ee/ and the full runtime")
	}
	if getPolicyBool(byID["kubevirt-must-gather"].Policy, "automaticNativeKubernetes") || getPolicyBool(byID["kubevirt-must-gather"].Policy, "privilegedCollector") {
		t.Error("native Kubernetes must-gather may not auto-run a privileged collector")
	}
	if getPolicyBool(byID["kubernetes-mcp-kubevirt-toolset"].Policy, "directCredentials") || getPolicyBool(byID["kubernetes-mcp-kubevirt-toolset"].Policy, "writeEnabled") {
		t.Error("KubeVirt MCP toolset may not keep direct credentials or writes")
	}
	for _, id := range []string{"coroot-community-check", "metal3-bmo-model", "keep-community-model"} {
		if getPolicyBool(byID[id].Policy, "fullRuntimeDeployed") {
			t.Errorf("%s must not deploy a complete upstream runtime", id)
		}
	}
	for _, id := range []string{"ipmi-exporter", "smartctl-exporter"} {
		if getPolicyBool(byID[id].Policy, "liveHardwareValidated") {
			t.Errorf("%s fixture-only result cannot claim live hardware validation", id)
		}
	}

	assertInspectionCatalogCandidates(t, root, nonVirtualIDs, byID)
	assertThirdPartyManifestLock(t, root, nonVirtualIDs, byID, shaPattern, commitPattern)
}

func assertInspectionCatalogCandidates(t *testing.T, root string, ids []string, byID map[string]upstreamCapability) {
	t.Helper()
	path := filepath.Join(root, "bundle", "component-catalog.yaml")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read component catalog: %v", err)
	}
	catalog, err := supplychain.LoadCatalogWithEvidence(bytes.NewReader(contents), os.DirFS(root))
	if err != nil {
		t.Fatalf("load component catalog evidence: %v", err)
	}
	componentName := map[string]string{
		"k8sgpt-analyzer":             "k8sgpt",
		"node-problem-detector-rules": "node-problem-detector",
		"coroot-community-check":      "coroot-community",
		"keep-community-model":        "keep-community",
		"metal3-bmo-model":            "metal3-bmo",
		"gofish-redfish":              "gofish",
		"ipmi-exporter":               "ipmi-exporter",
		"smartctl-exporter":           "smartctl-exporter",
	}
	for _, id := range ids {
		name := componentName[id]
		component, ok := catalog.Component(name)
		if !ok {
			t.Errorf("component catalog lacks %s", name)
			continue
		}
		capability := byID[id]
		if component.State != "candidate" || component.Source != capability.Source || component.Version != capability.Version || component.Commit != capability.Commit || component.Digest != "sha256:"+capability.SourceArchiveSHA256 || component.SourceArchiveSHA256 != "sha256:"+capability.SourceArchiveSHA256 || component.License != capability.License {
			t.Errorf("component catalog entry %s disagrees with review lock or is no longer candidate: state=%s source=%s version=%s commit=%s digest=%s archive=%s license=%s", name, component.State, component.Source, component.Version, component.Commit, component.Digest, component.SourceArchiveSHA256, component.License)
		}
		if err := catalog.ValidateBundle([]string{name}); err == nil || !strings.Contains(err.Error(), "candidate component") {
			t.Errorf("candidate %s was not rejected from Bundle: %v", name, err)
		}
	}
	for _, name := range []string{"kubevirt", "cdi"} {
		component, ok := catalog.Component(name)
		if !ok || component.State != "candidate" {
			t.Errorf("deferred %s component must remain a catalog candidate", name)
		}
	}
}

func assertThirdPartyManifestLock(t *testing.T, root string, ids []string, byID map[string]upstreamCapability, shaPattern, commitPattern *regexp.Regexp) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, "third_party", "manifest.yaml"))
	if err != nil {
		t.Fatalf("read third-party manifest: %v", err)
	}
	var manifest struct {
		Sources []thirdPartySource `yaml:"sources"`
	}
	if err := yaml.Unmarshal(contents, &manifest); err != nil {
		t.Fatalf("decode third-party manifest: %v", err)
	}
	rows := make(map[string]thirdPartySource)
	for _, row := range manifest.Sources {
		rows[row.Name] = row
	}
	manifestName := map[string]string{
		"k8sgpt-analyzer":             "k8sgpt-analyzer",
		"node-problem-detector-rules": "node-problem-detector-rules",
		"coroot-community-check":      "coroot-community-check",
		"keep-community-model":        "keep-community-model",
		"metal3-bmo-model":            "metal3-bmo-model",
		"gofish-redfish":              "gofish-redfish",
		"ipmi-exporter":               "ipmi-exporter",
		"smartctl-exporter":           "smartctl-exporter",
	}
	for _, id := range ids {
		capability := byID[id]
		row, ok := rows[manifestName[id]]
		if !ok {
			t.Errorf("third-party manifest lacks %s", manifestName[id])
			continue
		}
		if row.Status != "candidate" || row.Source != capability.Source || row.Version != capability.Version || row.Commit != capability.Commit || row.GitArchiveSHA256 != capability.SourceArchiveSHA256 || row.License != capability.License || row.LicenseEvidence != capability.LicenseEvidence || row.LicenseEvidenceSHA256 != capability.LicenseEvidenceSHA256 || row.Lock != "docs/poc/inspection-reuse-lock.yaml#"+id || len(row.Fixtures) == 0 || row.Use == "selected upstream test/model/rule baseline; runtime disabled" {
			t.Errorf("third-party manifest entry %s disagrees with the locked reuse boundary", row.Name)
		}
		if row.Source == "" || !commitPattern.MatchString(row.Commit) || !shaPattern.MatchString(row.GitArchiveSHA256) || !shaPattern.MatchString(row.LicenseEvidenceSHA256) {
			t.Errorf("third-party manifest entry %s is not precisely pinned", row.Name)
		}
		for _, fixture := range row.Fixtures {
			found := false
			for _, selected := range capability.SelectedFiles {
				if selected.Path == fixture {
					found = true
				}
			}
			for _, conformance := range capability.ConformanceFiles {
				if conformance.Path == fixture {
					found = true
				}
			}
			if capability.PlatformMapping.Fixture == fixture {
				found = true
			}
			if !found {
				t.Errorf("third-party manifest fixture %s is not covered by %s lock", fixture, id)
			}
		}
	}
}

func verifyLockedFile(root, relative, expectedSHA string) error {
	clean := filepath.Clean(relative)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || len(clean) >= 3 && clean[:3] == "../" {
		return fmt.Errorf("unsafe repository-relative path %q", relative)
	}
	path := filepath.Join(root, clean)
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != expectedSHA {
		return fmt.Errorf("sha256 mismatch for %s", relative)
	}
	return nil
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func getPolicyBool(policy map[string]any, key string) bool {
	value, _ := policy[key].(bool)
	return value
}

func getPolicyString(policy map[string]any, key string) string {
	value, _ := policy[key].(string)
	return value
}
