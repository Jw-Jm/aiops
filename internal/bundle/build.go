package bundle

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/sigstore/sigstore/pkg/signature"
)

// BuildSpec declares local, already prepared inputs. Digest and size are supplied
// by preparation, not silently filled in from whatever happens to be on disk.
// Relative source paths are resolved against the spec's directory.
type BuildSpec struct {
	SchemaVersion   int         `json:"schemaVersion"`
	BundleID        string      `json:"bundleId"`
	PlatformVersion string      `json:"platformVersion"`
	Architecture    string      `json:"architecture"`
	Files           []BuildFile `json:"files"`
	Materials       []Material  `json:"materials"`
}

type BuildFile struct {
	PayloadFile
	Source string `json:"source"`
}

type BuildReport struct {
	Directory      string             `json:"directory"`
	KeyFingerprint string             `json:"keyFingerprint"`
	Verification   VerificationReport `json:"verification"`
}

func ParseBuildSpec(raw []byte) (BuildSpec, error) {
	if len(raw) == 0 || len(raw) > maxManifestBytes {
		return BuildSpec{}, errors.New("build spec is empty or exceeds 1 MiB")
	}
	var spec BuildSpec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return BuildSpec{}, fmt.Errorf("decode build spec: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return BuildSpec{}, errors.New("build spec must contain exactly one JSON document")
	}
	return spec, nil
}

