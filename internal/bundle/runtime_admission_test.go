package bundle

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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

func TestRuntimeSourceBindsQualifiedSDKAndTargetArchitecture(t *testing.T) {
	catalog, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	sdk, _ := catalog.Component("opa-sdk")
	goMaterial := Material{Name: "platform-api", Kind: "container-image", Architecture: "linux/arm64"}
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
