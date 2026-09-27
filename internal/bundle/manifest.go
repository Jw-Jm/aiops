package bundle

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"ops-platform/internal/contract"
)

const (
	ManifestSchemaID = "https://ops.local/schemas/bundle-lock/v1"
	maxManifestBytes = 1 << 20
)

type Manifest struct {
	SchemaVersion   int        `json:"schemaVersion"`
	BundleID        string     `json:"bundleId"`
	PlatformVersion string     `json:"platformVersion"`
	Architecture    string     `json:"architecture"`
	Payload         Payload    `json:"payload"`
	Materials       []Material `json:"materials"`

	// Runtime-only inputs populated by the bootstrap CLI. They are never signed
	// as fields in bundle.lock.json; Signature is detached in bundle.lock.sig.
	CanonicalJSON []byte `json:"-"`
	Signature     []byte `json:"-"`
	PayloadPath   string `json:"-"`
}

type Payload struct {
	File               string        `json:"file"`
	Digest             string        `json:"digest"`
	MaxFiles           int           `json:"maxFiles"`
	MaxBytes           int64         `json:"maxBytes"`
	MaxCompressedBytes int64         `json:"maxCompressedBytes"`
	Files              []PayloadFile `json:"files"`
}

type PayloadFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	Kind   string `json:"kind"`
}

type Material struct {
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

type TrustRoot struct {
	PublicKey   crypto.PublicKey
	Fingerprint string
}

type VerificationReport struct {
	BundleID              string `json:"bundleId"`
	Architecture          string `json:"architecture"`
	PayloadDigest         string `json:"payloadDigest"`
	SignatureVerified     bool   `json:"signatureVerified"`
	PayloadDigestVerified bool   `json:"payloadDigestVerified"`
	PayloadFilesVerified  int    `json:"payloadFilesVerified"`
	PayloadBytesVerified  int64  `json:"payloadBytesVerified"`
}

// ParseManifest accepts only a schema-valid, already RFC 8785 canonicalized
// manifest, preserving those exact bytes for detached signature verification.
func ParseManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > maxManifestBytes {
		return Manifest{}, fmt.Errorf("manifest size must be between 1 and %d bytes", maxManifestBytes)
	}
	canonical, err := CanonicalizeJSON(raw)
	if err != nil {
		return Manifest{}, fmt.Errorf("canonicalize manifest: %w", err)
	}
	if !bytes.Equal(raw, canonical) {
		return Manifest{}, errors.New("manifest must use RFC 8785 canonical JSON bytes")
	}
	if err := contract.Validate(ManifestSchemaID, raw); err != nil {
		return Manifest{}, fmt.Errorf("validate bundle manifest schema: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode bundle manifest: %w", err)
	}
	manifest.CanonicalJSON = append([]byte(nil), raw...)
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// ReadTrustRoot loads an explicitly trusted PEM public key and records its
// SHA-256 fingerprint. No key embedded in a Bundle is consulted.
func ReadTrustRoot(reader io.Reader) (TrustRoot, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 64*1024+1))
	if err != nil {
		return TrustRoot{}, fmt.Errorf("read trust key: %w", err)
	}
	if len(data) > 64*1024 {
		return TrustRoot{}, errors.New("trust key exceeds 65536 bytes")
	}
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || block.Type != "PUBLIC KEY" {
		return TrustRoot{}, errors.New("trust root must contain one PEM PUBLIC KEY")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return TrustRoot{}, fmt.Errorf("parse trust public key: %w", err)
	}
	fingerprint, err := PublicKeyFingerprint(key)
	if err != nil {
		return TrustRoot{}, err
	}
	return TrustRoot{PublicKey: key, Fingerprint: fingerprint}, nil
}

