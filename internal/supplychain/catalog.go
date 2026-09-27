package supplychain

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	commitPattern       = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)
	digestPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	exactVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	exactReleasePattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?(-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)
)

var knownLicenses = map[string]struct{}{
	"0BSD": {}, "Apache-2.0": {}, "BSD-2-Clause": {}, "BSD-3-Clause": {},
	"GPL-2.0-only": {}, "GPL-2.0-or-later": {}, "GPL-3.0-only": {}, "GPL-3.0-or-later": {},
	"ISC": {}, "MIT": {}, "MPL-2.0": {},
	"AGPL-3.0-only": {}, "AGPL-3.0-or-later": {},
}

type Catalog struct {
	Components        []Component
	FirstPartyKernels []FirstPartyKernel
	byName            map[string]Component
	evidenceVerified  bool
}

type Component struct {
	Name                      string
	State                     string
	Version                   string
	Source                    string
	Commit                    string
	Digest                    string
	License                   string
	SpecialLicenseADR         string
	Usage                     string
	ReuseMode                 string
	LinkageMode               string
	SourceSnapshot            bool
	SourceArchiveSHA256       string
	DependencyClosureVerified bool
	RequiredFor1_0            bool
	OfficialSupportSources    []string
	FileLicenses              []FileLicense
	Architectures             []string
	ImportedPaths             []string
	DependencyClosure         []Dependency
	Patches                   []Patch
	ForkPolicy                any
	Owner                     string
	POCReport                 string
	ConformanceFixtures       []string
	ExitPlan                  string
	PendingFields             map[string]bool
}

type FileLicense struct {
	Path    string
	License string
	Digest  string
}

type Dependency struct {
	Name    string
	Version string
	Source  string
	Commit  string
	Digest  string
	License string
}

type Patch struct {
	Path   string
	Digest string
}

type FirstPartyKernel struct {
	Domain         string
	Implementation string
	ReuseGate      string
	POCReport      string
	ExitPlan       string
}

func (c Catalog) Component(name string) (Component, bool) {
	component, ok := c.byName[name]
	return component, ok
}

// LoadCatalog decodes candidate entries. Qualified entries require an evidence
// filesystem and must be loaded with LoadCatalogWithEvidence.
func LoadCatalog(reader io.Reader) (Catalog, error) {
	catalog, err := loadCatalog(reader)
	if err != nil {
		return Catalog{}, err
	}
	for _, component := range catalog.Components {
		if component.State == "qualified" {
			return Catalog{}, fmt.Errorf("qualified component %q evidence requires LoadCatalogWithEvidence", component.Name)
		}
	}
	return catalog, nil
}

// LoadCatalogWithEvidence decodes and validates the catalog and resolves all
// PoC reports and conformance fixtures for qualified components from evidence.
func LoadCatalogWithEvidence(reader io.Reader, evidence fs.FS) (Catalog, error) {
	catalog, err := loadCatalog(reader)
	if err != nil {
		return Catalog{}, err
	}
	if err := catalog.validateQualifiedEvidence(evidence); err != nil {
		return Catalog{}, err
	}
	catalog.evidenceVerified = true
	return catalog, nil
}

