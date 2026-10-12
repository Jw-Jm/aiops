package bundle

import (
	"testing"
)

func TestRuntimeCatalogSelectsIndependentAMD64Source(t *testing.T) {
	arm, err := loadEmbeddedCatalog("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	amd, err := loadEmbeddedCatalog("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := arm.Component("opa-sdk")
	b, _ := amd.Component("opa-sdk")
	if a.Digest != b.Digest || a.Version != b.Version || a.CorrespondingSourceBundleSHA256 == b.CorrespondingSourceBundleSHA256 {
		t.Fatal("architecture selection changed upstream identity or borrowed source")
	}
	native := []Material{{Name: "platform-api", Kind: "container-image", Version: "1.0.0", Architecture: "linux/amd64"}, {Name: "opa-sdk-source", Kind: "source", Version: b.Version, Architecture: "linux/amd64", Digest: b.CorrespondingSourceBundleSHA256}}
	if err := validateCatalogAdmissionWithCatalog(native, amd); err != nil {
		t.Fatal(err)
	}
	borrowed := append([]Material(nil), native...)
	borrowed[1].Digest = a.CorrespondingSourceBundleSHA256
	if validateCatalogAdmissionWithCatalog(borrowed, amd) == nil {
		t.Fatal("AMD64 accepted ARM64 source identity")
	}
	if validateCatalogAdmissionWithCatalog(native, arm) == nil {
		t.Fatal("ARM64 admission accepted AMD64 materials")
	}
	if _, err := loadEmbeddedCatalog("linux/riscv64"); err == nil {
		t.Fatal("unsupported target inherited another catalog")
	}
}

func TestAMD64SDKCatalogDoesNotQualifyDeferredNativeImages(t *testing.T) {
	catalog, err := loadEmbeddedCatalog("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	component, _ := catalog.Component("kubevirt")
	materials := []Material{{Name: "kubevirt", Kind: "container-image", Version: component.Version, Architecture: "linux/amd64", Digest: component.Digest}, {Name: "kubevirt-source", Kind: "source", Version: component.Version, Architecture: "linux/amd64", Digest: component.CorrespondingSourceBundleSHA256}}
	if validateCatalogAdmissionWithCatalog(materials, catalog) == nil {
		t.Fatal("SDK target admission promoted deferred KubeVirt without native qualification")
	}
}

func TestNativeRunnerAdmissionRejectsBorrowedArchitectureClosure(t *testing.T) {
	amd, err := loadEmbeddedCatalog("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	arm, err := loadEmbeddedCatalog("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := amd.Component("command-runner")
	b, _ := arm.Component("command-runner")
	materials := []Material{{Name: "command-runner", Kind: "container-image", Version: a.Version, Architecture: "linux/amd64", Digest: a.Digest}, {Name: "command-runner-source", Kind: "source", Version: a.Version, Architecture: "linux/amd64", Digest: a.CorrespondingSourceBundleSHA256}}
	sdk, _ := amd.Component("opa-sdk")
	materials = append(materials, Material{Name: "opa-sdk-source", Kind: "source", Version: sdk.Version, Architecture: "linux/amd64", Digest: sdk.CorrespondingSourceBundleSHA256})
	if err := validateCatalogAdmissionWithCatalog(materials, amd); err != nil {
		t.Fatal(err)
	}
	materials[1].Digest = b.CorrespondingSourceBundleSHA256
	if validateCatalogAdmissionWithCatalog(materials, amd) == nil {
		t.Fatal("native runner accepted historical ARM source closure")
	}
	if a.Digest == b.Digest || a.CorrespondingSourceBundleSHA256 == b.CorrespondingSourceBundleSHA256 {
		t.Fatal("native runner catalog borrowed historical image/source identities")
	}
}
