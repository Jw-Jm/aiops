package supplychain_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/supplychain"
)

const qualifiedCatalog = `
schemaVersion: 1
components:
  - name: demo
    state: qualified
    version: 1.2.3
    source: https://example.org/demo
    commit: 0123456789abcdef0123456789abcdef01234567
    digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    license: Apache-2.0
    specialLicenseADR: pending
    fileLicenses: []
    sourceSnapshot: false
    architectures: [linux/arm64]
    usage: contract-test
    reuseMode: direct-dependency
    linkageMode: dynamic
    importedPaths: []
    dependencyClosure: []
    dependencyClosureVerified: true
    conformanceFixtures: [test/fixtures/demo.json]
    forkPolicy: not-applicable
    owner: platform-team
    pocReport: reports/demo-poc.md
    exitPlan: replace-through-adapter
firstPartyKernels: []
`

func TestLoadCatalogRejectsUnpinnedOrUnverifiedQualifiedComponents(t *testing.T) {
	tests := []struct {
		name      string
		wantError string
		mutate    func(string) string
	}{
		{
			name:      "latest version",
			wantError: "floating version",
			mutate: func(document string) string {
				return strings.Replace(document, "version: 1.2.3", "version: latest", 1)
			},
		},
		{
			name:      "develop branch version",
			wantError: "floating version",
			mutate: func(document string) string {
				return strings.Replace(document, "version: 1.2.3", "version: develop", 1)
			},
		},
		{
			name:      "pending qualified version",
			wantError: "qualified component must lock an exact version",
			mutate: func(document string) string {
				return strings.Replace(document, "version: 1.2.3", "version: pending", 1)
			},
		},
		{
			name:      "pending dependency closure",
			wantError: "dependency closure must not be pending",
			mutate: func(document string) string {
				return strings.Replace(document, "dependencyClosure: []", "dependencyClosure: pending", 1)
			},
		},
		{
			name:      "pending file license inventory without source snapshot",
			wantError: "file-level license inventory must not be pending",
			mutate: func(document string) string {
				return strings.Replace(document, "fileLicenses: []", "fileLicenses: pending", 1)
			},
		},
		{
			name:      "empty digest",
			wantError: "field \"digest\" must not be empty",
			mutate: func(document string) string {
				return strings.Replace(document, "digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "digest: ''", 1)
			},
		},
		{
			name:      "unknown license",
			wantError: "unknown SPDX license",
			mutate: func(document string) string {
				return strings.Replace(document, "license: Apache-2.0", "license: UNKNOWN-LICENSE", 1)
			},
		},
		{
			name:      "missing source URL",
			wantError: "field \"source\" must not be empty",
			mutate: func(document string) string {
				return strings.Replace(document, "source: https://example.org/demo", "source: ''", 1)
			},
		},
		{
			name:      "missing upstream commit",
			wantError: "field \"commit\" must not be empty",
			mutate: func(document string) string {
				return strings.Replace(document, "commit: 0123456789abcdef0123456789abcdef01234567", "commit: ''", 1)
			},
		},
		{
			name:      "source snapshot without file license inventory",
			wantError: "file-level license inventory",
			mutate: func(document string) string {
				document = strings.Replace(document, "fileLicenses: []", "fileLicenses: []", 1)
				document = strings.Replace(document, "sourceSnapshot: false", "sourceSnapshot: true", 1)
				return strings.Replace(document, "reuseMode: direct-dependency", "reuseMode: vendor", 1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := supplychain.LoadCatalog(strings.NewReader(test.mutate(qualifiedCatalog))); err == nil {
				t.Fatal("LoadCatalog accepted an invalid qualified component")
			} else if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("LoadCatalog error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestLoadCatalogAcceptsFullyLockedQualifiedComponent(t *testing.T) {
	catalog, err := supplychain.LoadCatalogWithEvidence(strings.NewReader(qualifiedCatalog), validQualifiedEvidence())
	if err != nil {
		t.Fatalf("LoadCatalog(qualified): %v", err)
	}
	if err := catalog.ValidateBundle([]string{"demo"}); err != nil {
		t.Fatalf("ValidateBundle(qualified): %v", err)
	}
}

func TestLoadCatalogAcceptsExactPrereleaseVersionContainingX(t *testing.T) {
	document := strings.Replace(qualifiedCatalog, "version: 1.2.3", "version: 1.2.3-experimental", 1)
	if _, err := supplychain.LoadCatalogWithEvidence(strings.NewReader(document), validQualifiedEvidence()); err != nil {
		t.Fatalf("LoadCatalog(exact prerelease version): %v", err)
	}
}

func TestLoadCatalogWithEvidenceRejectsMissingQualifiedEvidenceFiles(t *testing.T) {
	tests := []struct {
		name      string
		files     fstest.MapFS
		wantError string
	}{
		{
			name: "missing PoC report",
			files: fstest.MapFS{
				"test/fixtures/demo.json": &fstest.MapFile{Data: []byte(`{"fixture":"demo"}`)},
			},
			wantError: "PoC report",
		},
		{
			name: "missing conformance fixture",
			files: fstest.MapFS{
				"reports/demo-poc.md": &fstest.MapFile{Data: []byte("# Demo PoC\nResult: passed\n")},
			},
			wantError: "conformance fixture",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := supplychain.LoadCatalogWithEvidence(strings.NewReader(qualifiedCatalog), test.files); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("LoadCatalogWithEvidence error = %v, want %q rejection", err, test.wantError)
			}
		})
	}
}

func TestLoadCatalogWithEvidenceRejectsEmptyEvidenceContent(t *testing.T) {
	files := validQualifiedEvidence()
	files["reports/demo-poc.md"].Data = []byte(" \n\t")
	if _, err := supplychain.LoadCatalogWithEvidence(strings.NewReader(qualifiedCatalog), files); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("LoadCatalogWithEvidence error = %v, want empty evidence rejection", err)
	}
}

func TestLoadCatalogWithEvidenceRejectsMalformedFixtureContent(t *testing.T) {
	files := validQualifiedEvidence()
	files["test/fixtures/demo.json"].Data = []byte(`{"fixture":`)
	if _, err := supplychain.LoadCatalogWithEvidence(strings.NewReader(qualifiedCatalog), files); err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("LoadCatalogWithEvidence error = %v, want malformed JSON fixture rejection", err)
	}
}

func TestLoadCatalogWithEvidenceRejectsTraversalEvidencePath(t *testing.T) {
	document := strings.Replace(qualifiedCatalog, "pocReport: reports/demo-poc.md", "pocReport: ../reports/demo-poc.md", 1)
	if _, err := supplychain.LoadCatalogWithEvidence(strings.NewReader(document), validQualifiedEvidence()); err == nil || !strings.Contains(err.Error(), "clean relative path") {
		t.Fatalf("LoadCatalogWithEvidence error = %v, want traversal path rejection", err)
	}
}

func TestLoadCatalogWithoutEvidenceContextRejectsQualifiedComponent(t *testing.T) {
	if _, err := supplychain.LoadCatalog(strings.NewReader(qualifiedCatalog)); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("LoadCatalog error = %v, want evidence resolver requirement", err)
	}
}

func validQualifiedEvidence() fstest.MapFS {
	return fstest.MapFS{
		"reports/demo-poc.md":     &fstest.MapFile{Data: []byte("# Demo PoC\nEnvironment: test\nResult: passed\n")},
		"test/fixtures/demo.json": &fstest.MapFile{Data: []byte(`{"fixture":"demo","expected":"pass"}`)},
	}
}

func TestQualifiedCopyleftRequiresSpecializedLicenseADR(t *testing.T) {
	document := strings.Replace(qualifiedCatalog, "license: Apache-2.0", "license: GPL-3.0-only", 1)
	if _, err := supplychain.LoadCatalog(strings.NewReader(document)); err == nil || !strings.Contains(err.Error(), "specialized license ADR") {
		t.Fatalf("LoadCatalog error = %v, want specialized GPL/AGPL license ADR rejection", err)
	}
}

func TestValidateBundleRejectsCandidateComponents(t *testing.T) {
	document := strings.Replace(qualifiedCatalog, "state: qualified", "state: candidate", 1)
	catalog, err := supplychain.LoadCatalog(strings.NewReader(document))
	if err != nil {
		t.Fatalf("LoadCatalog(candidate): %v", err)
	}
	if err := catalog.ValidateBundle([]string{"demo"}); err == nil {
		t.Fatal("ValidateBundle accepted a candidate component")
	}
}

func TestLoadCatalogRejectsAGPLStaticLinkOrCodeCopy(t *testing.T) {
	tests := []struct {
		name        string
		reuseMode   string
		linkageMode string
	}{
		{name: "static link", reuseMode: "static-link", linkageMode: "static"},
		{name: "code copy", reuseMode: "code-copy", linkageMode: "copied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := strings.Replace(qualifiedCatalog, "license: Apache-2.0", "license: AGPL-3.0-only", 1)
			document = strings.Replace(document, "reuseMode: direct-dependency", "reuseMode: "+test.reuseMode, 1)
			document = strings.Replace(document, "linkageMode: dynamic", "linkageMode: "+test.linkageMode, 1)
			if _, err := supplychain.LoadCatalog(strings.NewReader(document)); err == nil {
				t.Fatal("LoadCatalog accepted an AGPL component combined into platform code")
			}
		})
	}
}

