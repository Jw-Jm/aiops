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
	businessPath := flags.String("business-values", "", "explicit installation-business-values/v1 operator input for SP04–SP06")
	foundationOnly := flags.Bool("foundation-only", false, "explicit historical foundation deployment; does not deliver SP04–SP06")
	stage := flags.String("stage", "", "current initialization phase: dependencies, bootstrap-api or business")
	tokenPath := flags.String("registration-token-file", "", "repository-external private operator token for formal source binding verification")
	offline := flags.Bool("offline", false, "require offline installation")
	nodeName := flags.String("node-name", "", "containerd import-only: explicit local Kubernetes node name")
	nodeUID := flags.String("node-uid", "", "containerd import-only: independently observed node UID")
	socket := flags.String("containerd-address", "", "containerd import-only: absolute local Unix socket")
	snapshotter := flags.String("snapshotter", "", "containerd import-only: explicit CRI snapshotter")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *bundlePath == "" || *keyPath == "" || *selected == "" {
		return errors.New("requires --profile, --bundle, --key")
	}
	var business bundle.BusinessValues
	if install {
		if (*businessPath == "") == (!*foundationOnly) {
			return errors.New("current install requires --business-values; historical foundation requires explicit --foundation-only (mutually exclusive)")
		}
		if !*foundationOnly && *stage != "dependencies" && *stage != "bootstrap-api" && *stage != "business" {
			return errors.New("current install requires explicit --stage dependencies, bootstrap-api or business")
		}
		if *foundationOnly && *stage != "" {
			return errors.New("stages require current business values")
		}
		if *businessPath != "" {
			f, err := os.Open(*businessPath)
			if err != nil {
				return errors.New("business values file unavailable")
			}
			business, err = bundle.ReadBusinessValues(f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	} else if *businessPath != "" || *foundationOnly || *stage != "" {
		return errors.New("business values and foundation-only are install-only options")
	}
	if *tokenPath != "" && (!install || *foundationOnly || *stage != "business") {
		return errors.New("registration token is only valid for current business activation")
	}
	if install && !*foundationOnly && *stage == "business" && *tokenPath == "" {
		return errors.New("business activation requires --registration-token-file for actual API registrations")
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
	if install && !*foundationOnly && *stage == "business" {
		business, err = verifyInstalledSourceBindings(ctx, p, business, *tokenPath)
		if err != nil {
			return err
		}
	}
	var runtime bundle.RuntimeImporter
	switch p.Runtime.ImageImporter {
	case "orbstack_shared_store":
		if *nodeName != "" || *nodeUID != "" || *socket != "" || *snapshotter != "" {
			return errors.New("containerd node options cannot target OrbStack shared store")
		}
		runtime = drivers.OrbStackSharedStore{}
	case "containerd_ctr":
		if install {
			if *nodeName != "" || *nodeUID != "" || *socket != "" || *snapshotter != "" {
				return errors.New("install verifies all preloaded nodes; node socket options are import-only")
			}
			runtime = &drivers.PreloadedContainerd{}
		} else {
			if *nodeName == "" || *nodeUID == "" || *socket == "" || *snapshotter == "" {
				return errors.New("containerd import requires --node-name --node-uid --containerd-address --snapshotter; run locally on every node")
			}
			runtime = &drivers.LocalContainerd{Address: *socket, NodeName: *nodeName, NodeUID: *nodeUID, Snapshotter: *snapshotter}
		}
	default:
		return errors.New("CAPABILITY_DISABLED: unresolved or unsupported image importer")
	}
	if install {
		if *foundationOnly {
			report, err = bundle.Install(ctx, manifest, trust, p, runtime, drivers.Run)
		} else {
			report, err = bundle.InstallCurrent(ctx, manifest, trust, p, runtime, drivers.Run, business, *stage)
		}
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