func loadCatalog(reader io.Reader) (Catalog, error) {
	decoder := yaml.NewDecoder(reader)
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return Catalog{}, fmt.Errorf("decode component catalog: %w", err)
	}
	if err := rejectAdditionalDocument(decoder); err != nil {
		return Catalog{}, err
	}
	if document == nil {
		return Catalog{}, fmt.Errorf("component catalog is empty")
	}
	for field := range document {
		if field != "schemaVersion" && field != "components" && field != "firstPartyKernels" {
			return Catalog{}, fmt.Errorf("component catalog has unknown field %q", field)
		}
	}
	if number, ok := document["schemaVersion"].(int); !ok || number != 1 {
		return Catalog{}, fmt.Errorf("schemaVersion must be 1")
	}

	componentValues, ok := document["components"].([]any)
	if !ok || len(componentValues) == 0 {
		return Catalog{}, fmt.Errorf("components must be a non-empty list")
	}
	catalog := Catalog{byName: make(map[string]Component, len(componentValues))}
	for index, value := range componentValues {
		values, ok := value.(map[string]any)
		if !ok {
			return Catalog{}, fmt.Errorf("components[%d] must be an object", index)
		}
		component, err := parseComponent(values)
		if err != nil {
			return Catalog{}, fmt.Errorf("components[%d]: %w", index, err)
		}
		if _, exists := catalog.byName[component.Name]; exists {
			return Catalog{}, fmt.Errorf("duplicate component %q", component.Name)
		}
		if err := validateComponent(component); err != nil {
			return Catalog{}, fmt.Errorf("component %q: %w", component.Name, err)
		}
		catalog.Components = append(catalog.Components, component)
		catalog.byName[component.Name] = component
	}
	if raw, present := document["firstPartyKernels"]; present {
		values, ok := raw.([]any)
		if !ok {
			return Catalog{}, fmt.Errorf("firstPartyKernels must be a list")
		}
		for index, value := range values {
			mapping, ok := value.(map[string]any)
			if !ok {
				return Catalog{}, fmt.Errorf("firstPartyKernels[%d] must be an object", index)
			}
			kernel, err := parseKernel(mapping)
			if err != nil {
				return Catalog{}, fmt.Errorf("firstPartyKernels[%d]: %w", index, err)
			}
			if err := validateKernel(kernel); err != nil {
				return Catalog{}, err
			}
			catalog.FirstPartyKernels = append(catalog.FirstPartyKernels, kernel)
		}
	}
	return catalog, nil
}

func (c Catalog) ValidateBundle(componentNames []string) error {
	selected := make(map[string]bool, len(componentNames))
	for _, name := range componentNames {
		selected[name] = true
	}
	if selected["deepflow"] && selected["deepflow-app"] {
		return fmt.Errorf("DeepFlow bundle must not contain deepflow-app")
	}
	for _, name := range componentNames {
		component, ok := c.byName[name]
		if !ok {
			return fmt.Errorf("bundle references unknown component %q", name)
		}
		if component.State != "qualified" {
			return fmt.Errorf("candidate component %q cannot enter a Bundle", name)
		}
		if !c.evidenceVerified {
			return fmt.Errorf("qualified component %q evidence was not resolved before Bundle admission", name)
		}
	}
	return nil
}

func (c Catalog) validateQualifiedEvidence(evidence fs.FS) error {
	for _, component := range c.Components {
		if component.State != "qualified" {
			continue
		}
		if err := validateEvidenceFile(evidence, "PoC report", component.POCReport); err != nil {
			return fmt.Errorf("component %q: %w", component.Name, err)
		}
		for _, fixture := range component.ConformanceFixtures {
			if err := validateEvidenceFile(evidence, "conformance fixture", fixture); err != nil {
				return fmt.Errorf("component %q: %w", component.Name, err)
			}
		}
	}
	return nil
}

