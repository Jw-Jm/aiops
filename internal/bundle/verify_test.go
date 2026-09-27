package bundle

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/klauspost/compress/zstd"
	"ops-platform/internal/contract"
)

type fixtureManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	BundleID      string            `json:"bundleId"`
	Platform      string            `json:"platformVersion"`
	Architecture  string            `json:"architecture"`
	Payload       fixturePayload    `json:"payload"`
	Materials     []fixtureMaterial `json:"materials"`
}

type fixturePayload struct {
	File               string        `json:"file"`
	Digest             string        `json:"digest"`
	MaxFiles           int           `json:"maxFiles"`
	MaxBytes           int64         `json:"maxBytes"`
	MaxCompressedBytes int64         `json:"maxCompressedBytes"`
	Files              []PayloadFile `json:"files"`
}

type fixtureMaterial struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Version      string   `json:"version"`
	Digest       string   `json:"digest"`
	Architecture string   `json:"architecture"`
	PayloadRef   string   `json:"payloadRef"`
	SBOMRef      string   `json:"sbomRef"`
	LicenseRef   string   `json:"licenseRef"`
	InstallAfter []string `json:"installAfter"`
}

func TestVerifyAcceptsSignedBundleWithExplicitTrustRoot(t *testing.T) {
	manifest, trustRoot, _ := signedFixture(t, nil)
	report, err := Verify(context.Background(), manifest, trustRoot)
	if err != nil {
		t.Fatalf("Verify(valid bundle): %v", err)
	}
	if !report.SignatureVerified || !report.PayloadDigestVerified {
		t.Fatalf("VerificationReport = %+v, want signature and payload verified", report)
	}
}

func TestVerifyAcceptsExactPrereleaseVersionContainingX(t *testing.T) {
	manifest, trustRoot, _ := signedFixture(t, func(manifest *fixtureManifest) {
		manifest.Materials[0].Version = "1.0.0-experimental"
	})
	if _, err := Verify(context.Background(), manifest, trustRoot); err != nil {
		t.Fatalf("Verify(exact prerelease version): %v", err)
	}
}

func TestVerifyRejectsTamperedPayloadByte(t *testing.T) {
	manifest, trustRoot, payloadPath := signedFixture(t, nil)
	if err := os.WriteFile(payloadPath, []byte("tampered payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), manifest, trustRoot); err == nil || !strings.Contains(err.Error(), "payload digest") {
		t.Fatalf("Verify(tampered payload) error = %v, want payload digest rejection", err)
	}
}

func TestVerifyRejectsReplacementTrustKey(t *testing.T) {
	manifest, _, _ := signedFixture(t, nil)
	_, replacementPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trustRoot := trustRootFor(t, replacementPrivateKey.Public().(ed25519.PublicKey))
	if _, err := Verify(context.Background(), manifest, trustRoot); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("Verify(replacement trust key) error = %v, want signature rejection", err)
	}
}

func TestReadTrustRootLoadsExplicitPEMPublicKey(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	trustRoot, err := ReadTrustRoot(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("ReadTrustRoot(PEM): %v", err)
	}
	fingerprint, err := PublicKeyFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if trustRoot.Fingerprint != fingerprint {
		t.Fatalf("fingerprint = %q, want %q", trustRoot.Fingerprint, fingerprint)
	}
}

func TestParseManifestRejectsNonCanonicalJSON(t *testing.T) {
	manifest, _, _ := signedFixture(t, nil)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, manifest.CanonicalJSON, "", "  "); err != nil {
		t.Fatal(err)
	}
	raw := pretty.Bytes()
	if _, err := ParseManifest(raw); err == nil || !strings.Contains(err.Error(), "RFC 8785") {
		t.Fatalf("ParseManifest(noncanonical JSON) error = %v, want RFC 8785 canonicalization rejection", err)
	}
}

func TestParseManifestAcceptsCanonicalJSON(t *testing.T) {
	manifest, _, _ := signedFixture(t, nil)
	parsed, err := ParseManifest(manifest.CanonicalJSON)
	if err != nil {
		t.Fatalf("ParseManifest(canonical JSON): %v", err)
	}
	if parsed.BundleID != "ops-platform-dev" {
		t.Fatalf("BundleID = %q, want ops-platform-dev", parsed.BundleID)
	}
}

