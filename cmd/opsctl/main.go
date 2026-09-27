package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ops-platform/internal/bundle"
	"ops-platform/internal/profile"
)

const usage = "usage: opsctl bundle verify --manifest <file> --signature <file> --payload <archive> --key <pubkey> | opsctl profile detect --context <name> -o <file> | opsctl profile resolve -f <file> -o <file>"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "profile" {
		return runProfile(ctx, args[1:], stdout, stderr)
	}
	if len(args) < 2 || args[0] != "bundle" || args[1] != "verify" {
		return errors.New(usage)
	}
	flags := flag.NewFlagSet("opsctl bundle verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "signed Bundle lock JSON")
	signaturePath := flags.String("signature", "", "detached base64 signature")
	payloadPath := flags.String("payload", "", "compressed Bundle payload")
	keyPath := flags.String("key", "", "independently trusted PEM public key")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *manifestPath == "" || *signaturePath == "" || *payloadPath == "" || *keyPath == "" {
		return errors.New(usage)
	}
	if filepath.Base(*manifestPath) != "bundle.lock.json" || filepath.Base(*signaturePath) != "bundle.lock.sig" || filepath.Base(*payloadPath) != "payload.tar.zst" {
		return errors.New("Bundle files must use bundle.lock.json, bundle.lock.sig, and payload.tar.zst names")
	}
	manifestDir, err := filepath.Abs(filepath.Dir(*manifestPath))
	if err != nil {
		return fmt.Errorf("resolve manifest directory: %w", err)
	}
	for _, filePath := range []string{*signaturePath, *payloadPath} {
		directory, err := filepath.Abs(filepath.Dir(filePath))
		if err != nil || directory != manifestDir {
			return errors.New("manifest, signature, and payload must share one Bundle directory")
		}
	}
	if err := ensureTrustKeyIsExternal(manifestDir, *keyPath); err != nil {
		return err
	}
	rawManifest, err := readBoundedFile(*manifestPath, maxCLIManifestBytes)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	manifest, err := bundle.ParseManifest(rawManifest)
	if err != nil {
		return err
	}
	signatureText, err := readBoundedFile(*signaturePath, maxCLISignatureBytes)
	if err != nil {
		return fmt.Errorf("read signature: %w", err)
	}
	signatureBytes, err := decodeBase64Signature(signatureText)
	if err != nil {
		return fmt.Errorf("decode detached signature: %w", err)
	}
	keyFile, err := os.Open(*keyPath)
	if err != nil {
		return fmt.Errorf("open trusted public key: %w", err)
	}
	trustRoot, keyErr := bundle.ReadTrustRoot(keyFile)
	closeErr := keyFile.Close()
	if keyErr != nil {
		return keyErr
	}
	if closeErr != nil {
		return fmt.Errorf("close trusted public key: %w", closeErr)
	}
	manifest.Signature = signatureBytes
	manifest.PayloadPath = *payloadPath
	if err := checkPayloadDigestFile(filepath.Join(manifestDir, "payload.sha256"), manifest.Payload.Digest); err != nil {
		return err
	}
	report, err := bundle.Verify(ctx, manifest, trustRoot)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("write verification report: %w", err)
	}
	return nil
}

func runProfile(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: opsctl profile detect|resolve")
	}
	operation := args[0]
	flags := flag.NewFlagSet("opsctl profile "+operation, flag.ContinueOnError)
	flags.SetOutput(stderr)
	contextName := flags.String("context", "", "Kubernetes context for detection")
	inputPath := flags.String("f", "", "detected or operator-edited Deployment Profile")
	outputPath := flags.String("o", "", "profile output path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *outputPath == "" {
		return errors.New("profile operation requires -o <file>")
	}
	catalogPath := "bundle/component-catalog.yaml"
	switch operation {
	case "detect":
		if *contextName == "" || *inputPath != "" {
			return errors.New("usage: opsctl profile detect --context <name> -o <file>")
		}
		input, err := profile.ReadProfileFile("deploy/profiles/dev-orbstack.yaml")
		if err != nil {
			return err
		}
		discovery, err := profile.Discover(ctx, *contextName, "", catalogPath)
		if err != nil {
			return err
		}
		detected, err := profile.Detect(input, discovery)
		if err != nil {
			return err
		}
		if err := profile.WriteYAML(*outputPath, detected); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "detected Deployment Profile written to %s\n", *outputPath)
		return err
	case "resolve":
		if *inputPath == "" || *contextName != "" {
			return errors.New("usage: opsctl profile resolve -f <detected-profile> -o <file>")
		}
		input, err := profile.ReadProfileFile(*inputPath)
		if err != nil {
			return err
		}
		if input.Kind == "template" {
			return errors.New("PROFILE_INPUT_NOT_DETECTED: repository templates cannot be used directly; run profile detect first")
		}
		resolvedContext := input.Context
		if resolvedContext == "" {
			resolvedContext = input.Kubernetes.Context
		}
		discovery, err := profile.Discover(ctx, resolvedContext, "", catalogPath)
		if err != nil {
			return err
		}
		resolved, err := profile.Resolve(ctx, input, discovery)
		if err != nil {
			return err
		}
		if err := profile.WriteYAML(*outputPath, resolved); err != nil {
			return err
		}
		if !resolved.Installable {
			_, err = fmt.Fprintf(stdout, "resolved Deployment Profile written to %s; installability is blocked by candidate Component Catalog entries\n", *outputPath)
			return err
		}
		_, err = fmt.Fprintf(stdout, "resolved Deployment Profile written to %s\n", *outputPath)
		return err
	default:
		return errors.New("usage: opsctl profile detect|resolve")
	}
}

const (
	maxCLIManifestBytes  = 1 << 20
	maxCLISignatureBytes = 16 << 10
)

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%q exceeds the %d byte limit", path, limit)
	}
	return data, nil
}

func decodeBase64Signature(raw []byte) ([]byte, error) {
	encoded := strings.TrimSpace(string(raw))
	if encoded == "" {
		return nil, errors.New("signature is empty")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func checkPayloadDigestFile(path, expected string) error {
	contents, err := readBoundedFile(path, 256)
	if err != nil {
		return fmt.Errorf("read payload.sha256: %w", err)
	}
	actual := strings.TrimSpace(string(contents))
	if len(actual) != 71 || !strings.HasPrefix(actual, "sha256:") {
		return errors.New("payload.sha256 must contain a sha256:<hex> digest")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(actual, "sha256:")); err != nil {
		return fmt.Errorf("payload.sha256 is invalid: %w", err)
	}
	if actual != expected {
		return fmt.Errorf("payload.sha256 does not match signed manifest digest %s", expected)
	}
	return nil
}

func ensureTrustKeyIsExternal(bundleDirectory, keyPath string) error {
	resolvedBundle, err := filepath.EvalSymlinks(bundleDirectory)
	if err != nil {
		return fmt.Errorf("resolve Bundle directory: %w", err)
	}
	resolvedKey, err := filepath.EvalSymlinks(keyPath)
	if err != nil {
		return fmt.Errorf("resolve trusted key path: %w", err)
	}
	relative, err := filepath.Rel(resolvedBundle, resolvedKey)
	if err != nil {
		return fmt.Errorf("compare trusted key path: %w", err)
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return errors.New("trusted public key must be stored outside the Bundle directory")
	}
	return nil
}