func TestValidateBundleRejectsDeepFlowApp(t *testing.T) {
	document := strings.Replace(qualifiedCatalog, "name: demo", "name: deepflow", 1)
	catalog, err := supplychain.LoadCatalogWithEvidence(strings.NewReader(document), validQualifiedEvidence())
	if err != nil {
		t.Fatalf("LoadCatalog(deepflow): %v", err)
	}
	if err := catalog.ValidateBundle([]string{"deepflow", "deepflow-app"}); err == nil || !strings.Contains(err.Error(), "deepflow-app") {
		t.Fatalf("ValidateBundle error = %v, want rejection naming deepflow-app", err)
	}
}

func TestLoadCatalogRejectsUnapprovedFirstPartyDiagnosticKernels(t *testing.T) {
	for _, domain := range []string{"graph", "rca", "inspection"} {
		t.Run(domain, func(t *testing.T) {
			document := strings.Replace(qualifiedCatalog, "firstPartyKernels: []", "firstPartyKernels:\n  - domain: "+domain+"\n    implementation: self-developed\n    reuseGate: pending", 1)
			if _, err := supplychain.LoadCatalog(strings.NewReader(document)); err == nil {
				t.Fatalf("LoadCatalog accepted a self-developed %s kernel before reuse gate", domain)
			}
		})
	}
}