func TestBundleLockSchemaRejectsFloatingVersion(t *testing.T) {
	for _, version := range []string{"latest", "1.x", "develop"} {
		t.Run(version, func(t *testing.T) {
			manifest, _, _ := signedFixture(t, func(manifest *fixtureManifest) {
				manifest.Materials[0].Version = version
			})
			if err := contract.Validate(ManifestSchemaID, manifest.CanonicalJSON); err == nil {
				t.Fatalf("Bundle Lock Schema accepted floating material version %q", version)
			}
		})
	}
}

func TestBundleLockSchemaRejectsMissingDigestAndUnsupportedArchitecture(t *testing.T) {
	manifest, _, _ := signedFixture(t, nil)
	var document map[string]any
	if err := json.Unmarshal(manifest.CanonicalJSON, &document); err != nil {
		t.Fatal(err)
	}
	t.Run("missing material digest", func(t *testing.T) {
		copy := cloneJSONMap(t, document)
		materials := copy["materials"].([]any)
		delete(materials[0].(map[string]any), "digest")
		encoded, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := CanonicalizeJSON(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if err := contract.Validate(ManifestSchemaID, canonical); err == nil {
			t.Fatal("Bundle Lock Schema accepted a missing material digest")
		}
	})
	t.Run("missing payload reference", func(t *testing.T) {
		copy := cloneJSONMap(t, document)
		materials := copy["materials"].([]any)
		delete(materials[0].(map[string]any), "payloadRef")
		encoded, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := CanonicalizeJSON(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if err := contract.Validate(ManifestSchemaID, canonical); err == nil {
			t.Fatal("Bundle Lock Schema accepted a missing material payload reference")
		}
	})
	t.Run("unsupported architecture", func(t *testing.T) {
		copy := cloneJSONMap(t, document)
		copy["architecture"] = "darwin/arm64"
		encoded, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := CanonicalizeJSON(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if err := contract.Validate(ManifestSchemaID, canonical); err == nil {
			t.Fatal("Bundle Lock Schema accepted an unsupported architecture")
		}
	})
}

func TestBundleLockSchemaRequiresMaterialPayloadReference(t *testing.T) {
	manifest, _, _ := signedFixture(t, nil)
	var document map[string]any
	if err := json.Unmarshal(manifest.CanonicalJSON, &document); err != nil {
		t.Fatal(err)
	}
	materials := document["materials"].([]any)
	delete(materials[0].(map[string]any), "payloadRef")
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalizeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate(ManifestSchemaID, canonical); err == nil {
		t.Fatal("Bundle Lock Schema accepted a material without a payloadRef")
	}
}

func TestValidateCatalogAdmissionAcceptsQualifiedComponentWithVerifiedEvidence(t *testing.T) {
	catalog := []byte(`
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
`)
	evidence := fstest.MapFS{
		"reports/demo-poc.md":     &fstest.MapFile{Data: []byte("PoC evidence")},
		"test/fixtures/demo.json": &fstest.MapFile{Data: []byte(`{"status":"pass"}`)},
	}

	if err := validateCatalogAdmissionWithEvidence([]Material{{Name: "demo"}}, catalog, evidence); err != nil {
		t.Fatalf("validateCatalogAdmissionWithEvidence(qualified): %v", err)
	}
}

func TestValidateCatalogAdmissionRejectsQualifiedComponentWithoutEvidence(t *testing.T) {
	catalog := []byte(`
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
`)

	err := validateCatalogAdmissionWithEvidence([]Material{{Name: "demo"}}, catalog, fstest.MapFS{})
	if err == nil || !strings.Contains(err.Error(), "cannot be resolved") {
		t.Fatalf("validateCatalogAdmissionWithEvidence(missing evidence) error = %v, want unresolved evidence rejection", err)
	}
}

func cloneJSONMap(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var copied map[string]any
	if err := json.Unmarshal(encoded, &copied); err != nil {
		t.Fatal(err)
	}
	return copied
}

func TestCanonicalizeJSONRejectsDuplicateNamesAndTrailingValues(t *testing.T) {
	for _, input := range []string{`{"schemaVersion":1,"schemaVersion":1}`, `{} {}`} {
		t.Run(input, func(t *testing.T) {
			if _, err := CanonicalizeJSON([]byte(input)); err == nil {
				t.Fatalf("CanonicalizeJSON(%s) accepted invalid RFC 8785 input", input)
			}
		})
	}
}

func TestVerifyRejectsManifestContractViolations(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*fixtureManifest)
		wantError string
	}{
		{
			name: "architecture mismatch",
			mutate: func(manifest *fixtureManifest) {
				manifest.Materials[0].Architecture = "linux/amd64"
			},
			wantError: "architecture",
		},
		{
			name: "floating version",
			mutate: func(manifest *fixtureManifest) {
				manifest.Materials[0].Version = "latest"
			},
			wantError: "floating version",
		},
		{
			name: "floating wildcard version",
			mutate: func(manifest *fixtureManifest) {
				manifest.Materials[0].Version = "1.x"
			},
			wantError: "floating version",
		},
		{
			name: "duplicate material name",
			mutate: func(manifest *fixtureManifest) {
				manifest.Materials = append(manifest.Materials, manifest.Materials[0])
			},
			wantError: "duplicate material",
		},
		{
			name: "install order cycle",
			mutate: func(manifest *fixtureManifest) {
				manifest.Materials[0].InstallAfter = []string{"worker"}
				manifest.Materials = append(manifest.Materials, fixtureMaterial{
					Name: "worker", Kind: "binary", Version: "1.0.0", Digest: fixtureDigest("worker"),
					Architecture: manifest.Architecture, PayloadRef: "binaries/worker", SBOMRef: "sbom/worker.json", LicenseRef: "licenses/worker.txt",
					InstallAfter: []string{manifest.Materials[0].Name},
				})
				manifest.Payload.Files = append(manifest.Payload.Files,
					PayloadFile{Path: "binaries/worker", Digest: fixtureDigest("worker"), Size: 6, Kind: "binary"},
					PayloadFile{Path: "sbom/worker.json", Digest: fixtureDigest("worker sbom"), Size: 10, Kind: "sbom"},
					PayloadFile{Path: "licenses/worker.txt", Digest: fixtureDigest("worker license"), Size: 10, Kind: "license"},
				)
			},
			wantError: "install order cycle",
		},
		{
			name: "missing SBOM reference",
			mutate: func(manifest *fixtureManifest) {
				manifest.Materials[0].SBOMRef = "sbom/missing.json"
			},
			wantError: "SBOM reference",
		},
		{
			name: "missing license reference",
			mutate: func(manifest *fixtureManifest) {
				manifest.Materials[0].LicenseRef = "licenses/missing.txt"
			},
			wantError: "license reference",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, trustRoot, _ := signedFixture(t, test.mutate)
			if _, err := Verify(context.Background(), manifest, trustRoot); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Verify() error = %v, want %q rejection", err, test.wantError)
			}
		})
	}
}

