package supplychain

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNativePermissionReferencesRejectDifferentScopeAndMissingEvidence(t *testing.T) {
	var historical, investigator, debian, cargo, embedded, rustLibrary, metrics int
	for _, scope := range nativeLicenses {
		if strings.HasPrefix(scope.ID, "LicenseRef-Task27-Native-") {
			historical++
		}
		if scope.Component == "metrics-server" && strings.HasPrefix(scope.ID, "LicenseRef-PreSP07-Metrics-") {
			metrics++
		}
		if scope.Component == "holmesgpt" && strings.HasPrefix(scope.ID, "LicenseRef-SP06-Native-") {
			investigator++
			switch {
			case strings.HasPrefix(scope.DependencyName, "debian/"):
				debian++
			case strings.HasPrefix(scope.DependencyName, "cargo/"):
				cargo++
			case strings.HasPrefix(scope.DependencyName, "embedded/"):
				embedded++
			case scope.DependencyName == "rust/standard-library":
				rustLibrary++
			}
		}
	}
	if historical != 281 || debian != 87 || cargo != 767 || embedded != 8 || rustLibrary != 1 || metrics != 4 || investigator != debian+cargo+embedded+rustLibrary || len(nativeLicenses) != historical+investigator+metrics {
		t.Fatalf("compiled exact native license registry incomplete: historical=%d investigator=%d metrics=%d total=%d", historical, investigator, metrics, len(nativeLicenses))
	}
	var scope nativeLicenseScope
	for _, s := range nativeLicenses {
		scope = s
		break
	}
	component := Component{Name: scope.Component, Version: scope.ComponentVersion, Digest: scope.ImageDigest,
		CorrespondingSourceBundleSHA256: scope.CorrespondingSourceBundleSHA256, SpecialLicenseADR: scope.ADR,
		FileLicenses: []FileLicense{{Path: scope.NoticePath, Digest: scope.NoticeDigest, License: scope.ID}}}
	dependency := Dependency{Name: scope.DependencyName, Version: scope.Version, Source: scope.Source,
		SourceType: "archive", SourceArchiveSHA256: scope.SourceArchiveSHA256, License: scope.ID}
	if err := validateNativeDependency(component, dependency); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Dependency){
		"another package":     func(d *Dependency) { d.Name += "-other" },
		"another version":     func(d *Dependency) { d.Version += ".1" },
		"another source":      func(d *Dependency) { d.Source += "-other" },
		"another source hash": func(d *Dependency) { d.SourceArchiveSHA256 = "sha256:" + strings.Repeat("0", 64) },
		"fabricated choice":   func(d *Dependency) { d.License += " OR MIT" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := dependency
			mutate(&copy)
			if err := validateNativeDependency(component, copy); err == nil {
				t.Fatal("scoped permission reference accepted a different dependency")
			}
		})
	}
	for name, mutate := range map[string]func(*Component){
		"another component":       func(c *Component) { c.Name += "-other" },
		"another root version":    func(c *Component) { c.Version += ".1" },
		"another image":           func(c *Component) { c.Digest = "sha256:" + strings.Repeat("0", 64) },
		"another closure archive": func(c *Component) { c.CorrespondingSourceBundleSHA256 = "sha256:" + strings.Repeat("0", 64) },
		"absent source closure":   func(c *Component) { c.CorrespondingSourceBundleSHA256 = "" },
		"absent notice":           func(c *Component) { c.FileLicenses = nil },
		"different ADR":           func(c *Component) { c.SpecialLicenseADR = "another.md" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := component
			mutate(&copy)
			if err := validateNativeDependency(copy, dependency); err == nil {
				t.Fatal("scoped permission reference accepted a different component/source")
			}
		})
	}
	actual := os.DirFS("../../bundle/evidence")
	if err := validateNativeLicenseEvidence(component, actual); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{scope.NoticePath, scope.ADR} {
		notice, err := fs.ReadFile(actual, scope.NoticePath)
		if err != nil {
			t.Fatal(err)
		}
		adr, err := fs.ReadFile(actual, scope.ADR)
		if err != nil {
			t.Fatal(err)
		}
		evidence := fstest.MapFS{scope.NoticePath: &fstest.MapFile{Data: notice}, scope.ADR: &fstest.MapFile{Data: adr}}
		evidence[changed].Data = []byte("Status: Accepted\nchanged permission text\n")
		if err := validateNativeLicenseEvidence(component, evidence); err == nil {
			t.Fatal("changed notice/ADR accepted solely because a file exists")
		}
		delete(evidence, changed)
		if err := validateNativeLicenseEvidence(component, evidence); err == nil {
			t.Fatal("missing notice/ADR accepted")
		}
	}
	if validLicenseExpression("LicenseRef-unregistered") {
		t.Fatal("unregistered license reference accepted")
	}
}