func validateEvidenceFile(evidence fs.FS, kind, name string) error {
	if evidence == nil {
		return fmt.Errorf("%s evidence filesystem is required", kind)
	}
	if name == "." || !fs.ValidPath(name) {
		return fmt.Errorf("%s path %q must be a clean relative path", kind, name)
	}
	info, err := fs.Stat(evidence, name)
	if err != nil {
		return fmt.Errorf("%s %q cannot be resolved: %w", kind, name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s %q must resolve to a regular file", kind, name)
	}
	contents, err := fs.ReadFile(evidence, name)
	if err != nil {
		return fmt.Errorf("read %s %q: %w", kind, name, err)
	}
	if strings.TrimSpace(string(contents)) == "" {
		return fmt.Errorf("%s %q is empty", kind, name)
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".json":
		if !json.Valid(contents) {
			return fmt.Errorf("%s %q must contain valid JSON", kind, name)
		}
	case ".yaml", ".yml":
		var document any
		if err := yaml.Unmarshal(contents, &document); err != nil {
			return fmt.Errorf("%s %q must contain valid YAML: %w", kind, name, err)
		}
	}
	return nil
}

func parseComponent(values map[string]any) (Component, error) {
	allowed := map[string]bool{
		"name": true, "state": true, "version": true, "source": true, "commit": true,
		"digest": true, "license": true, "fileLicenses": true, "sourceSnapshot": true,
		"sourceArchiveSHA256": true, "architectures": true, "usage": true, "reuseMode": true,
		"linkageMode": true, "importedPaths": true, "dependencyClosure": true,
		"dependencyClosureVerified": true, "patches": true, "forkPolicy": true, "owner": true,
		"pocReport": true, "conformanceFixtures": true, "exitPlan": true,
		"requiredFor1_0": true, "officialSupportSources": true, "candidateNote": true,
		"specialLicenseADR": true,
	}
	for key := range values {
		if !allowed[key] {
			return Component{}, fmt.Errorf("unknown field %q", key)
		}
	}
	component := Component{PendingFields: make(map[string]bool)}
	for _, field := range []string{
		"name", "state", "version", "source", "commit", "digest", "license", "usage", "reuseMode",
		"linkageMode", "owner", "pocReport", "exitPlan",
	} {
		value, exists := values[field]
		if !exists {
			return Component{}, fmt.Errorf("missing required field %q", field)
		}
		text, ok := value.(string)
		if !ok {
			return Component{}, fmt.Errorf("field %q must be a string", field)
		}
		if strings.TrimSpace(text) == "" {
			return Component{}, fmt.Errorf("field %q must not be empty", field)
		}
		component.PendingFields[field] = text == "pending"
		switch field {
		case "name":
			component.Name = text
		case "state":
			component.State = text
		case "version":
			component.Version = text
		case "source":
			component.Source = text
		case "commit":
			component.Commit = text
		case "digest":
			component.Digest = text
		case "license":
			component.License = text
		case "usage":
			component.Usage = text
		case "reuseMode":
			component.ReuseMode = text
		case "linkageMode":
			component.LinkageMode = text
		case "owner":
			component.Owner = text
		case "pocReport":
			component.POCReport = text
		case "exitPlan":
			component.ExitPlan = text
		}
	}
	var err error
	if component.FileLicenses, component.PendingFields["fileLicenses"], err = stringOrFileLicenses(values, "fileLicenses"); err != nil {
		return Component{}, err
	}
	if component.Architectures, component.PendingFields["architectures"], err = stringList(values, "architectures"); err != nil {
		return Component{}, err
	}
	if component.ImportedPaths, component.PendingFields["importedPaths"], err = stringList(values, "importedPaths"); err != nil {
		return Component{}, err
	}
	if component.DependencyClosure, component.PendingFields["dependencyClosure"], err = dependencies(values, "dependencyClosure"); err != nil {
		return Component{}, err
	}
	if component.Patches, component.PendingFields["patches"], err = patches(values, "patches"); err != nil {
		return Component{}, err
	}
	if component.OfficialSupportSources, err = optionalStringList(values, "officialSupportSources"); err != nil {
		return Component{}, err
	}
	if component.ConformanceFixtures, component.PendingFields["conformanceFixtures"], err = stringList(values, "conformanceFixtures"); err != nil {
		return Component{}, err
	}
	if component.SourceArchiveSHA256, component.PendingFields["sourceArchiveSHA256"], err = optionalPendingString(values, "sourceArchiveSHA256"); err != nil {
		return Component{}, err
	}
	component.SpecialLicenseADR, _, err = optionalPendingString(values, "specialLicenseADR")
	if err != nil {
		return Component{}, err
	}
	if component.DependencyClosureVerified, err = optionalBool(values, "dependencyClosureVerified"); err != nil {
		return Component{}, err
	}
	if component.RequiredFor1_0, err = optionalBool(values, "requiredFor1_0"); err != nil {
		return Component{}, err
	}
	if component.SourceSnapshot, err = optionalBool(values, "sourceSnapshot"); err != nil {
		return Component{}, err
	}
	if component.ForkPolicy, component.PendingFields["forkPolicy"], err = pendingValue(values, "forkPolicy"); err != nil {
		return Component{}, err
	}
	return component, nil
}

func validateComponent(component Component) error {
	if component.State != "candidate" && component.State != "qualified" {
		return fmt.Errorf("state must be candidate or qualified")
	}
	if isFloating(component.Version) {
		return fmt.Errorf("floating version %q is not allowed", component.Version)
	}
	if component.Version != "pending" && !isExactVersion(component.Version) {
		return fmt.Errorf("version %q must be an exact version or immutable commit", component.Version)
	}
	if component.Source != "pending" {
		parsed, err := url.Parse(component.Source)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "oci") {
			return fmt.Errorf("source must be an HTTPS or OCI URL")
		}
	}
	if component.License != "pending" {
		if _, known := knownLicenses[component.License]; !known {
			return fmt.Errorf("unknown SPDX license %q", component.License)
		}
	}
	if isAGPL(component.License) && (component.LinkageMode == "static" || component.LinkageMode == "copied" || component.ReuseMode == "static-link" || component.ReuseMode == "code-copy" || component.ReuseMode == "vendor" || component.ReuseMode == "fork") {
		return fmt.Errorf("AGPL component %q cannot be statically linked or copied into platform code", component.Name)
	}
	if component.State == "candidate" {
		return nil
	}
	if component.Version == "pending" {
		return fmt.Errorf("qualified component must lock an exact version")
	}
	if component.PendingFields["dependencyClosure"] {
		return fmt.Errorf("qualified component dependency closure must not be pending")
	}
	if component.PendingFields["fileLicenses"] {
		return fmt.Errorf("qualified component file-level license inventory must not be pending")
	}
	if component.Source == "pending" {
		return fmt.Errorf("qualified component must declare its source URL")
	}
	if !commitPattern.MatchString(component.Commit) {
		return fmt.Errorf("qualified component must lock a full source commit")
	}
	if !digestPattern.MatchString(component.Digest) {
		return fmt.Errorf("qualified component must lock a sha256 digest")
	}
	if component.License == "pending" {
		return fmt.Errorf("qualified component must declare a known SPDX license")
	}
	if component.LinkageMode == "pending" {
		return fmt.Errorf("qualified component must lock its linkage or isolation mode")
	}
	if requiresSpecialLicenseADR(component.License) && (component.SpecialLicenseADR == "" || component.SpecialLicenseADR == "pending") {
		return fmt.Errorf("qualified GPL or AGPL component requires a specialized license ADR")
	}
	if !component.DependencyClosureVerified {
		return fmt.Errorf("qualified component must verify its dependency closure")
	}
	if len(component.Architectures) == 0 || component.PendingFields["architectures"] {
		return fmt.Errorf("qualified component must lock supported architectures")
	}
	if component.Owner == "pending" || component.POCReport == "pending" || component.ExitPlan == "pending" || component.PendingFields["conformanceFixtures"] {
		return fmt.Errorf("qualified component has pending owner, PoC, fixture, or exit-plan evidence")
	}
	if len(component.ConformanceFixtures) == 0 {
		return fmt.Errorf("qualified component must list conformance fixtures")
	}
	if component.SourceSnapshot || component.ReuseMode == "vendor" || component.ReuseMode == "fork" {
		if component.PendingFields["fileLicenses"] || len(component.FileLicenses) == 0 {
			return fmt.Errorf("source snapshot requires a file-level license inventory")
		}
		if component.SourceArchiveSHA256 == "" || component.PendingFields["sourceArchiveSHA256"] || !digestPattern.MatchString(component.SourceArchiveSHA256) {
			return fmt.Errorf("source snapshot requires a sha256 archive digest")
		}
		if component.PendingFields["importedPaths"] || len(component.ImportedPaths) == 0 {
			return fmt.Errorf("source snapshot requires selected imported paths")
		}
		if component.PendingFields["patches"] {
			return fmt.Errorf("source snapshot must explicitly list patches or an empty patch list")
		}
	}
	if component.ReuseMode == "fork" && !validForkPolicy(component.ForkPolicy) {
		return fmt.Errorf("fork policy must include update cadence, security SLA, rollback version, and patches")
	}
	for _, file := range component.FileLicenses {
		if file.Path == "" || path.IsAbs(file.Path) || path.Clean(file.Path) != file.Path || file.Path == ".." || strings.HasPrefix(file.Path, "../") {
			return fmt.Errorf("file-level license path %q must be a clean relative path", file.Path)
		}
		if _, known := knownLicenses[file.License]; !known {
			return fmt.Errorf("file %q has unknown SPDX license %q", file.Path, file.License)
		}
		if !digestPattern.MatchString(file.Digest) {
			return fmt.Errorf("file %q requires a sha256 digest", file.Path)
		}
	}
	for _, dependency := range component.DependencyClosure {
		if dependency.Name == "" || !isExactVersion(dependency.Version) || dependency.Source == "" || !commitPattern.MatchString(dependency.Commit) || !digestPattern.MatchString(dependency.Digest) {
			return fmt.Errorf("dependency closure contains an unpinned dependency")
		}
		if _, known := knownLicenses[dependency.License]; !known {
			return fmt.Errorf("dependency %q has unknown SPDX license %q", dependency.Name, dependency.License)
		}
		parsed, err := url.Parse(dependency.Source)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "oci") {
			return fmt.Errorf("dependency %q source must be an HTTPS or OCI URL", dependency.Name)
		}
		if requiresSpecialLicenseADR(dependency.License) && (component.SpecialLicenseADR == "" || component.SpecialLicenseADR == "pending") {
			return fmt.Errorf("dependency %q requires a specialized GPL or AGPL license ADR", dependency.Name)
		}
	}
	return nil
}