func TestSafeExtractRejectsEscapingAndSpecialEntries(t *testing.T) {
	tests := []struct {
		name   string
		header tar.Header
	}{
		{name: "absolute path", header: tar.Header{Name: "/tmp/escape", Typeflag: tar.TypeReg, Mode: 0o600, Size: 1}},
		{name: "parent traversal", header: tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o600, Size: 1}},
		{name: "symlink", header: tar.Header{Name: "profiles/link", Typeflag: tar.TypeSymlink, Linkname: "../../escape", Mode: 0o777}},
		{name: "hardlink", header: tar.Header{Name: "profiles/link", Typeflag: tar.TypeLink, Linkname: "../../escape", Mode: 0o600}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body []byte
			if test.header.Typeflag == tar.TypeReg {
				body = []byte("x")
			}
			archive := compressZstd(t, makeTar(t, []tarEntry{{header: test.header, body: body}}))
			destination := filepath.Join(t.TempDir(), "out")
			if _, err := safeExtract(context.Background(), bytes.NewReader(archive), destination, ExtractionLimits{MaxFiles: 10, MaxBytes: 1024}); err == nil {
				t.Fatal("SafeExtract accepted an escaping or special entry")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(destination), "escape")); !os.IsNotExist(err) {
				t.Fatalf("escaped path was created, stat error = %v", err)
			}
		})
	}
}