func TestComponentCatalogCoversApprovedScopeWithoutQualifyingCandidates(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "bundle", "component-catalog.yaml"))
	if err != nil {
		t.Fatalf("open component catalog: %v", err)
	}
	defer file.Close()

	catalog, err := supplychain.LoadCatalog(file)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	required := []string{
		"victoria-metrics", "victoria-logs", "deepflow", "kubernetes", "kubevirt", "cdi",
		"holmesgpt", "k8sgpt", "coroot-community", "kubevirt-observability-controller",
		"kubevirt-must-gather", "node-problem-detector", "gofish", "ipmi-exporter",
		"smartctl-exporter", "kubernetes-ontology", "ariadne", "pyrca", "keep-community",
		"metal3-bmo", "keycloak", "openbao", "seaweedfs", "opa", "postgresql", "robusta", "medik8s",
	}
	for _, name := range required {
		if _, ok := catalog.Component(name); !ok {
			t.Errorf("component catalog is missing %q", name)
		}
	}
	for _, name := range []string{"robusta", "medik8s"} {
		component, ok := catalog.Component(name)
		if ok && (component.State != "candidate" || component.RequiredFor1_0) {
			t.Errorf("%s must remain a non-required 1.0 candidate", name)
		}
	}
	kubevirt, ok := catalog.Component("kubevirt")
	if ok && !containsText(kubevirt.OfficialSupportSources, "github.com/kubevirt/sig-release") {
		t.Error("KubeVirt catalog entry must cite the official Kubernetes support matrix")
	}
}

func containsText(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func TestLicensePolicyRequiresKnownLicensesAndRestrictsAGPLIntegration(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "bundle", "licenses-policy.yaml"))
	if err != nil {
		t.Fatalf("read license policy: %v", err)
	}
	var policy struct {
		UnknownLicenseAction string `yaml:"unknownLicenseAction"`
		Permissive           []struct {
			ID string `yaml:"id"`
		} `yaml:"permissiveLicenses"`
		Copyleft []struct {
			ID                 string `yaml:"id"`
			RequiresSpecialADR bool   `yaml:"requiresSpecialADR"`
			StaticLinkAllowed  *bool  `yaml:"staticLinkAllowed"`
			CodeCopyAllowed    *bool  `yaml:"codeCopyAllowed"`
		} `yaml:"copyleftLicenses"`
	}
	if err := yaml.Unmarshal(contents, &policy); err != nil {
		t.Fatalf("parse license policy: %v", err)
	}
	if policy.UnknownLicenseAction != "reject" {
		t.Fatalf("unknownLicenseAction = %q, want reject", policy.UnknownLicenseAction)
	}
	permissive := map[string]bool{}
	for _, license := range policy.Permissive {
		permissive[license.ID] = true
	}
	for _, license := range []string{"Apache-2.0", "MIT", "BSD-2-Clause", "BSD-3-Clause", "MPL-2.0"} {
		if !permissive[license] {
			t.Errorf("license policy is missing %s", license)
		}
	}
	for _, license := range policy.Copyleft {
		if license.ID == "AGPL-3.0-only" || license.ID == "AGPL-3.0-or-later" {
			if !license.RequiresSpecialADR || license.StaticLinkAllowed == nil || *license.StaticLinkAllowed || license.CodeCopyAllowed == nil || *license.CodeCopyAllowed {
				t.Errorf("AGPL policy must require a special ADR and forbid static link/code copy: %+v", license)
			}
		}
	}
	policyLicenses := make([]string, 0, len(policy.Permissive)+len(policy.Copyleft))
	for _, license := range policy.Permissive {
		policyLicenses = append(policyLicenses, license.ID)
	}
	for _, license := range policy.Copyleft {
		policyLicenses = append(policyLicenses, license.ID)
	}
	for _, license := range policyLicenses {
		document := strings.Replace(qualifiedCatalog, "state: qualified", "state: candidate", 1)
		document = strings.Replace(document, "license: Apache-2.0", "license: "+license, 1)
		if _, err := supplychain.LoadCatalog(strings.NewReader(document)); err != nil {
			t.Errorf("validator rejects license %s present in license policy: %v", license, err)
		}
	}
}
