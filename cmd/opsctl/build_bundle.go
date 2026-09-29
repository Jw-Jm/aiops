package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"ops-platform/internal/bundle"
)

func runBuildBundle(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bundle build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	specPath := flags.String("spec", "", "local prepared-input JSON inventory")
	output := flags.String("output", "", "new Bundle directory")
	keyPath := flags.String("signing-key", "", "0600 PKCS#8 signing key outside repository and Bundle")
	architecture := flags.String("architecture", "", "required inventory architecture")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *specPath == "" || *output == "" || *keyPath == "" {
		return errors.New("usage: opsctl bundle build --spec <json> --output <new-dir> --signing-key <external-private-key> [--architecture linux/arm64]")
	}
	// The key is an operator-supplied secret, never a repository artifact.
	repository, err := platformRepositoryRoot()
	if err != nil {
		return err
	}
	if err := ensureTrustKeyIsExternal(repository, *keyPath); err != nil {
		return fmt.Errorf("signing key must be outside the repository: %w", err)
	}
	parentPath, err := filepath.Abs(filepath.Dir(*output))
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(parentPath)
	if err != nil {
		return fmt.Errorf("resolve output parent: %w", err)
	}
	keyPathAbsolute, err := filepath.Abs(*keyPath)
	if err != nil {
		return err
	}
	key, err := filepath.EvalSymlinks(keyPathAbsolute)
	if err != nil {
		return err
	}
	outputPath := filepath.Join(parent, filepath.Base(*output))
	relative, err := filepath.Rel(outputPath, key)
	if err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return errors.New("signing key must be outside the Bundle output")
	}
	raw, err := readBoundedFile(*specPath, maxCLIManifestBytes)
	if err != nil {
		return err
	}
	spec, err := bundle.ParseBuildSpec(raw)
	if err != nil {
		return err
	}
	if *architecture != "" && spec.Architecture != *architecture {
		return errors.New("build spec architecture does not match requested architecture")
	}
	report, err := bundle.Build(ctx, spec, filepath.Dir(*specPath), outputPath, *keyPath)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(report)
}
