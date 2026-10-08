package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ops-platform/internal/bootstrap"
)

func runBootstrapEnvironment(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap environment", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "explicit public environment bootstrap JSON")
	secretPath := flags.String("secrets-file", "", "repository-external private dependency inputs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *configPath == "" || *secretPath == "" {
		return errors.New("bootstrap environment requires --config and --secrets-file")
	}
	var config bootstrap.EnvironmentInput
	var secrets []bootstrap.EnvironmentSecret
	for _, input := range []struct {
		path  string
		value any
		limit int64
	}{{*configPath, &config, 1 << 20}, {*secretPath, &secrets, 4 << 20}} {
		if input.path == *secretPath {
			info, err := os.Stat(input.path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(input.path) {
				return errors.New("bootstrap Secret input must be a private regular file")
			}
		}
		raw, err := readBoundedFile(input.path, input.limit)
		if err != nil {
			return errors.New("bootstrap input unavailable")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(input.value) != nil || decoder.Decode(new(any)) != io.EOF {
			return errors.New("bootstrap input must be one strict JSON document")
		}
	}
	if err := config.Validate(secrets); err != nil {
		return err
	}
	receipt, err := bootstrap.CreateEnvironment(ctx, environmentKubectl{context: config.Context}, config, secrets)
	if receipt.NamespaceUID != "" {
		if outputErr := json.NewEncoder(stdout).Encode(receipt); outputErr != nil {
			return outputErr
		}
	}
	return err
}

// Reject private inputs inside any Git checkout, including worktrees and symlinks.
func outsideGitTree(path string) bool {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return false
	}
	directory := filepath.Dir(absolute)
	if info, err := os.Stat(absolute); err != nil {
		return false
	} else if info.IsDir() {
		directory = absolute
	}
	for ; ; directory = filepath.Dir(directory) {
		if _, err := os.Lstat(filepath.Join(directory, ".git")); err == nil {
			return false
		} else if !os.IsNotExist(err) {
			return false
		}
		if filepath.Dir(directory) == directory {
			return true
		}
	}
}

type environmentKubectl struct{ context string }

func (k environmentKubectl) NamespaceExists(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "--context", k.context, "get", "namespace", name, "--ignore-not-found=true", "-o", "jsonpath={.metadata.uid}")
	output, err := cmd.Output()
	if err != nil {
		return false, errors.New("namespace discovery failed")
	}
	return strings.TrimSpace(string(output)) != "", nil
}
func (k environmentKubectl) Create(ctx context.Context, object map[string]any) (string, error) {
	raw, err := json.Marshal(object)
	if err != nil {
		return "", errors.New("bootstrap resource serialization failed")
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--context", k.context, "create", "-f", "-", "-o", "jsonpath={.metadata.uid}")
	cmd.Stdin = bytes.NewReader(raw)
	output, err := cmd.Output()
	// Never forward kubectl stderr: admission responses can contain Secret data.
	if err != nil {
		return "", errors.New("bootstrap resource creation failed")
	}
	uid := strings.TrimSpace(string(output))
	if uid == "" {
		return "", errors.New("bootstrap resource UID missing")
	}
	return uid, nil
}