// Build never downloads, imports images or calls Helm. It signs a complete local
// inventory only after admission, input digest and OCI closure checks. A freshly
// built Bundle is verified before publishing; this self-check does not establish
// the installer's independent trust in the signing public key.
func Build(ctx context.Context, spec BuildSpec, inputDirectory, outputDirectory, signingKeyPath string) (BuildReport, error) {
	if err := ctx.Err(); err != nil {
		return BuildReport{}, err
	}
	if outputDirectory == "" || signingKeyPath == "" {
		return BuildReport{}, errors.New("output directory and external signing key are required")
	}
	manifest := Manifest{
		SchemaVersion: spec.SchemaVersion, BundleID: spec.BundleID,
		PlatformVersion: spec.PlatformVersion, Architecture: spec.Architecture,
		Materials: append([]Material(nil), spec.Materials...),
		Payload: Payload{File: "payload.tar.zst", Digest: "sha256:" + strings.Repeat("0", 64),
			MaxFiles: len(spec.Files), MaxBytes: 1, MaxCompressedBytes: 1},
	}
	files := append([]BuildFile(nil), spec.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, file := range files {
		if file.Size < 0 || file.Size > (1<<53-1)-manifest.Payload.MaxBytes {
			return BuildReport{}, errors.New("build inputs exceed supported byte limit")
		}
		manifest.Payload.MaxBytes += file.Size
		manifest.Payload.Files = append(manifest.Payload.Files, file.PayloadFile)
	}
	if manifest.Payload.MaxBytes > 1 {
		manifest.Payload.MaxBytes--
	}
	sort.Slice(manifest.Materials, func(i, j int) bool { return manifest.Materials[i].Name < manifest.Materials[j].Name })
	for i := range manifest.Materials {
		manifest.Materials[i].InstallAfter = append([]string{}, manifest.Materials[i].InstallAfter...)
		sort.Strings(manifest.Materials[i].InstallAfter)
	}
	if err := validateManifest(manifest); err != nil {
		return BuildReport{}, err
	}
	referenced := make(map[string]bool)
	for _, material := range manifest.Materials {
		for _, ref := range []string{material.PayloadRef, material.SBOMRef, material.LicenseRef} {
			referenced[ref] = true
		}
	}
	for _, file := range files {
		if !referenced[file.Path] {
			return BuildReport{}, fmt.Errorf("payload file %s is not declared by a material", file.Path)
		}
	}
	preliminary, err := json.Marshal(manifest)
	if err != nil {
		return BuildReport{}, err
	}
	if err := validateManifestContract(preliminary); err != nil {
		return BuildReport{}, err
	}
	catalog, err := loadEmbeddedCatalog()
	if err != nil {
		return BuildReport{}, err
	}
	if err := validateCatalogAdmissionWithCatalog(manifest.Materials, catalog); err != nil {
		return BuildReport{}, err
	}
	if _, err := os.Lstat(outputDirectory); !errors.Is(err, os.ErrNotExist) {
		return BuildReport{}, errors.New("Bundle output must not already exist")
	}
	key, keyInfo, err := readBuildSigningKey(signingKeyPath)
	if err != nil {
		return BuildReport{}, err
	}
	// The key must never be packaged, including through a hard-link alias.
	for i := range files {
		if files[i].Source == "" {
			return BuildReport{}, fmt.Errorf("source is required for %s", files[i].Path)
		}
		if !filepath.IsAbs(files[i].Source) {
			files[i].Source = filepath.Join(inputDirectory, files[i].Source)
		}
		info, err := os.Lstat(files[i].Source)
		if err != nil || !info.Mode().IsRegular() {
			return BuildReport{}, fmt.Errorf("source for %s must be a regular, non-symlink file", files[i].Path)
		}
		if os.SameFile(info, keyInfo) {
			return BuildReport{}, errors.New("signing key cannot be a payload input")
		}
	}
	// Private staging is beside the output, so publication stays on one volume.
	temporary, err := os.MkdirTemp(filepath.Dir(outputDirectory), ".ops-bundle-build-")
	if err != nil {
		return BuildReport{}, fmt.Errorf("create Bundle staging directory: %w", err)
	}
	defer os.RemoveAll(temporary)
	manifest.PayloadPath = filepath.Join(temporary, "payload.tar.zst")
	if err := writeBuildPayload(ctx, manifest.PayloadPath, files); err != nil {
		return BuildReport{}, err
	}
	// Validate image semantics on the authenticated snapshot rather than on
	// source files that could change between hashing and packaging.
	payload, err := os.Open(manifest.PayloadPath)
	if err != nil {
		return BuildReport{}, err
	}
	info, err := payload.Stat()
	if err == nil {
		manifest.Payload.MaxCompressedBytes = info.Size()
		manifest.Payload.Digest, _, err = digestReader(ctx, payload, info.Size())
	}
	closeErr := payload.Close()
	if err != nil {
		return BuildReport{}, err
	}
	if closeErr != nil {
		return BuildReport{}, closeErr
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return BuildReport{}, err
	}
	manifest.CanonicalJSON, err = CanonicalizeJSON(raw)
	if err != nil {
		return BuildReport{}, err
	}
	if _, err := ParseManifest(manifest.CanonicalJSON); err != nil {
		return BuildReport{}, err
	}
	hash := crypto.SHA256
	if _, ok := key.(ed25519.PrivateKey); ok {
		hash = 0
	}
	signer, err := signature.LoadSigner(key, hash)
	if err != nil {
		return BuildReport{}, err
	}
	manifest.Signature, err = signer.SignMessage(bytes.NewReader(manifest.CanonicalJSON))
	if err != nil {
		return BuildReport{}, fmt.Errorf("sign Bundle lock: %w", err)
	}
	fingerprint, err := PublicKeyFingerprint(key.Public())
	if err != nil {
		return BuildReport{}, err
	}
	verified, err := prepare(ctx, manifest, TrustRoot{PublicKey: key.Public(), Fingerprint: fingerprint})
	if err != nil {
		return BuildReport{}, fmt.Errorf("verify built Bundle: %w", err)
	}
	defer verified.close()
	for _, material := range manifest.Materials {
		if material.Kind != "container-image" {
			continue
		}
		digest, reference, err := validateOCIArchive(filepath.Join(verified.path, filepath.FromSlash(material.PayloadRef)), manifest.Architecture)
		if err != nil {
			return BuildReport{}, fmt.Errorf("OCI material %s: %w", material.Name, err)
		}
		if reference == "" || !strings.HasSuffix(reference, "@"+digest) {
			return BuildReport{}, fmt.Errorf("OCI material %s needs an immutable image reference", material.Name)
		}
		if component, ok := catalog.Component(material.Name); ok && (component.Digest != digest || component.Version != material.Version) {
			return BuildReport{}, fmt.Errorf("OCI material %s differs from the qualified catalog image/version", material.Name)
		}
	}
	outerFiles := map[string][]byte{
		"bundle.lock.json": manifest.CanonicalJSON,
		"bundle.lock.sig":  []byte(base64.StdEncoding.EncodeToString(manifest.Signature) + "\n"),
		"payload.sha256":   []byte(manifest.Payload.Digest + "\n"),
	}
	for name, content := range outerFiles {
		if err := os.WriteFile(filepath.Join(temporary, name), content, 0o600); err != nil {
			return BuildReport{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return BuildReport{}, err
	}
	// Claim the destination exclusively; do not replace an existing directory
	// (even an empty one) or a concurrently created Bundle.
	if err := os.Mkdir(outputDirectory, 0o700); err != nil {
		return BuildReport{}, fmt.Errorf("claim Bundle output: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(outputDirectory)
		}
	}()
	for _, name := range []string{"payload.tar.zst", "payload.sha256", "bundle.lock.json", "bundle.lock.sig"} {
		if err := os.Rename(filepath.Join(temporary, name), filepath.Join(outputDirectory, name)); err != nil {
			return BuildReport{}, fmt.Errorf("publish Bundle file: %w", err)
		}
	}
	complete = true
	return BuildReport{Directory: outputDirectory, KeyFingerprint: fingerprint, Verification: verified.report}, nil
}

func readBuildSigningKey(file string) (crypto.Signer, os.FileInfo, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return nil, nil, fmt.Errorf("stat signing key: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 64<<10 {
		return nil, nil, errors.New("signing key must be a private regular file (0600 or stricter), at most 64 KiB")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, errors.New("signing key must contain one unencrypted PKCS#8 PRIVATE KEY")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, errors.New("invalid PKCS#8 signing key")
	}
	key, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, nil, errors.New("unsupported signing key")
	}
	return key, info, nil
}

func writeBuildPayload(ctx context.Context, output string, files []BuildFile) (err error) {
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	// The verifier permits at least a 1 MiB window. Streaming's default 8 MiB
	// window exceeds the signed byte bound for small real image Bundles.
	compressed, err := zstd.NewWriter(file, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(1<<20))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, compressed.Close()) }()
	archive := tar.NewWriter(compressed)
	defer func() { err = errors.Join(err, archive.Close()) }()
	for _, directory := range payloadDirectories {
		if err := archive.WriteHeader(&tar.Header{Name: directory + "/", Mode: 0o700, Typeflag: tar.TypeDir, Format: tar.FormatPAX}); err != nil {
			return err
		}
	}
	for _, input := range files {
		mode := int64(0o600)
		if input.Kind == "binary" {
			mode = 0o700
		}
		if err := archive.WriteHeader(&tar.Header{Name: input.Path, Mode: mode, Size: input.Size, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return err
		}
		source, err := os.Open(input.Source)
		if err != nil {
			return err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(archive, hash), contextInput{ctx: ctx, reader: io.LimitReader(source, input.Size+1)})
		closeErr := source.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		if size != input.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != input.Digest {
			return fmt.Errorf("input digest or size mismatch: %s", input.Path)
		}
	}
	return nil
}
