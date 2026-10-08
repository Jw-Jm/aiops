package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"ops-platform/internal/bootstrap"
)

func runBootstrapArchiveIAM(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap archive-iam", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("secrets-file", "", "private explicit IAM input")
	output := flags.String("output", "", "new private repository-external IAM JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *input == "" || *output == "" {
		return errors.New("Archive IAM preparation requires --secrets-file and --output")
	}
	info, err := os.Stat(*input)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(*input) {
		return errors.New("Archive IAM input requires private repository-external file")
	}
	raw, err := readBoundedFile(*input, 1<<20)
	if err != nil {
		return errors.New("Archive IAM input unavailable")
	}
	var config bootstrap.ArchiveIAMInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("Archive IAM input invalid")
	}
	iam, err := bootstrap.BuildArchiveIAM(config)
	if err != nil {
		return err
	}
	// Resolve the existing parent instead of following an output symlink. O_EXCL
	// preserves existing recovery material and never truncates a private file.
	if !outsideGitTree(filepath.Dir(*output) + string(os.PathSeparator) + ".") {
		return errors.New("Archive IAM output must remain outside Git")
	}
	f, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("Archive IAM output must be new and writable")
	}
	_, writeErr := f.Write(append(iam, '\n'))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("partial private Archive IAM output retained")
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"bucket": config.Bucket, "tenantCount": len(config.Credentials.Tenants), "roleCount": 4 * len(config.Credentials.Tenants), "output": *output, "sourceActivation": "pending installation of explicit IAM and actual role/scope verification"})
}