func TestSafeExtractEnforcesFileAndByteLimits(t *testing.T) {
	archive := compressZstd(t, makeTar(t, []tarEntry{
		{header: tar.Header{Name: "profiles/a.yaml", Typeflag: tar.TypeReg, Mode: 0o600, Size: 3}, body: []byte("abc")},
		{header: tar.Header{Name: "profiles/b.yaml", Typeflag: tar.TypeReg, Mode: 0o600, Size: 3}, body: []byte("def")},
	}))
	tests := []struct {
		name   string
		limits ExtractionLimits
	}{
		{name: "file count", limits: ExtractionLimits{MaxFiles: 1, MaxBytes: 10}},
		{name: "total bytes", limits: ExtractionLimits{MaxFiles: 10, MaxBytes: 5}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "out")
			if _, err := safeExtract(context.Background(), bytes.NewReader(archive), destination, test.limits); err == nil {
				t.Fatal("SafeExtract accepted an archive over its declared limits")
			}
		})
	}
}

func TestSafeExtractDoesNotCountDirectoriesAsFiles(t *testing.T) {
	archive := compressZstd(t, makeTar(t, []tarEntry{
		{header: tar.Header{Name: "profiles/nested/", Typeflag: tar.TypeDir, Mode: 0o700}},
		{header: tar.Header{Name: "profiles/nested/a.yaml", Typeflag: tar.TypeReg, Mode: 0o600, Size: 3}, body: []byte("abc")},
	}))
	destination := filepath.Join(t.TempDir(), "out")
	report, err := safeExtract(context.Background(), bytes.NewReader(archive), destination, ExtractionLimits{MaxFiles: 1, MaxBytes: 10})
	if err != nil {
		t.Fatalf("safeExtract with one file and one directory: %v", err)
	}
	if report.FileCount != 1 {
		t.Fatalf("FileCount = %d, want 1", report.FileCount)
	}
}

func TestVerifyRejectsPayloadFileInventoryMismatch(t *testing.T) {
	manifest, trustRoot, _ := signedFixture(t, func(manifest *fixtureManifest) {
		mismatchedDigest := fixtureDigest("different content")
		manifest.Payload.Files[0].Digest = mismatchedDigest
		manifest.Materials[0].Digest = mismatchedDigest
	})
	if _, err := Verify(context.Background(), manifest, trustRoot); err == nil || !strings.Contains(err.Error(), "signed inventory") {
		t.Fatalf("Verify(inventory mismatch) error = %v, want signed inventory rejection", err)
	}
}

func TestVerifyRejectsMaterialDigestNotBoundToItsPayloadFile(t *testing.T) {
	manifest, trustRoot, _ := signedFixture(t, func(manifest *fixtureManifest) {
		manifest.Materials[0].Digest = fixtureDigest("license")
	})
	if _, err := Verify(context.Background(), manifest, trustRoot); err == nil {
		t.Fatal("Verify accepted a material digest that identifies another payload file")
	}
}

func TestVerifyRejectsCandidateCatalogMaterial(t *testing.T) {
	manifest, trustRoot, _ := signedFixture(t, func(manifest *fixtureManifest) {
		manifest.Materials = append(manifest.Materials, fixtureMaterial{
			Name: "deepflow", Kind: "container-image", Version: "7.2.0", Digest: fixtureDigest("api"),
			Architecture: manifest.Architecture, PayloadRef: manifest.Materials[0].PayloadRef,
			SBOMRef: "sbom/platform-api.cdx.json", LicenseRef: "licenses/platform-api.txt",
			InstallAfter: []string{},
		})
	})
	if _, err := Verify(context.Background(), manifest, trustRoot); err == nil {
		t.Fatal("Verify accepted a Bundle containing a candidate catalog component")
	}
}

func TestVerifyRejectsUncataloguedThirdPartyMaterial(t *testing.T) {
	manifest, trustRoot, _ := signedFixture(t, func(manifest *fixtureManifest) {
		manifest.Materials[0].Name = "unlisted-third-party"
	})
	if _, err := Verify(context.Background(), manifest, trustRoot); err == nil {
		t.Fatal("Verify accepted a material that is neither first-party nor in the component catalog")
	}
}

