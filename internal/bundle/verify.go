package bundle

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sigstore/sigstore/pkg/signature"
	componentcatalog "ops-platform/bundle"
	"ops-platform/internal/contract"
	"ops-platform/internal/supplychain"
)

var firstPartyMaterialNames = map[string]struct{}{
	"platform-api":             {},
	"platform-worker":          {},
	"platform-web":             {},
	"opsctl":                   {},
	"ops-platform-chart":       {},
	"ops-dependencies-chart":   {},
	"ops-metrics-server-chart": {},
	"opsctl-source":            {},
}

// Verify authenticates the canonical manifest using the independently trusted
// key, checks the compressed payload digest, then safely extracts into a private
// temporary directory and compares every payload file with the signed inventory.
type verifiedPayload struct {
	root     string
	path     string
	manifest Manifest
	report   VerificationReport
	catalog  *supplychain.Catalog
}

func (p *verifiedPayload) close() { _ = os.RemoveAll(p.root) }

func Verify(ctx context.Context, manifest Manifest, trustRoot TrustRoot) (VerificationReport, error) {
	p, err := prepare(ctx, manifest, trustRoot)
	if err != nil {
		return VerificationReport{}, err
	}
	defer p.close()
	return p.report, nil
}

func prepare(ctx context.Context, manifest Manifest, trustRoot TrustRoot) (*verifiedPayload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	if len(manifest.CanonicalJSON) == 0 || len(manifest.CanonicalJSON) > maxManifestBytes {
		return nil, errors.New("manifest canonical bytes are missing or exceed the size limit")
	}
	canonical, err := CanonicalizeJSON(manifest.CanonicalJSON)
	if err != nil || !bytes.Equal(canonical, manifest.CanonicalJSON) {
		return nil, errors.New("manifest bytes are not canonical RFC 8785 JSON")
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	encodedCanonical, err := CanonicalizeJSON(encoded)
	if err != nil || !bytes.Equal(encodedCanonical, manifest.CanonicalJSON) {
		return nil, errors.New("manifest values differ from their signed canonical JSON")
	}
	if err := validateManifestContract(manifest.CanonicalJSON); err != nil {
		return nil, err
	}
	if len(manifest.Signature) == 0 || len(manifest.Signature) > 8192 {
		return nil, errors.New("detached manifest signature is missing or invalid in size")
	}
	if trustRoot.PublicKey == nil || trustRoot.Fingerprint == "" {
		return nil, errors.New("an explicit trusted public key and fingerprint are required")
	}
	fingerprint, err := PublicKeyFingerprint(trustRoot.PublicKey)
	if err != nil {
		return nil, err
	}
	if fingerprint != trustRoot.Fingerprint {
		return nil, errors.New("trusted public key fingerprint does not match the supplied trust root")
	}
	hash := crypto.SHA256
	if _, ok := trustRoot.PublicKey.(ed25519.PublicKey); ok {
		hash = 0
	}
	verifier, err := signature.LoadVerifier(trustRoot.PublicKey, hash)
	if err != nil {
		return nil, fmt.Errorf("load Sigstore public-key verifier: %w", err)
	}
	if err := verifier.VerifySignature(bytes.NewReader(manifest.Signature), bytes.NewReader(manifest.CanonicalJSON)); err != nil {
		return nil, fmt.Errorf("verify detached manifest signature: %w", err)
	}
	catalog, err := loadEmbeddedCatalog()
	if err != nil {
		return nil, err
	}
	if err := validateCatalogAdmissionWithCatalog(manifest.Materials, catalog); err != nil {
		return nil, err
	}

	if filepath.Base(manifest.PayloadPath) != "payload.tar.zst" {
		return nil, errors.New("payload path must name payload.tar.zst")
	}
	pathInfo, err := os.Lstat(manifest.PayloadPath)
	if err != nil {
		return nil, fmt.Errorf("stat payload path: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, errors.New("payload must be a regular, non-symlink file")
	}
	payload, err := os.Open(manifest.PayloadPath)
	if err != nil {
		return nil, fmt.Errorf("open payload: %w", err)
	}
	defer payload.Close()
	info, err := payload.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat payload: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("payload must be a regular file")
	}
	if info.Size() > manifest.Payload.MaxCompressedBytes {
		return nil, errors.New("payload exceeds maxCompressedBytes")
	}
	digest, size, err := digestReader(ctx, payload, manifest.Payload.MaxCompressedBytes)
	if err != nil {
		return nil, fmt.Errorf("hash payload: %w", err)
	}
	if size != info.Size() || digest != manifest.Payload.Digest {
		return nil, fmt.Errorf("payload digest mismatch: got %s, want %s", digest, manifest.Payload.Digest)
	}
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind payload: %w", err)
	}
	temporaryRoot, err := os.MkdirTemp("", "opsctl-bundle-verify-")
	if err != nil {
		return nil, fmt.Errorf("create verification directory: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(temporaryRoot)
		}
	}()
	extractPath := filepath.Join(temporaryRoot, "payload")
	extracted, err := safeExtract(ctx, payload, extractPath, ExtractionLimits{MaxFiles: manifest.Payload.MaxFiles, MaxBytes: manifest.Payload.MaxBytes})
	if err != nil {
		return nil, fmt.Errorf("safely extract payload: %w", err)
	}
	if err := comparePayloadFiles(manifest.Payload.Files, extracted.Files); err != nil {
		return nil, err
	}
	success = true
	return &verifiedPayload{root: temporaryRoot, path: extractPath, manifest: manifest, catalog: catalog, report: VerificationReport{
		BundleID: manifest.BundleID, Architecture: manifest.Architecture, PayloadDigest: digest,
		SignatureVerified: true, PayloadDigestVerified: true,
		PayloadFilesVerified: extracted.FileCount, PayloadBytesVerified: extracted.TotalBytes,
	}}, nil
}