func parseKernel(values map[string]any) (FirstPartyKernel, error) {
	for key := range values {
		if key != "domain" && key != "implementation" && key != "reuseGate" && key != "pocReport" && key != "exitPlan" {
			return FirstPartyKernel{}, fmt.Errorf("unknown field %q", key)
		}
	}
	var kernel FirstPartyKernel
	var err error
	if kernel.Domain, err = requiredString(values, "domain"); err != nil {
		return FirstPartyKernel{}, err
	}
	if kernel.Implementation, err = requiredString(values, "implementation"); err != nil {
		return FirstPartyKernel{}, err
	}
	if kernel.ReuseGate, err = requiredString(values, "reuseGate"); err != nil {
		return FirstPartyKernel{}, err
	}
	kernel.POCReport, _ = optionalString(values, "pocReport")
	kernel.ExitPlan, _ = optionalString(values, "exitPlan")
	return kernel, nil
}

func validateKernel(kernel FirstPartyKernel) error {
	domain := strings.ToLower(kernel.Domain)
	if domain != "graph" && domain != "rca" && domain != "inspection" {
		return fmt.Errorf("unknown first-party kernel domain %q", kernel.Domain)
	}
	if kernel.Implementation == "self-developed" && (kernel.ReuseGate != "passed" || kernel.POCReport == "" || kernel.ExitPlan == "") {
		return fmt.Errorf("self-developed %s kernel requires a passed reuse gate, PoC report, and exit plan", domain)
	}
	return nil
}