func signedFixture(t *testing.T, mutate func(*fixtureManifest)) (Manifest, TrustRoot, string) {
	t.Helper()
	payloadBytes := compressZstd(t, makeTar(t, []tarEntry{
		{header: tar.Header{Name: "oci/platform-api.tar", Typeflag: tar.TypeReg, Mode: 0o600, Size: 3}, body: []byte("api")},
		{header: tar.Header{Name: "sbom/platform-api.cdx.json", Typeflag: tar.TypeReg, Mode: 0o600, Size: 4}, body: []byte("sbom")},
		{header: tar.Header{Name: "licenses/platform-api.txt", Typeflag: tar.TypeReg, Mode: 0o600, Size: 7}, body: []byte("license")},
	}))
	payloadDigest := sha256.Sum256(payloadBytes)
	manifest := fixtureManifest{
		SchemaVersion: 1,
		BundleID:      "ops-platform-dev",
		Platform:      "1.0.0",
		Architecture:  "linux/arm64",
		Payload: fixturePayload{
			File:               "payload.tar.zst",
			Digest:             "sha256:" + hex.EncodeToString(payloadDigest[:]),
			MaxFiles:           20,
			MaxBytes:           1024 * 1024,
			MaxCompressedBytes: 1024 * 1024,
			Files: []PayloadFile{
				{Path: "oci/platform-api.tar", Digest: fixtureDigest("api"), Size: 3, Kind: "oci"},
				{Path: "sbom/platform-api.cdx.json", Digest: fixtureDigest("sbom"), Size: 4, Kind: "sbom"},
				{Path: "licenses/platform-api.txt", Digest: fixtureDigest("license"), Size: 7, Kind: "license"},
			},
		},
		Materials: []fixtureMaterial{{
			Name: "platform-api", Kind: "container-image", Version: "1.0.0", Digest: fixtureDigest("api"),
			Architecture: "linux/arm64", PayloadRef: "oci/platform-api.tar", SBOMRef: "sbom/platform-api.cdx.json", LicenseRef: "licenses/platform-api.txt",
			InstallAfter: []string{},
		}},
	}
	if mutate != nil {
		mutate(&manifest)
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalizeJSON(manifestJSON)
	if err != nil {
		t.Fatalf("CanonicalizeJSON(%s): %v", manifestJSON, err)
	}
	var parsed Manifest
	if err := json.Unmarshal(canonical, &parsed); err != nil {
		t.Fatalf("decode signed fixture: %v", err)
	}
	parsed.CanonicalJSON = append([]byte(nil), canonical...)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Signature = ed25519.Sign(privateKey, canonical)
	payloadPath := filepath.Join(t.TempDir(), "payload.tar.zst")
	if err := os.WriteFile(payloadPath, payloadBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	parsed.PayloadPath = payloadPath
	return parsed, trustRootFor(t, privateKey.Public().(ed25519.PublicKey)), payloadPath
}

func trustRootFor(t *testing.T, key ed25519.PublicKey) TrustRoot {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(der)
	return TrustRoot{PublicKey: key, Fingerprint: "sha256:" + hex.EncodeToString(digest[:])}
}

func fixtureDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

type tarEntry struct {
	header tar.Header
	body   []byte
}

func makeTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := tar.NewWriter(&data)
	for _, entry := range entries {
		if entry.header.Size == 0 && len(entry.body) > 0 {
			entry.header.Size = int64(len(entry.body))
		}
		if err := writer.WriteHeader(&entry.header); err != nil {
			t.Fatal(err)
		}
		if len(entry.body) > 0 {
			if _, err := io.Copy(writer, bytes.NewReader(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func compressZstd(t *testing.T, content []byte) []byte {
	t.Helper()
	writer, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	compressed := writer.EncodeAll(content, nil)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed
}

func ExampleCanonicalizeJSON() {
	canonical, err := CanonicalizeJSON([]byte(`{"z":1.0,"a":0.000001}`))
	fmt.Println(string(canonical), err)
	// Output: {"a":0.000001,"z":1} <nil>
}
