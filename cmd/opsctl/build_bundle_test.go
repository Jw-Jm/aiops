package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ops-platform/internal/bundle"
)

func TestBundleBuildCLIAndIndependentVerify(t *testing.T) {
	input, secret := t.TempDir(), t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	key, pub := filepath.Join(secret, "signing.pem"), filepath.Join(secret, "trusted.pem")
	for path, block := range map[string]*pem.Block{key: {Type: "PRIVATE KEY", Bytes: privateDER}, pub: {Type: "PUBLIC KEY", Bytes: publicDER}} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := bundle.BuildSpec{SchemaVersion: 1, BundleID: "cli-fixture", PlatformVersion: "1.0.0", Architecture: "linux/arm64"}
	content := map[string]string{"binaries/opsctl": "test fixture", "sbom/opsctl.json": `{"fixture":true}`, "licenses/opsctl.txt": "test fixture only"}
	digests := map[string]string{}
	for name, data := range content {
		sum := sha256.Sum256([]byte(data))
		digests[name] = "sha256:" + hex.EncodeToString(sum[:])
		kind := "binary"
		if strings.HasPrefix(name, "sbom/") {
			kind = "sbom"
		} else if strings.HasPrefix(name, "licenses/") {
			kind = "license"
		}
		file := filepath.Join(input, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		spec.Files = append(spec.Files, bundle.BuildFile{PayloadFile: bundle.PayloadFile{Path: name, Digest: digests[name], Size: int64(len(data)), Kind: kind}, Source: name})
	}
	spec.Materials = []bundle.Material{{Name: "opsctl", Kind: "binary", Version: "1.0.0", Digest: digests["binaries/opsctl"], Architecture: "linux/arm64", PayloadRef: "binaries/opsctl", SBOMRef: "sbom/opsctl.json", LicenseRef: "licenses/opsctl.txt", InstallAfter: []string{}}}
	// This is a packaging fixture, but first-party names still satisfy the real
	// source-admission gate. Rebuild from the locked local cache, without network.
	sourceDir := t.TempDir()
	command := exec.CommandContext(t.Context(), "python3", "scripts/prepare-runtime-source.py", "--out", sourceDir)
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare qualified runtime source: %v: %s", err, output)
	}
	sourcePath := filepath.Join(sourceDir, "runtime-go-source.tar")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	sourceDigest := "sha256:" + hex.EncodeToString(sum[:])
	spec.Files = append(spec.Files, bundle.BuildFile{PayloadFile: bundle.PayloadFile{Path: "sources/opa-sdk.tar", Digest: sourceDigest, Size: int64(len(source)), Kind: "source"}, Source: sourcePath})
	spec.Materials = append(spec.Materials, bundle.Material{Name: "opa-sdk-source", Kind: "source", Version: "v1.21.0", Digest: sourceDigest, Architecture: "linux/arm64", PayloadRef: "sources/opa-sdk.tar", SBOMRef: "sbom/opsctl.json", LicenseRef: "licenses/opsctl.txt", InstallAfter: []string{}})
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(input, "build.json")
	if err := os.WriteFile(specPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "bundle")
	// The Make target supplies a relative output while the operator key is
	// absolute. Both must be normalized before their containment comparison.
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	output, err = filepath.Rel(workingDirectory, output)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"bundle", "build", "--architecture", "linux/arm64", "--spec", specPath, "--output", output, "--signing-key", key}
	if err := run(context.Background(), args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report bundle.BuildReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || !report.Verification.SignatureVerified {
		t.Fatalf("invalid CLI build report: %s; %v", stdout.String(), err)
	}
	stdout.Reset()
	if err := run(context.Background(), []string{"bundle", "verify", "--manifest", filepath.Join(output, "bundle.lock.json"), "--signature", filepath.Join(output, "bundle.lock.sig"), "--payload", filepath.Join(output, "payload.tar.zst"), "--key", pub}, &stdout, &stderr); err != nil {
		t.Fatalf("independently keyed CLI verification: %v", err)
	}
	// The arm64 Make target must fail rather than silently build an amd64 spec.
	args[3] = "linux/amd64"
	if err := run(context.Background(), args, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "architecture") {
		t.Fatalf("architecture mismatch accepted: %v", err)
	}
	args[3] = "linux/arm64"
	args[len(args)-1] = filepath.Join("..", "..", "go.mod")
	if err := run(context.Background(), args, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("repository signing key accepted: %v", err)
	}
}
