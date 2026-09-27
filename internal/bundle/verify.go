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
	"os"
	"path/filepath"

	"github.com/sigstore/sigstore/pkg/signature"
	"ops-platform/internal/contract"
)

// Verify authenticates the canonical manifest using the independently trusted
// key, checks the compressed payload digest, then safely extracts into a private
// temporary directory and compares every payload file with the signed inventory.
func Verify(ctx context.Context, manifest Manifest, trustRoot TrustRoot) (VerificationReport, error) {
	if err := ctx.Err(); err != nil {
		return VerificationReport{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return VerificationReport{}, err
	}
	if len(manifest.CanonicalJSON) == 0 || len(manifest.CanonicalJSON) > maxManifestBytes {
		return VerificationReport{}, errors.New("manifest canonical bytes are missing or exceed the size limit")
	}
	canonical, err := CanonicalizeJSON(manifest.CanonicalJSON)
	if err != nil || !bytes.Equal(canonical, manifest.CanonicalJSON) {
		return VerificationReport{}, errors.New("manifest bytes are not canonical RFC 8785 JSON")
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return VerificationReport{}, fmt.Errorf("encode manifest: %w", err)
	}
	encodedCanonical, err := CanonicalizeJSON(encoded)
	if err != nil || !bytes.Equal(encodedCanonical, manifest.CanonicalJSON) {
		return VerificationReport{}, errors.New("manifest values differ from their signed canonical JSON")
	}
	if err := validateManifestContract(manifest.CanonicalJSON); err != nil {
		return VerificationReport{}, err
	}
	if len(manifest.Signature) == 0 || len(manifest.Signature) > 8192 {
		return VerificationReport{}, errors.New("detached manifest signature is missing or invalid in size")
	}
	if trustRoot.PublicKey == nil || trustRoot.Fingerprint == "" {
		return VerificationReport{}, errors.New("an explicit trusted public key and fingerprint are required")
	}
	fingerprint, err := PublicKeyFingerprint(trustRoot.PublicKey)
	if err != nil {
		return VerificationReport{}, err
	}
	if fingerprint != trustRoot.Fingerprint {
		return VerificationReport{}, errors.New("trusted public key fingerprint does not match the supplied trust root")
	}
	hash := crypto.SHA256
	if _, ok := trustRoot.PublicKey.(ed25519.PublicKey); ok {
		hash = 0
	}
	verifier, err := signature.LoadVerifier(trustRoot.PublicKey, hash)
	if err != nil {
		return VerificationReport{}, fmt.Errorf("load Sigstore public-key verifier: %w", err)
	}
	if err := verifier.VerifySignature(bytes.NewReader(manifest.Signature), bytes.NewReader(manifest.CanonicalJSON)); err != nil {
		return VerificationReport{}, fmt.Errorf("verify detached manifest signature: %w", err)
	}

	if filepath.Base(manifest.PayloadPath) != "payload.tar.zst" {
		return VerificationReport{}, errors.New("payload path must name payload.tar.zst")
	}
	pathInfo, err := os.Lstat(manifest.PayloadPath)
	if err != nil {
		return VerificationReport{}, fmt.Errorf("stat payload path: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return VerificationReport{}, errors.New("payload must be a regular, non-symlink file")
	}
	payload, err := os.Open(manifest.PayloadPath)
	if err != nil {
		return VerificationReport{}, fmt.Errorf("open payload: %w", err)
	}
	defer payload.Close()
	info, err := payload.Stat()
	if err != nil {
		return VerificationReport{}, fmt.Errorf("stat payload: %w", err)
	}
	if !info.Mode().IsRegular() {
		return VerificationReport{}, errors.New("payload must be a regular file")
	}
	if info.Size() > manifest.Payload.MaxCompressedBytes {
		return VerificationReport{}, errors.New("payload exceeds maxCompressedBytes")
	}
	digest, size, err := digestReader(ctx, payload, manifest.Payload.MaxCompressedBytes)
	if err != nil {
		return VerificationReport{}, fmt.Errorf("hash payload: %w", err)
	}
	if size != info.Size() || digest != manifest.Payload.Digest {
		return VerificationReport{}, fmt.Errorf("payload digest mismatch: got %s, want %s", digest, manifest.Payload.Digest)
	}
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		return VerificationReport{}, fmt.Errorf("rewind payload: %w", err)
	}
	temporaryRoot, err := os.MkdirTemp("", "opsctl-bundle-verify-")
	if err != nil {
		return VerificationReport{}, fmt.Errorf("create verification directory: %w", err)
	}
	defer os.RemoveAll(temporaryRoot)
	extractPath := filepath.Join(temporaryRoot, "payload")
	extracted, err := safeExtract(ctx, payload, extractPath, ExtractionLimits{MaxFiles: manifest.Payload.MaxFiles, MaxBytes: manifest.Payload.MaxBytes})
	if err != nil {
		return VerificationReport{}, fmt.Errorf("safely extract payload: %w", err)
	}
	if err := comparePayloadFiles(manifest.Payload.Files, extracted.Files); err != nil {
		return VerificationReport{}, err
	}
	return VerificationReport{
		BundleID: manifest.BundleID, Architecture: manifest.Architecture, PayloadDigest: digest,
		SignatureVerified: true, PayloadDigestVerified: true,
		PayloadFilesVerified: extracted.FileCount, PayloadBytesVerified: extracted.TotalBytes,
	}, nil
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