func rejectAdditionalDocument(decoder *yaml.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("component catalog must contain one YAML document")
	} else if err != io.EOF {
		return fmt.Errorf("read component catalog: %w", err)
	}
	return nil
}

func requiredString(values map[string]any, name string) (string, error) {
	value, ok := values[name]
	if !ok {
		return "", fmt.Errorf("missing required field %q", name)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("field %q must be a non-empty string", name)
	}
	return text, nil
}

func optionalString(values map[string]any, name string) (string, bool) {
	value, ok := values[name]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func optionalBool(values map[string]any, name string) (bool, error) {
	value, ok := values[name]
	if !ok {
		return false, nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("field %q must be a boolean", name)
	}
	return boolean, nil
}

func optionalPendingString(values map[string]any, name string) (string, bool, error) {
	value, ok := values[name]
	if !ok {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("field %q must be a string", name)
	}
	return text, text == "pending", nil
}

func stringList(values map[string]any, name string) ([]string, bool, error) {
	value, ok := values[name]
	if !ok {
		return nil, false, fmt.Errorf("missing required field %q", name)
	}
	if text, ok := value.(string); ok {
		if text != "pending" {
			return nil, false, fmt.Errorf("field %q must be a list or pending", name)
		}
		return nil, true, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false, fmt.Errorf("field %q must be a list or pending", name)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, false, fmt.Errorf("field %q must contain non-empty strings", name)
		}
		result = append(result, text)
	}
	return result, false, nil
}

func optionalStringList(values map[string]any, name string) ([]string, error) {
	value, ok := values[name]
	if !ok {
		return nil, nil
	}
	items, isList := value.([]any)
	if !isList {
		if text, isText := value.(string); isText && text == "pending" {
			return nil, nil
		}
		return nil, fmt.Errorf("field %q must be a list or pending", name)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("field %q must contain non-empty strings", name)
		}
		result = append(result, text)
	}
	return result, nil
}

