package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"ops-platform/internal/bundle"
	"ops-platform/internal/bundle/drivers"
)

func runCurrentCleanup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f := flag.NewFlagSet("opsctl cleanup", flag.ContinueOnError)
	f.SetOutput(stderr)
	profilePath := f.String("profile", "", "installed resolved Profile")
	valuesPath := f.String("business-values", "", "installed explicit business values")
	path := f.String("bundle", "", "installed signed Bundle directory")
	keyPath := f.String("key", "", "independently trusted external public key")
	apply := f.Bool("apply", false, "remove only verified current Helm releases and stage receipts, retaining storage and bootstrap state")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *profilePath == "" || *valuesPath == "" || *path == "" || *keyPath == "" {
		return errors.New("cleanup requires --profile, --business-values, --bundle and --key; defaults to read-only planning")
	}
	p, err := readResolvedProfile(*profilePath)
	if err != nil {
		return err
	}
	values, err := os.Open(*valuesPath)
	if err != nil {
		return err
	}
	b, err := bundle.ReadBusinessValues(values)
	closeErr := values.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = ensureTrustKeyIsExternal(*path, *keyPath); err != nil {
		return err
	}
	raw, err := readBoundedFile(filepath.Join(*path, "bundle.lock.json"), maxCLIManifestBytes)
	if err != nil {
		return err
	}
	m, err := bundle.ParseManifest(raw)
	if err != nil {
		return err
	}
	signature, err := readBoundedFile(filepath.Join(*path, "bundle.lock.sig"), maxCLISignatureBytes)
	if err != nil {
		return err
	}
	m.Signature, err = decodeBase64Signature(signature)
	if err != nil {
		return err
	}
	m.PayloadPath = filepath.Join(*path, "payload.tar.zst")
	key, err := os.Open(*keyPath)
	if err != nil {
		return err
	}
	trust, err := bundle.ReadTrustRoot(key)
	closeErr = key.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	report, err := bundle.CleanupCurrent(ctx, m, trust, p, b, drivers.Run, *apply)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(report)
}
