package bundle

import (
	"archive/tar"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ops-platform/internal/profile"
	"ops-platform/internal/supplychain"
)

type recordingImporter struct{ calls []string }

func (r *recordingImporter) Probe(context.Context, profile.ResolvedProfile) (string, error) {
	r.calls = append(r.calls, "probe")
	return "orbstack_shared_store", nil
}
func (r *recordingImporter) Import(context.Context, ImageArtifact) error {
	r.calls = append(r.calls, "import")
	return nil
}
func (r *recordingImporter) Verify(context.Context, ImageArtifact) error {
	r.calls = append(r.calls, "verify")
	return nil
}

func TestImportRejectsTamperedBundleBeforeRuntimeProbe(t *testing.T) {
	manifest, trust, _ := signedFixture(t, nil)
	manifest.Signature[0] ^= 1
	runtime := &recordingImporter{}
	if _, err := Import(context.Background(), manifest, trust, importProfile(), runtime); err == nil {
		t.Fatal("tampered signature accepted")
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("runtime called before verification: %v", runtime.calls)
	}
}

func TestImportRejectsCandidateProfileBeforeRuntimeProbe(t *testing.T) {
	manifest, trust, _ := signedFixture(t, nil)
	p := importProfile()
	p.Installable = false
	runtime := &recordingImporter{}
	if _, err := Import(context.Background(), manifest, trust, p, runtime); err == nil || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("candidate profile error = %v", err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("runtime called for candidate profile: %v", runtime.calls)
	}
}

func TestImportRejectsInvalidOCIClosureBeforeRuntimeProbe(t *testing.T) {
	manifest, trust, _ := signedFixture(t, nil)
	runtime := &recordingImporter{}
	if _, err := Import(context.Background(), manifest, trust, importProfile(), runtime); err == nil || !strings.Contains(err.Error(), "OCI") {
		t.Fatalf("invalid OCI archive error = %v", err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("runtime called before all OCI material was checked: %v", runtime.calls)
	}
}

func importProfile() profile.ResolvedProfile {
	return profile.ResolvedProfile{
		SchemaVersion: 1, Kind: "resolved", ProfileID: "core-test", Environment: "development",
		Architecture: "arm64", Selected: "core", Installable: true,
		Kubernetes: profile.KubernetesDiscovery{Distribution: "orbstack", Context: "orbstack", ServerVersion: "v1.35.6+orb1", Architecture: "arm64", ClusterUID: "cluster-1"},
		Runtime:    profile.RuntimeInput{PublicEgress: "deny", ImageImporter: "orbstack_shared_store"},
		Components: map[string]profile.ResolvedComponent{},
	}
}

func TestGenericContainerdCoreProfileAdmission(t *testing.T) {
	p := importProfile()
	p.Kubernetes.Distribution = "kubernetes"
	p.Kubernetes.Context = "isolated-standard-k8s"
	p.Runtime.ImageImporter = "containerd_ctr"
	if err := validateInstallProfile(p, "linux/arm64"); err != nil {
		t.Fatalf("standard Kubernetes containerd core rejected: %v", err)
	}
	for _, mutate := range []func(*profile.ResolvedProfile){
		func(p *profile.ResolvedProfile) { p.Runtime.ImageImporter = "internal_registry" },
		func(p *profile.ResolvedProfile) { p.Runtime.PublicEgress = "allow" },
		func(p *profile.ResolvedProfile) { p.Architecture = "amd64" },
		func(p *profile.ResolvedProfile) { p.Installable = false },
	} {
		bad := p
		mutate(&bad)
		if validateInstallProfile(bad, "linux/arm64") == nil {
			t.Fatal("unsupported driver, open egress, mismatched architecture or candidate accepted")
		}
	}
}

func TestPlanFallbackKeepsObservedExternalVersionIndependent(t *testing.T) {
	archive, digest := ociFixture(t, false, false)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fallback.tar"), archive, 0600); err != nil {
		t.Fatal(err)
	}
	// This isolated planning fixture is deliberately candidate. Real Import
	// rejects candidates in prepare; no release qualification is simulated here.
	catalog, err := supplychain.LoadCatalog(strings.NewReader(fmt.Sprintf(`
schemaVersion: 1
components:
  - name: victoria-metrics
    state: candidate
    version: 1.0.0
    source: https://example.org/fixture
    commit: pending
    digest: %s
    license: Apache-2.0
    fileLicenses: pending
    architectures: [linux/arm64]
    importedPaths: pending
    dependencyClosure: pending
    patches: pending
    conformanceFixtures: pending
    forkPolicy: pending
    usage: fixture
    reuseMode: external-service
    linkageMode: pending
    owner: pending
    pocReport: pending
    exitPlan: pending
`, digest)))
	if err != nil {
		t.Fatal(err)
	}
	v := &verifiedPayload{path: dir, catalog: &catalog, manifest: Manifest{Architecture: "linux/arm64", Materials: []Material{{Name: "victoria-metrics", Kind: "container-image", Version: "1.0.0", PayloadRef: "fallback.tar"}}}}
	p := importProfile()
	observed := profile.ResolvedComponent{Mode: "external", Version: "2.0.0", Digest: "sha256:" + strings.Repeat("b", 64), Image: "example.org/existing@sha256:" + strings.Repeat("b", 64), Endpoint: "http://existing", AdmissionState: "qualified"}
	p.Components["victoriaMetrics"] = observed
	images, err := planImages(v, p)
	if err != nil || len(images) != 0 {
		t.Fatalf("fallback plan=%v error=%v", images, err)
	}
	if p.Components["victoriaMetrics"].Image != observed.Image {
		t.Fatal("observed external service was overwritten")
	}
	broken, _ := ociFixture(t, true, false)
	if err := os.WriteFile(filepath.Join(dir, "fallback.tar"), broken, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := planImages(v, p); err == nil || !strings.Contains(err.Error(), "layer") {
		t.Fatalf("external fallback with a missing layer was skipped: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fallback.tar"), archive, 0600); err != nil {
		t.Fatal(err)
	}
	observed.Mode = "bundled"
	p.Components["victoriaMetrics"] = observed
	if _, err := planImages(v, p); err == nil {
		t.Fatal("bundled profile accepted a different image")
	}
	v.manifest.Materials[0].Version = "2.0.0"
	if _, err := planImages(v, p); err == nil || !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("catalog mismatch error=%v", err)
	}
}