func stringOrFileLicenses(values map[string]any, name string) ([]FileLicense, bool, error) {
	value, ok := values[name]
	if !ok {
		return nil, false, fmt.Errorf("missing required field %q", name)
	}
	if text, ok := value.(string); ok {
		if text != "pending" {
			return nil, false, fmt.Errorf("field %q must be a file license list or pending", name)
		}
		return nil, true, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false, fmt.Errorf("field %q must be a file license list or pending", name)
	}
	result := make([]FileLicense, 0, len(items))
	for index, item := range items {
		mapping, ok := item.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s[%d] must be an object", name, index)
		}
		file := FileLicense{}
		var err error
		if file.Path, err = requiredString(mapping, "path"); err != nil {
			return nil, false, err
		}
		if file.License, err = requiredString(mapping, "license"); err != nil {
			return nil, false, err
		}
		if file.Digest, err = requiredString(mapping, "digest"); err != nil {
			return nil, false, err
		}
		result = append(result, file)
	}
	return result, false, nil
}

func dependencies(values map[string]any, name string) ([]Dependency, bool, error) {
	value, ok := values[name]
	if !ok {
		return nil, false, fmt.Errorf("missing required field %q", name)
	}
	if text, ok := value.(string); ok {
		if text != "pending" {
			return nil, false, fmt.Errorf("field %q must be a list or pending", name)
		}
		return nil, true, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false, fmt.Errorf("field %q must be a list or pending", name)
	}
	result := make([]Dependency, 0, len(items))
	for index, item := range items {
		mapping, ok := item.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s[%d] must be an object", name, index)
		}
		fields := []*string{}
		dependency := Dependency{}
		fields = append(fields, &dependency.Name, &dependency.Version, &dependency.Source, &dependency.Commit, &dependency.Digest, &dependency.License)
		for fieldIndex, field := range []string{"name", "version", "source", "commit", "digest", "license"} {
			value, err := requiredString(mapping, field)
			if err != nil {
				return nil, false, err
			}
			*fields[fieldIndex] = value
		}
		result = append(result, dependency)
	}
	return result, false, nil
}

func patches(values map[string]any, name string) ([]Patch, bool, error) {
	value, ok := values[name]
	if !ok {
		return nil, false, nil
	}
	if text, ok := value.(string); ok {
		if text != "pending" {
			return nil, false, fmt.Errorf("field %q must be a list or pending", name)
		}
		return nil, true, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false, fmt.Errorf("field %q must be a list or pending", name)
	}
	result := make([]Patch, 0, len(items))
	for index, item := range items {
		mapping, ok := item.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s[%d] must be an object", name, index)
		}
		patch := Patch{}
		var err error
		if patch.Path, err = requiredString(mapping, "path"); err != nil {
			return nil, false, err
		}
		if patch.Digest, err = requiredString(mapping, "digest"); err != nil {
			return nil, false, err
		}
		if !digestPattern.MatchString(patch.Digest) {
			return nil, false, fmt.Errorf("patch %q requires a sha256 digest", patch.Path)
		}
		result = append(result, patch)
	}
	return result, false, nil
}

func pendingValue(values map[string]any, name string) (any, bool, error) {
	value, ok := values[name]
	if !ok {
		return nil, false, fmt.Errorf("missing required field %q", name)
	}
	text, isText := value.(string)
	if isText {
		if text == "pending" || text == "not-applicable" {
			return text, text == "pending", nil
		}
		return nil, false, fmt.Errorf("field %q must be pending, not-applicable, or a policy object", name)
	}
	return value, false, nil
}

func validForkPolicy(value any) bool {
	policy, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, field := range []string{"updateCadence", "securitySLA", "rollbackVersion", "patches"} {
		if _, exists := policy[field]; !exists {
			return false
		}
	}
	return true
}

func isAGPL(license string) bool {
	return strings.HasPrefix(license, "AGPL-")
}

func requiresSpecialLicenseADR(license string) bool {
	return strings.HasPrefix(license, "GPL-") || isAGPL(license)
}

func isFloating(version string) bool {
	normalized := strings.ToLower(strings.TrimSpace(version))
	switch normalized {
	case "latest", "main", "master", "develop", "trunk", "stable", "nightly", "edge", "release", "dev", "unstable", "canary":
		return true
	default:
		return strings.ContainsAny(version, "*?") || strings.HasSuffix(normalized, ".x")
	}
}

func isExactVersion(version string) bool {
	return exactVersionPattern.MatchString(version) || exactReleasePattern.MatchString(version) || commitPattern.MatchString(version)
}
