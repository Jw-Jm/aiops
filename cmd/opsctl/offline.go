package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"ops-platform/internal/bundle"
	"ops-platform/internal/bundle/drivers"
)

func runOffline(ctx context.Context, args []string, install bool, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl offline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	selected := flags.String("profile", "", "resolved Profile path for import, or core for install")
	resolvedPath := flags.String("resolved", "", "resolved Profile path for install")
	bundlePath := flags.String("bundle", "", "local verified Bundle directory")
	keyPath := flags.String("key", "", "independently trusted public key outside Bundle")
	offline := flags.Bool("offline", false, "require offline installation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *bundlePath == "" || *keyPath == "" || *selected == "" {
		return errors.New("requires --profile, --bundle, --key")
	}
	profilePath := *selected
	if install {
		if *selected != "core" || *resolvedPath == "" || !*offline {
			return errors.New("usage: opsctl install --profile core --resolved <file> --bundle <dir> --key <pubkey> --offline")
		}
		profilePath = *resolvedPath
	} else if *resolvedPath != "" {
		return errors.New("--resolved is only used by install")
	}
	p, err := readResolvedProfile(profilePath)
	if err != nil {
		return err
	}
	if err := ensureTrustKeyIsExternal(*bundlePath, *keyPath); err != nil {
		return err
	}
	raw, err := readBoundedFile(filepath.Join(*bundlePath, "bundle.lock.json"), maxCLIManifestBytes)
	if err != nil {
		return err
	}
	manifest, err := bundle.ParseManifest(raw)
	if err != nil {
		return err
	}
	signatureText, err := readBoundedFile(filepath.Join(*bundlePath, "bundle.lock.sig"), maxCLISignatureBytes)
	if err != nil {
		return err
	}
	manifest.Signature, err = decodeBase64Signature(signatureText)
	if err != nil {
		return err
	}
	manifest.PayloadPath = filepath.Join(*bundlePath, "payload.tar.zst")
	keyFile, err := os.Open(*keyPath)
	if err != nil {
		return err
	}
	trust, keyErr := bundle.ReadTrustRoot(keyFile)
	closeErr := keyFile.Close()
	if keyErr != nil {
		return keyErr
	}
	if closeErr != nil {
		return closeErr
	}
	var report bundle.ImportReport
	runtime := drivers.OrbStackSharedStore{}
	if install {
		report, err = bundle.Install(ctx, manifest, trust, p, runtime, drivers.Run)
	} else {
		report, err = bundle.Import(ctx, manifest, trust, p, runtime)
	}
	if err != nil {
		return err
	}
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return fmt.Errorf("write import report: %w", err)
	}
	return nil
}
