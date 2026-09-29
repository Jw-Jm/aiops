package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Generated SDK rights follow both the exact Schema and generator sources.
// The rejected Interplex-dependent release is retained as rejected evidence.
func TestK8sGPTBufLicenseProvenance(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs/poc/k8sgpt-buf-license-provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var audit struct {
		Accepted     struct{ Version, Commit string } `json:"acceptedCLI"`
		SchemaSource struct {
			Commit         string       `json:"commit"`
			BSRCommit      string       `json:"bsrCommit"`
			Files          []lockedFile `json:"files"`
			License        lockedFile   `json:"license"`
			DependencyLock lockedFile   `json:"dependencyLock"`
			Imports        []lockedFile `json:"imports"`
		} `json:"schemaSource"`
		Resolved []struct {
			Module        string     `json:"module"`
			Version       string     `json:"version"`
			Archive       string     `json:"archiveSHA256"`
			License       string     `json:"license"`
			SchemaLicense lockedFile `json:"schemaLicense"`
			Generator     struct {
				Version  string     `json:"version"`
				Archive  string     `json:"archiveSHA256"`
				Evidence lockedFile `json:"licenseEvidence"`
			} `json:"generator"`
		} `json:"resolvedModules"`
		Rejected lockedFile `json:"rejectedCandidate"`
	}
	if err = json.Unmarshal(b, &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Accepted.Version != "v0.3.41" || audit.Accepted.Commit != "f071b32aa85d77b197cd2d0f9868294f7b55c5eb" || audit.SchemaSource.Commit != "327bb733aec02ff9ed8af18cdb212449e8c2b263" || audit.SchemaSource.BSRCommit != "7a91c862051546c892f89bdd97f5d31a" || len(audit.SchemaSource.Files) != 4 || len(audit.SchemaSource.Imports) != 2 {
		t.Fatal("accepted CLI or matching frozen Schema provenance changed")
	}
	for _, f := range append(append(audit.SchemaSource.Files, audit.SchemaSource.Imports...), audit.SchemaSource.License, audit.SchemaSource.DependencyLock, audit.Rejected) {
		if err := verifyLockedFile(root, f.Path, f.SHA256); err != nil {
			t.Fatal(err)
		}
	}
	closureBytes, err := os.ReadFile(filepath.Join(root, "test/fixtures/upstream-inspection/dependency-closures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var closure dependencyClosureDocument
	if err = json.Unmarshal(closureBytes, &closure); err != nil {
		t.Fatal(err)
	}
	modules := map[string]selectedDependencyModule{}
	for _, r := range closure.GeneratedFrom.Records {
		if r.ID != "k8sgpt-analyzer" {
			continue
		}
		if r.UnknownLicenses != 0 || !r.LicenseReviewComplete || !r.InventoryComplete || len(r.Modules) != 233 {
			t.Fatal("CLI license inventory is incomplete")
		}
		for _, m := range r.Modules {
			if strings.Contains(m.Path, "interplex-ai") || m.License == "NOASSERTION" {
				t.Fatal("unlicensed dependency cannot enter accepted CLI scope")
			}
			modules[m.Path] = m
			for _, f := range m.LicenseFiles {
				if err := verifyLockedFile(root, f.Path, f.SHA256); err != nil {
					t.Fatalf("%s license: %v", m.Path, err)
				}
			}
		}
	}
	if len(audit.Resolved) != 3 {
		t.Fatal("expected three licensed generated K8sGPT SDKs")
	}
	for _, r := range audit.Resolved {
		m, ok := modules[r.Module]
		if !ok || m.Version != r.Version || m.ArtifactSHA256 != r.Archive || m.License != r.License || len(m.LicenseFiles) != 2 {
			t.Fatalf("SDK provenance disagrees with CLI closure: %s", r.Module)
		}
		for _, f := range []lockedFile{r.SchemaLicense, r.Generator.Evidence} {
			if err := verifyLockedFile(root, f.Path, f.SHA256); err != nil {
				t.Fatal(err)
			}
		}
		if r.Generator.Version == "" || len(r.Generator.Archive) != 64 {
			t.Fatal("generator artifact is not frozen")
		}
	}
	rejected, err := os.ReadFile(filepath.Join(root, audit.Rejected.Path))
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Unresolved struct {
			License  string
			Modules  []string
			Required string `json:"requiredExternalEvidence"`
		}
	}
	if err = json.Unmarshal(rejected, &old); err != nil {
		t.Fatal(err)
	}
	if old.Unresolved.License != "NOASSERTION" || len(old.Unresolved.Modules) != 2 || old.Unresolved.Required == "" {
		t.Fatal("rejected release must retain unresolved Schema rights")
	}
	var frozen any
	if err := json.Unmarshal(rejected, &frozen); err != nil {
		t.Fatal(err)
	}
	verifyRetainedLicenseEvidence(t, root, frozen)
}

func verifyRetainedLicenseEvidence(t *testing.T, root string, value any) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		path, hasPath := v["path"].(string)
		sha, hasSHA := v["sha256"].(string)
		if hasPath && hasSHA {
			if err := verifyLockedFile(root, path, sha); err != nil {
				t.Fatal(err)
			}
		}
		for _, child := range v {
			verifyRetainedLicenseEvidence(t, root, child)
		}
	case []any:
		for _, child := range v {
			verifyRetainedLicenseEvidence(t, root, child)
		}
	}
}