func signedOCIImportFixture(t *testing.T, missingWorkerLayer bool) (Manifest, TrustRoot) {
	t.Helper()
	entries := []tarEntry{}
	files := []PayloadFile{}
	materials := []Material{}
	add := func(name string, content []byte, kind string) {
		entries = append(entries, tarEntry{header: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(content))}, body: content})
		files = append(files, PayloadFile{Path: name, Digest: fixtureDigest(string(content)), Size: int64(len(content)), Kind: kind})
	}
	for _, name := range []string{"platform-api", "platform-worker"} {
		archive, _ := ociFixture(t, name == "platform-worker" && missingWorkerLayer, false)
		add("oci/"+name+".tar", archive, "oci")
		add("sbom/"+name+".json", []byte("{}"), "sbom")
		add("licenses/"+name+".txt", []byte("fixture license"), "license")
		materials = append(materials, Material{Name: name, Kind: "container-image", Version: "1.0.0", Digest: fixtureDigest(string(archive)), Architecture: "linux/arm64",
			PayloadRef: "oci/" + name + ".tar", SBOMRef: "sbom/" + name + ".json", LicenseRef: "licenses/" + name + ".txt", InstallAfter: []string{}})
	}
	sdk, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, _ := sdk.Component("opa-sdk")
	source := runtimeSourceFixture(t)
	add("sources/opa-sdk-source.tar", source, "source")
	add("sbom/opa-sdk-source.json", []byte(`{"scope":"selected Go runtime source and original notices"}`), "sbom")
	add("licenses/opa-sdk-source.txt", []byte("Applicable original notices accompany selected sources in the archive."), "license")
	materials = append(materials, Material{Name: "opa-sdk-source", Kind: "source", Version: component.Version, Architecture: "linux/arm64", Digest: component.CorrespondingSourceBundleSHA256,
		PayloadRef: "sources/opa-sdk-source.tar", SBOMRef: "sbom/opa-sdk-source.json", LicenseRef: "licenses/opa-sdk-source.txt", InstallAfter: []string{}})
	payload := compressZstd(t, makeTar(t, entries))
	m := Manifest{SchemaVersion: 1, BundleID: "import-test", PlatformVersion: "1.0.0", Architecture: "linux/arm64", Materials: materials,
		Payload: Payload{File: "payload.tar.zst", Digest: fixtureDigest(string(payload)), MaxFiles: 20, MaxBytes: 128 << 20, MaxCompressedBytes: 128 << 20, Files: files}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	m.CanonicalJSON, err = CanonicalizeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m.Signature = ed25519.Sign(key, m.CanonicalJSON)
	m.PayloadPath = filepath.Join(t.TempDir(), "payload.tar.zst")
	if err := os.WriteFile(m.PayloadPath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	return m, trustRootFor(t, key.Public().(ed25519.PublicKey))
}

func TestImportValidatesEveryLayerBeforeAnyRuntimeCall(t *testing.T) {
	m, trust := signedOCIImportFixture(t, true)
	runtime := &recordingImporter{}
	if _, err := Import(context.Background(), m, trust, importProfile(), runtime); err == nil || !strings.Contains(err.Error(), "layer") {
		t.Fatalf("missing layer error = %v", err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("incomplete second image caused runtime effects: %v", runtime.calls)
	}
}

func TestPlanImportReturnsEveryImageWithoutRuntimeSideEffects(t *testing.T) {
	m, trust := signedOCIImportFixture(t, false)
	images, err := PlanImport(context.Background(), m, trust, importProfile())
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 {
		t.Fatalf("planned images=%+v", images)
	}
	for _, image := range images {
		if image.Path != "" || image.Digest == "" || !strings.HasSuffix(image.Reference, "@"+image.Digest) {
			t.Fatalf("incomplete image plan: %+v", image)
		}
	}
}

func TestImportVerifiedOCIArchivesWithoutPull(t *testing.T) {
	m, trust := signedOCIImportFixture(t, false)
	runtime := &recordingImporter{}
	report, err := Import(context.Background(), m, trust, importProfile(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Imported) != 2 || strings.Join(runtime.calls, ",") != "probe,import,verify,import,verify" {
		t.Fatalf("report=%+v calls=%v", report, runtime.calls)
	}
}
