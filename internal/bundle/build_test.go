package bundle

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildFixture(t *testing.T, missingLayer bool) (BuildSpec, string, string, TrustRoot) {
	t.Helper()
	input := t.TempDir()
	image, _ := ociFixture(t, missingLayer, false)
	content := map[string][]byte{
		"oci/platform-api.tar":      image,
		"sbom/platform-api.json":    []byte(`{"fixture":true}`),
		"licenses/platform-api.txt": []byte("Test fixture only; not a release license inventory"),
	}
	spec := BuildSpec{SchemaVersion: 1, BundleID: "build-fixture", PlatformVersion: "1.0.0", Architecture: "linux/arm64"}
	for name, data := range content {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(input, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(input, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
		spec.Files = append(spec.Files, BuildFile{PayloadFile: PayloadFile{Path: name, Digest: fixtureDigest(string(data)), Size: int64(len(data)), Kind: pathKind(name)}, Source: name})
	}
	// The primitive OCI test image is not a Go runtime; use the web identity.
	// Go runtime admission has a separate complete-source integration fixture.
	spec.Materials = []Material{{Name: "platform-web", Kind: "container-image", Version: "1.0.0", Architecture: "linux/arm64", Digest: fixtureDigest(string(image)), PayloadRef: "oci/platform-api.tar", SBOMRef: "sbom/platform-api.json", LicenseRef: "licenses/platform-api.txt", InstallAfter: []string{}}}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "signing.pem")
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return spec, input, key, trustRootFor(t, public)
}

func TestBuildReproducibleAndVerifiable(t *testing.T) {
	spec, input, key, trust := buildFixture(t, false)
	first, second := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	report, err := Build(context.Background(), spec, input, first, key)
	if err != nil {
		t.Fatal(err)
	}
	if report.KeyFingerprint != trust.Fingerprint || report.Verification.PayloadFilesVerified != 3 {
		t.Fatalf("unexpected build report: %+v", report)
	}
	// Order and filesystem timestamps must not influence the lock or payload.
	for i, j := 0, len(spec.Files)-1; i < j; i, j = i+1, j-1 {
		spec.Files[i], spec.Files[j] = spec.Files[j], spec.Files[i]
	}
	if _, err := Build(context.Background(), spec, input, second, key); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bundle.lock.json", "bundle.lock.sig", "payload.sha256", "payload.tar.zst"} {
		a, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(second, name))
		if err != nil || !bytes.Equal(a, b) {
			t.Fatalf("non-reproducible %s: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(first, "bundle.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(filepath.Join(first, "bundle.lock.sig"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil {
		t.Fatal(err)
	}
	manifest.PayloadPath = filepath.Join(first, "payload.tar.zst")
	if _, err := Verify(context.Background(), manifest, trust); err != nil {
		t.Fatalf("independent verification of built output: %v", err)
	}
	if err := os.WriteFile(manifest.PayloadPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), manifest, trust); err == nil {
		t.Fatal("tampered built payload accepted")
	}
}

func TestBuildPayloadLargerThanOneMiBRoundTripsWithinDecoderLimit(t *testing.T) {
	spec, input, key, _ := buildFixture(t, false)
	metadata := []byte(`{"fixture":true,"padding":"` + strings.Repeat("a", (2<<20)+17) + `"}`)
	for i := range spec.Files {
		if spec.Files[i].Path == "sbom/platform-api.json" {
			if err := os.WriteFile(filepath.Join(input, spec.Files[i].Source), metadata, 0600); err != nil {
				t.Fatal(err)
			}
			spec.Files[i].Size = int64(len(metadata))
			spec.Files[i].Digest = fixtureDigest(string(metadata))
		}
	}
	if _, err := Build(t.Context(), spec, input, filepath.Join(t.TempDir(), "bundle"), key); err != nil {
		t.Fatalf("signed payload window must fit the bounded verifier: %v", err)
	}
}

func TestBuildRejectsInvalidInputsWithoutPublishing(t *testing.T) {
	for _, name := range []string{"candidate", "changed-input", "missing-layer", "private-key-input", "public-key-permissions", "undeclared-image", "unsafe-path", "floating-version"} {
		t.Run(name, func(t *testing.T) {
			spec, input, key, _ := buildFixture(t, name == "missing-layer")
			switch name {
			case "candidate":
				spec.Materials[0].Name = "deepflow"
				spec.Materials[0].Version = "7.2.0"
			case "changed-input":
				if err := os.WriteFile(filepath.Join(input, "licenses/platform-api.txt"), []byte("altered"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "private-key-input":
				for i := range spec.Files {
					if spec.Files[i].Kind == "license" {
						spec.Files[i].Source = key
					}
				}
			case "public-key-permissions":
				if err := os.Chmod(key, 0o644); err != nil {
					t.Fatal(err)
				}
			case "undeclared-image":
				spec.Files = append(spec.Files, BuildFile{PayloadFile: PayloadFile{Path: "oci/hidden.tar", Digest: fixtureDigest("hidden"), Size: 6, Kind: "oci"}, Source: "not-read"})
			case "unsafe-path":
				spec.Files[0].Path = "oci/../escape"
			case "floating-version":
				spec.Materials[0].Version = "latest"
			}
			output := filepath.Join(t.TempDir(), "bundle")
			_, err := Build(context.Background(), spec, input, output, key)
			if err == nil {
				t.Fatal("invalid input accepted")
			}
			if name == "candidate" && !strings.Contains(err.Error(), "candidate component") {
				t.Fatalf("candidate must fail admission, got: %v", err)
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("failed build published output: %v", err)
			}
		})
	}
}

func TestBuildDoesNotOverwriteExistingDirectory(t *testing.T) {
	spec, input, key, _ := buildFixture(t, false)
	output := t.TempDir()
	sentinel := filepath.Join(output, "preserved")
	if err := os.WriteFile(sentinel, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), spec, input, output, key); err == nil {
		t.Fatal("existing output directory accepted")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserved" {
		t.Fatalf("existing output changed: %v", err)
	}
}