func PublicKeyFingerprint(key crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", fmt.Errorf("marshal trust public key: %w", err)
	}
	digest := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != 1 {
		return errors.New("schemaVersion must be 1")
	}
	if manifest.BundleID == "" || manifest.PlatformVersion == "" {
		return errors.New("bundleId and platformVersion are required")
	}
	if !strings.HasPrefix(manifest.Architecture, "linux/") || (manifest.Architecture != "linux/amd64" && manifest.Architecture != "linux/arm64") {
		return fmt.Errorf("unsupported bundle architecture %q", manifest.Architecture)
	}
	if manifest.Payload.File != "payload.tar.zst" {
		return errors.New("payload file must be payload.tar.zst")
	}
	if !isSHA256(manifest.Payload.Digest) {
		return errors.New("payload digest must be a sha256 digest")
	}
	if manifest.Payload.MaxFiles < 1 || manifest.Payload.MaxFiles > 100000 {
		return errors.New("payload maxFiles must be between 1 and 100000")
	}
	if manifest.Payload.MaxBytes < 1 || manifest.Payload.MaxBytes > 1<<53-1 {
		return errors.New("payload maxBytes is outside the supported range")
	}
	if manifest.Payload.MaxCompressedBytes < 1 || manifest.Payload.MaxCompressedBytes > 1<<53-1 {
		return errors.New("payload maxCompressedBytes is outside the supported range")
	}
	if len(manifest.Payload.Files) == 0 || len(manifest.Payload.Files) > manifest.Payload.MaxFiles {
		return errors.New("payload files must be non-empty and within maxFiles")
	}
	files := make(map[string]PayloadFile, len(manifest.Payload.Files))
	var totalBytes int64
	for _, file := range manifest.Payload.Files {
		if err := validatePayloadPath(file.Path); err != nil {
			return err
		}
		if !isSHA256(file.Digest) {
			return fmt.Errorf("payload file %q digest must be a sha256 digest", file.Path)
		}
		if file.Size < 0 || file.Size > manifest.Payload.MaxBytes-totalBytes {
			return errors.New("declared payload files exceed maxBytes")
		}
		totalBytes += file.Size
		if file.Kind != pathKind(file.Path) {
			return fmt.Errorf("payload file %q kind does not match its payload path", file.Path)
		}
		if _, exists := files[file.Path]; exists {
			return fmt.Errorf("duplicate payload file %q", file.Path)
		}
		files[file.Path] = file
	}
	if len(manifest.Materials) == 0 {
		return errors.New("bundle materials must not be empty")
	}
	materials := make(map[string]Material, len(manifest.Materials))
	for _, material := range manifest.Materials {
		if material.Name == "" || material.Version == "" {
			return errors.New("material name and version are required")
		}
		if isFloatingVersion(material.Version) {
			return fmt.Errorf("material %q uses a floating version %q", material.Name, material.Version)
		}
		if !isExactBundleVersion(material.Version) {
			return fmt.Errorf("material %q version %q must be an exact version or immutable commit", material.Name, material.Version)
		}
		if _, exists := materials[material.Name]; exists {
			return fmt.Errorf("duplicate material name %q", material.Name)
		}
		if material.Architecture != manifest.Architecture {
			return fmt.Errorf("material %q architecture %q does not match bundle architecture %q", material.Name, material.Architecture, manifest.Architecture)
		}
		if !isSHA256(material.Digest) {
			return fmt.Errorf("material %q digest must be a sha256 digest", material.Name)
		}
		artifact, ok := files[material.PayloadRef]
		if !ok {
			return fmt.Errorf("material %q payload reference %q is missing", material.Name, material.PayloadRef)
		}
		if artifact.Digest != material.Digest {
			return fmt.Errorf("material %q digest does not match payload file %q", material.Name, material.PayloadRef)
		}
		if artifact.Kind != materialPayloadKind(material.Kind) {
			return fmt.Errorf("material %q payload file %q has kind %q, want %q", material.Name, material.PayloadRef, artifact.Kind, materialPayloadKind(material.Kind))
		}
		if sbom, ok := files[material.SBOMRef]; !ok || sbom.Kind != "sbom" {
			return fmt.Errorf("material %q SBOM reference %q is missing", material.Name, material.SBOMRef)
		}
		if license, ok := files[material.LicenseRef]; !ok || license.Kind != "license" {
			return fmt.Errorf("material %q license reference %q is missing", material.Name, material.LicenseRef)
		}
		materials[material.Name] = material
	}
	return validateInstallOrder(materials)
}

func materialPayloadKind(materialKind string) string {
	switch materialKind {
	case "container-image":
		return "oci"
	case "chart":
		return "chart"
	case "binary":
		return "binary"
	case "source":
		return "source"
	case "profile":
		return "profile"
	default:
		return ""
	}
}

func validateInstallOrder(materials map[string]Material) error {
	state := make(map[string]uint8, len(materials))
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("install order cycle includes material %q", name)
		case 2:
			return nil
		}
		state[name] = 1
		for _, dependency := range materials[name].InstallAfter {
			if _, exists := materials[dependency]; !exists {
				return fmt.Errorf("material %q installAfter references unknown material %q", name, dependency)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	for name := range materials {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func validatePayloadPath(value string) error {
	if value == "" || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") || strings.Contains(value, "..") {
		return fmt.Errorf("unsafe payload path %q", value)
	}
	if path.Clean(value) != value || pathKind(value) == "" {
		return fmt.Errorf("payload path %q is not within an approved payload directory", value)
	}
	return nil
}

func pathKind(value string) string {
	root, _, ok := strings.Cut(value, "/")
	if !ok {
		return ""
	}
	switch root {
	case "oci":
		return "oci"
	case "charts":
		return "chart"
	case "binaries":
		return "binary"
	case "sbom":
		return "sbom"
	case "licenses":
		return "license"
	case "sources":
		return "source"
	case "profiles":
		return "profile"
	default:
		return ""
	}
}

func isSHA256(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func isFloatingVersion(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "latest", "main", "master", "develop", "trunk", "stable", "nightly", "edge", "release", "dev", "unstable", "canary", "pending":
		return true
	default:
		return strings.ContainsAny(value, "*?") || strings.HasSuffix(normalized, ".x")
	}
}

var exactBundleVersionPattern = regexp.MustCompile(`^(v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?|[A-Fa-f0-9]{40,64})$`)

func isExactBundleVersion(value string) bool {
	return exactBundleVersionPattern.MatchString(value)
}
