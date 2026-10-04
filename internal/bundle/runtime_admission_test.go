package bundle

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	componentcatalog "ops-platform/bundle"
	"ops-platform/internal/supplychain"
)

func TestFirstPartyGoMaterialsCannotBypassSDKSourceAdmission(t *testing.T) {
	catalog, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"platform-api", "platform-worker", "opsctl"} {
		if err := validateCatalogAdmissionWithCatalog([]Material{{Name: name, Kind: "container-image", Architecture: "linux/arm64"}}, catalog); err == nil {
			t.Fatalf("%s bypassed required embedded Go source/license closure", name)
		}
	}
}

func TestSP06InvestigatorCannotOmitCorrespondingSource(t *testing.T) {
	catalog, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, ok := catalog.Component("holmesgpt")
	if !ok || component.State != "qualified" {
		t.Fatal("locked investigator reuse material not admitted")
	}
	image := Material{Name: "holmesgpt", Kind: "container-image", Version: component.Version, Architecture: "linux/arm64", Digest: component.Digest}
	if validateCatalogAdmissionWithCatalog([]Material{image}, catalog) == nil {
		t.Fatal("GPL investigator admitted without corresponding sources")
	}
	source := Material{Name: "holmesgpt-source", Kind: "source", Version: component.Version, Architecture: "linux/arm64", Digest: component.CorrespondingSourceBundleSHA256}
	if err := validateCatalogAdmissionWithCatalog([]Material{image, source}, catalog); err != nil {
		t.Fatal(err)
	}
	source.Digest = "sha256:" + strings.Repeat("0", 64)
	if validateCatalogAdmissionWithCatalog([]Material{image, source}, catalog) == nil {
		t.Fatal("different corresponding sources admitted")
	}
}

func TestRuntimeSourceBindsQualifiedSDKAndTargetArchitecture(t *testing.T) {
	catalog, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	sdk, _ := catalog.Component("opa-sdk")
	goMaterial := Material{Name: "platform-api", Kind: "container-image", Version: "1.0.0", Architecture: "linux/arm64"}
	source := Material{Name: "opa-sdk-source", Kind: "source", Version: sdk.Version, Digest: sdk.CorrespondingSourceBundleSHA256, Architecture: "linux/arm64"}
	if err := validateCatalogAdmissionWithCatalog([]Material{goMaterial, source}, catalog); err != nil {
		t.Fatal(err)
	}
	wrongTarget := source
	wrongTarget.Architecture = "linux/amd64"
	if err := validateCatalogAdmissionWithCatalog([]Material{wrongTarget}, catalog); err == nil {
		t.Fatal("SDK source alone admitted for an unqualified architecture")
	}
	evidence, err := componentcatalog.ComponentEvidence()
	if err != nil {
		t.Fatal(err)
	}
	raw := bytes.Replace(componentcatalog.ComponentCatalog(), []byte("  - name: opa-sdk\n    state: qualified"), []byte("  - name: opa-sdk\n    state: candidate"), 1)
	candidate, err := supplychain.LoadCatalogWithEvidence(bytes.NewReader(raw), evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCatalogAdmissionWithCatalog([]Material{goMaterial, source}, &candidate); err == nil {
		t.Fatal("candidate SDK admitted through first-party name allowlist")
	}
}

var sourceFixtureOnce sync.Once
var sourceFixtureBytes []byte
var sourceFixtureError error

// Import tests retain real API/Worker identities and the exact SDK source
// closure. Generate it once from authenticated, locally cached module zips;
// do not weaken the production source digest or depend on ignored artifacts.
func runtimeSourceFixture(t *testing.T) []byte {
	t.Helper()
	sourceFixtureOnce.Do(func() {
		var dir string
		dir, sourceFixtureError = os.MkdirTemp("", "ops-sdk-source-fixture-")
		if sourceFixtureError != nil {
			return
		}
		defer os.RemoveAll(dir)
		command := exec.Command("python3", "scripts/prepare-runtime-source.py", "--out", dir)
		command.Dir = filepath.Join("..", "..")
		if output, err := command.CombinedOutput(); err != nil {
			sourceFixtureError = &sourcePreparationError{output: string(output), cause: err}
			return
		}
		sourceFixtureBytes, sourceFixtureError = os.ReadFile(filepath.Join(dir, "runtime-go-source.tar"))
	})
	if sourceFixtureError != nil {
		t.Fatal(sourceFixtureError)
	}
	return sourceFixtureBytes
}

type sourcePreparationError struct {
	output string
	cause  error
}

func (err *sourcePreparationError) Error() string { return err.output + err.cause.Error() }

func TestSP05WorkerCannotOmitAnalyzerCorrespondingSource(t *testing.T) {
	catalog, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	sdk, _ := catalog.Component("opa-sdk")
	materials := []Material{{Name: "platform-worker", Kind: "container-image", Version: "1.1.0", Architecture: "linux/arm64"}, {Name: "opa-sdk-source", Kind: "source", Version: sdk.Version, Digest: sdk.CorrespondingSourceBundleSHA256, Architecture: "linux/arm64"}}
	if err := validateCatalogAdmissionWithCatalog(materials, catalog); err == nil {
		t.Fatal("SP05 Worker admitted without unchanged CLI's complete licensed source closure")
	}
}

func TestSP05WorkerSourceAdmissionBindsExactCLIClosure(t *testing.T) {
	catalog, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	sdk, _ := catalog.Component("opa-sdk")
	cli, _ := catalog.Component("k8sgpt")
	materials := []Material{{Name: "platform-worker", Kind: "container-image", Version: "1.1.0", Architecture: "linux/arm64"}, {Name: "opa-sdk-source", Kind: "source", Version: sdk.Version, Digest: sdk.CorrespondingSourceBundleSHA256, Architecture: "linux/arm64"}, {Name: "k8sgpt-source", Kind: "source", Version: cli.Version, Digest: cli.CorrespondingSourceBundleSHA256, Architecture: "linux/arm64"}}
	if err := validateCatalogAdmissionWithCatalog(materials, catalog); err != nil {
		t.Fatal(err)
	}
	materials[2].Digest = "sha256:" + strings.Repeat("1", 64)
	if err := validateCatalogAdmissionWithCatalog(materials, catalog); err == nil {
		t.Fatal("changed corresponding-source bytes admitted")
	}
}