func loadEmbeddedCatalog() (*supplychain.Catalog, error) {
	evidence, err := componentcatalog.ComponentEvidence()
	if err != nil {
		return nil, fmt.Errorf("load embedded component evidence: %w", err)
	}
	catalog, err := supplychain.LoadCatalogWithEvidence(bytes.NewReader(componentcatalog.ComponentCatalog()), evidence)
	if err != nil {
		return nil, fmt.Errorf("load embedded component catalog: %w", err)
	}
	return &catalog, nil
}

func validateCatalogAdmissionWithEvidence(materials []Material, catalogYAML []byte, evidence fs.FS) error {
	catalog, err := supplychain.LoadCatalogWithEvidence(bytes.NewReader(catalogYAML), evidence)
	if err != nil {
		return fmt.Errorf("load embedded component catalog: %w", err)
	}
	return validateCatalogAdmissionWithCatalog(materials, &catalog)
}

func validateCatalogAdmissionWithCatalog(materials []Material, catalog *supplychain.Catalog) error {
	if err := validateMigrationMaterialAdmission(materials); err != nil {
		return err
	}
	selectedNames := make([]string, 0, len(materials))
	selected := make(map[string]bool, len(materials))
	for _, material := range materials {
		selected[material.Name] = true
	}
	for _, name := range []string{"platform-api", "platform-worker", "opsctl", "command-runner"} {
		if !selected[name] {
			continue
		}
		sdk, ok := catalog.Component("opa-sdk")
		if !ok || sdk.State != "qualified" || sdk.CorrespondingSourceBundleSHA256 == "" || !selected["opa-sdk-source"] {
			return fmt.Errorf("first-party Go material %s requires qualified opa-sdk and its complete locked source/license closure", name)
		}
		for _, material := range materials {
			if material.Name == name {
				supported := false
				for _, architecture := range sdk.Architectures {
					supported = supported || architecture == material.Architecture
				}
				if !supported {
					return fmt.Errorf("first-party Go material %s has no qualified SDK source closure for %s", name, material.Architecture)
				}
			}
		}
	}
	// Historical 1.0.0 Worker materials predate the optional SP05 CLI. New
	// versions distribute its immutable executable and must carry its exact
	// original source/license closure, even though it is embedded in the image.
	for _, material := range materials {
		if material.Name == "platform-worker" && material.Version != "1.0.0" {
			analyzer, ok := catalog.Component("k8sgpt")
			if !ok || analyzer.State != "qualified" || analyzer.CorrespondingSourceBundleSHA256 == "" || !selected["k8sgpt-source"] {
				return errors.New("SP05 Worker requires qualified fixed Analyzer and its complete corresponding-source material")
			}
		}
	}
	if selected["deepflow"] && selected["deepflow-app"] {
		return errors.New("DeepFlow bundle must not contain deepflow-app")
	}
	if selected["holmesgpt"] && !selected["holmesgpt-source"] {
		return errors.New("SP06 investigator requires its complete corresponding-source/license closure, including GPL wrapper and runtime dependencies")
	}
	for _, material := range materials {
		if material.Name == "db-migrate" || material.Name == "db-migrate-source" {
			// These exact first-party CLI/source identities have a separate
			// embedded admission; they do not borrow a runtime SDK allowlist.
			continue
		}
		if _, known := catalog.Component(material.Name); known {
			selectedNames = append(selectedNames, material.Name)
			continue
		}
		if strings.HasSuffix(material.Name, "-source") {
			name := strings.TrimSuffix(material.Name, "-source")
			if component, ok := catalog.Component(name); ok {
				if material.Kind != "source" || component.CorrespondingSourceBundleSHA256 == "" || material.Digest != component.CorrespondingSourceBundleSHA256 || material.Version != component.Version {
					return fmt.Errorf("source material %s differs from the reviewed corresponding-source lock", material.Name)
				}
				supported := false
				for _, architecture := range component.Architectures {
					supported = supported || architecture == material.Architecture
				}
				if !supported {
					return fmt.Errorf("source material %s has no qualified closure for %s", material.Name, material.Architecture)
				}
				selectedNames = append(selectedNames, name)
				continue
			}
		}
		if strings.HasSuffix(material.Name, "-chart") {
			name := strings.TrimSuffix(material.Name, "-chart")
			if component, ok := catalog.Component(name); ok {
				if material.Kind != "chart" || component.ChartLock == nil || component.ChartLock.Version != material.Version || component.ChartLock.Digest != material.Digest {
					return fmt.Errorf("chart material %s differs from the catalog chart lock", material.Name)
				}
				selectedNames = append(selectedNames, name)
				continue
			}
		}
		if _, firstParty := firstPartyMaterialNames[material.Name]; firstParty {
			continue
		}
		return fmt.Errorf("bundle material %q is not in the component catalog or first-party allowlist", material.Name)
	}
	if err := catalog.ValidateBundle(selectedNames); err != nil {
		return fmt.Errorf("validate component admission: %w", err)
	}
	for _, name := range selectedNames {
		component, _ := catalog.Component(name)
		if selected[name] && component.CorrespondingSourceBundleSHA256 != "" && !selected[name+"-source"] {
			return fmt.Errorf("component %s is missing its locked corresponding-source material", name)
		}
	}
	return nil
}

func validateManifestContract(raw []byte) error {
	if err := contract.Validate(ManifestSchemaID, raw); err != nil {
		return fmt.Errorf("validate bundle manifest: %w", err)
	}
	return nil
}

func digestReader(ctx context.Context, reader io.Reader, limit int64) (string, int64, error) {
	hash := sha256.New()
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", total, err
		}
		read, err := reader.Read(buffer)
		if read > 0 {
			total += int64(read)
			if total > limit {
				return "", total, errors.New("payload exceeds compressed byte limit")
			}
			_, _ = hash.Write(buffer[:read])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", total, err
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), total, nil
}

func comparePayloadFiles(expected, actual []PayloadFile) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("payload file inventory mismatch: manifest has %d files, archive has %d", len(expected), len(actual))
	}
	byPath := make(map[string]PayloadFile, len(actual))
	for _, file := range actual {
		byPath[file.Path] = file
	}
	for _, file := range expected {
		got, ok := byPath[file.Path]
		if !ok || got.Digest != file.Digest || got.Size != file.Size || got.Kind != file.Kind {
			return fmt.Errorf("payload file %q does not match its signed inventory", file.Path)
		}
	}
	return nil
}
