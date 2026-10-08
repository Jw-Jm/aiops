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
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ops-platform/internal/bundle"
	"ops-platform/internal/bundle/drivers"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/profile"
)

const usage = "usage: opsctl graph lease-recover --config <private-json> | opsctl bundle build --spec <json> --output <new-dir> --signing-key <external-private-key> | opsctl bundle verify --manifest <file> --signature <file> --payload <archive> --key <pubkey> | opsctl bundle import --profile <resolved> --bundle <dir> --key <pubkey> | opsctl install --profile core --resolved <file> --bundle <dir> --key <pubkey> --business-values <operator-values> --offline | opsctl profile detect --context <name> -o <file> | opsctl profile resolve -f <file> -o <file> | opsctl openbao status|init|unseal|configure --profile <resolved>"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) >= 2 && args[0] == "bootstrap" && args[1] == "graph-leases" {
		return runBootstrapGraphLeases(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bootstrap" && args[1] == "victoria-trust" {
		return runBootstrapVictoria(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "addon" && args[1] == "install" {
		return runMetricsAddon(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bootstrap" && args[1] == "metrics-trust" {
		return runBootstrapMetricsTrust(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bootstrap" && args[1] == "oidc-administrator" {
		return runBootstrapOIDCAdministrator(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bootstrap" && args[1] == "archive-iam" {
		return runBootstrapArchiveIAM(args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bootstrap" && args[1] == "archive-bucket" {
		return runBootstrapArchive(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bootstrap" && args[1] == "oidc-realm" {
		return runBootstrapOIDC(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bootstrap" && args[1] == "database-logins" {
		return runBootstrapDatabase(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bootstrap" && args[1] == "environment" {
		return runBootstrapEnvironment(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bootstrap" && args[1] == "first-tenant" {
		return runBootstrapTenant(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "graph" && args[1] == "lease-recover" {
		return runGraphRecovery(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "helm-render-owned" {
		return runHelmRenderer(args[1:], os.Stdin, stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bundle" && args[1] == "build" {
		return runBuildBundle(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "install" {
		return runOffline(ctx, args[1:], true, stdout, stderr)
	}
	if len(args) > 1 && args[0] == "bundle" && args[1] == "import" {
		return runOffline(ctx, args[2:], false, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "openbao" {
		return runOpenBao(ctx, args[1:], stdout, stderr)
	}
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
	catalogPathFlag := flags.String("catalog", "bundle/component-catalog.yaml", "local authenticated Component Catalog path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *outputPath == "" {
		return errors.New("profile operation requires -o <file>")
	}
	catalogPath := *catalogPathFlag
	switch operation {
	case "detect":
		if *contextName == "" {
			return errors.New("usage: opsctl profile detect --context <name> [-f <template>] -o <file>")
		}
		discovery, err := profile.Discover(ctx, *contextName, "", catalogPath)
		if err != nil {
			return err
		}
		template := *inputPath
		if template == "" {
			template = "deploy/profiles/kubernetes-containerd.yaml"
			if discovery.Kubernetes.Distribution == "orbstack" {
				template = "deploy/profiles/dev-orbstack.yaml"
			}
		}
		input, err := profile.ReadProfileFile(template)
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
		if resolved.Kubernetes.Distribution == "orbstack" {
			driver, err := (drivers.OrbStackSharedStore{}).Probe(ctx, resolved)
			if err != nil {
				return err
			}
			resolved.Runtime.ImageImporter = driver
		} else if resolved.Runtime.ImageImporter == "containerd_ctr" {
			if _, err := (&drivers.PreloadedContainerd{}).Probe(ctx, resolved); err != nil {
				return err
			}
		} else {
			return errors.New("CAPABILITY_DISABLED: generic Kubernetes requires Ready Linux/containerd nodes")
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

func runOpenBao(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: opsctl openbao status|init|unseal|configure --profile <resolved>")
	}
	operation := args[0]
	if operation != "status" && operation != "init" && operation != "unseal" && operation != "configure" {
		return errors.New("usage: opsctl openbao status|init|unseal|configure --profile <resolved>")
	}
	flags := flag.NewFlagSet("opsctl openbao "+operation, flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "", "resolved Deployment Profile")
	caFile := flags.String("ca-file", "", "trusted bootstrap CA file outside the repository and Bundle")
	recoveryFile := flags.String("recovery-file", "", "0600 recovery file outside the repository and Bundle")
	bundleDirectory := flags.String("bundle-dir", "", "Bundle directory to exclude from recovery file paths")
	shareIndex := flags.Int("share-index", -1, "zero-based Shamir share index for unseal")
	investigationSigning := flags.Bool("investigation-signing", false, "configure the nonexportable SP06 Context signing key during configure")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *profilePath == "" || *caFile == "" {
		return errors.New("openbao command requires --profile <resolved> --ca-file <path>")
	}
	if operation != "status" && *recoveryFile == "" {
		return errors.New("openbao init|unseal|configure requires --recovery-file <0600-path>")
	}
	resolved, err := readResolvedProfile(*profilePath)
	if err != nil {
		return err
	}
	component, ok := resolved.Components["openbao"]
	if !ok || component.Mode != "bundled" || component.Endpoint == "" {
		return errors.New("OPENBAO_PROFILE_INVALID: resolved profile must select a bundled OpenBao endpoint")
	}
	if operation != "status" && resolved.Environment != "development" {
		return errors.New("OPENBAO_PRODUCTION_GATE_UNRESOLVED: production seal, HA, and auto-unseal decisions are not accepted")
	}
	contextName := resolved.Kubernetes.Context
	if contextName == "" {
		return errors.New("OPENBAO_PROFILE_INVALID: resolved Kubernetes context is required")
	}
	service, namespace, serverName, serviceDomain, err := parseOpenBaoEndpoint(component.Endpoint)
	if err != nil {
		return err
	}
	repositoryRoot, err := platformRepositoryRoot()
	if err != nil {
		return err
	}
	if *recoveryFile != "" && !outsideGitTree(filepath.Dir(*recoveryFile)) {
		return errors.New("OpenBao recovery material must remain outside every Git checkout")
	}
	if *recoveryFile != "" {
		if _, err := os.Lstat(*recoveryFile); err == nil && !outsideGitTree(*recoveryFile) {
			return errors.New("OpenBao recovery material must remain outside every Git checkout")
		} else if err != nil && !os.IsNotExist(err) {
			return errors.New("OpenBao recovery material path is unavailable")
		}
	}
	if *bundleDirectory == "" {
		*bundleDirectory = filepath.Join(repositoryRoot, "bundle")
	}
	if operation == "init" {
		if err := openbao.EnsureBootstrapTLS(ctx, contextName, namespace, service, *caFile, repositoryRoot, *bundleDirectory); err != nil {
			return err
		}
	}
	caPEM, err := openbao.ReadBootstrapCA(*caFile, repositoryRoot, *bundleDirectory)
	if err != nil {
		return err
	}
	var recovery openbao.RecoveryMaterial
	if operation != "init" && *recoveryFile != "" {
		recovery, err = openbao.ReadExternalRecoveryMaterial(*recoveryFile, repositoryRoot, *bundleDirectory)
		if err != nil {
			return err
		}
	}
	if err := waitOpenBaoPod(ctx, contextName, namespace); err != nil {
		return err
	}
	localAddress, stopPortForward, err := startOpenBaoPortForward(ctx, contextName, namespace, service)
	if err != nil {
		return err
	}
	defer stopPortForward()
	client, err := openbao.NewClient(openbao.ClientConfig{
		Address:         localAddress,
		ServerName:      serverName,
		CACertBundle:    caPEM,
		Token:           recovery.RootToken,
		RepositoryRoot:  repositoryRoot,
		BundleDirectory: *bundleDirectory,
		ServiceDomain:   serviceDomain,
		ExpectedVersion: component.Version,
	})
	if err != nil {
		return err
	}

	switch operation {
	case "init":
		if err := client.Initialize(ctx, *recoveryFile); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "OpenBao initialized; state=sealed; key-shares=3; key-threshold=2; recovery material stored at %s\n", *recoveryFile)
		return err
	case "status":
		status, err := client.Status(ctx)
		if err != nil {
			return err
		}
		return writeOpenBaoStatus(stdout, status)
	case "unseal":
		if *shareIndex < 0 || *shareIndex >= len(recovery.Shares) {
			return errors.New("unseal requires --share-index in the range 0..2")
		}
		status, err := client.Unseal(ctx, recovery.Shares[*shareIndex])
		if err != nil {
			return err
		}
		return writeOpenBaoStatus(stdout, status)
	case "configure":
		if err := client.Configure(ctx); err != nil {
			return err
		}
		if *investigationSigning {
			if err := client.ConfigureInvocationSigning(ctx); err != nil {
				return err
			}
		}
		status, err := client.Status(ctx)
		if err != nil {
			return err
		}
		if status.State != openbao.StateReady {
			return fmt.Errorf("OpenBao configuration did not reach ready state: %s", status.State)
		}
		component.Version = status.Version
		component.Evidence = append(component.Evidence, "OpenBao status API confirmed initialized, unsealed, and configured at version "+status.Version)
		resolved.Components["openbao"] = component
		if err := profile.WriteYAML(*profilePath, resolved); err != nil {
			return fmt.Errorf("write observed OpenBao version to resolved profile: %w", err)
		}
		return writeOpenBaoStatus(stdout, status)
	default:
		return errors.New("usage: opsctl openbao status|init|unseal|configure --profile <resolved>")
	}
}

func readResolvedProfile(path string) (profile.ResolvedProfile, error) {
	return profile.ReadResolvedProfileFile(path)
}

func parseOpenBaoEndpoint(endpoint string) (service, namespace, serverName, serviceDomain string, err error) {
	parsed, parseErr := url.Parse(endpoint)
	if parseErr != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return "", "", "", "", errors.New("OPENBAO_PROFILE_INVALID: OpenBao endpoint must be HTTPS")
	}
	parts := strings.Split(parsed.Hostname(), ".")
	if len(parts) < 4 || parts[2] != "svc" || parts[3] != "cluster" {
		return "", "", "", "", errors.New("OPENBAO_PROFILE_INVALID: OpenBao endpoint must identify a Kubernetes Service DNS name")
	}
	return parts[0], parts[1], parsed.Hostname(), parts[1] + ".svc.cluster.local", nil
}

func platformRepositoryRoot() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current working directory: %w", err)
	}
	command := exec.Command("git", "-C", workingDirectory, "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		// A distributed, verified source/material archive deliberately has no
		// .git directory or Git executable. Keep a concrete exclusion boundary
		// without introducing Git as an offline runtime dependency.
		for root := workingDirectory; ; root = filepath.Dir(root) {
			complete := true
			for _, name := range []string{"go.mod", "bundle/component-catalog.yaml", "deploy/profiles/kubernetes-containerd.yaml"} {
				info, statErr := os.Stat(filepath.Join(root, name))
				complete = complete && statErr == nil && info.Mode().IsRegular()
			}
			if complete {
				return root, nil
			}
			if filepath.Dir(root) == root {
				break
			}
		}
		return "", errors.New("opsctl openbao requires the platform checkout or extracted authenticated installer source/material root")
	}
	return strings.TrimSpace(string(output)), nil
}

func waitOpenBaoPod(ctx context.Context, kubeContext, namespace string) error {
	command := exec.CommandContext(ctx, "kubectl", "--context", kubeContext, "--namespace", namespace, "wait", "--for=condition=Ready", "pod/ops-openbao-0", "--timeout=3m")
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		return errors.New("OPENBAO_UNAVAILABLE: OpenBao pod did not become Ready")
	}
	return nil
}

func startOpenBaoPortForward(ctx context.Context, kubeContext, namespace, service string) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("allocate local OpenBao port: %w", err)
	}
	localPort := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return "", nil, fmt.Errorf("release local OpenBao port: %w", err)
	}
	command := exec.CommandContext(ctx, "kubectl", "--context", kubeContext, "--namespace", namespace, "port-forward", "--address", "127.0.0.1", "service/"+service, fmt.Sprintf("%d:8200", localPort))
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		return "", nil, fmt.Errorf("start OpenBao port-forward: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stop := func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 150*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return fmt.Sprintf("https://127.0.0.1:%d", localPort), stop, nil
		}
		select {
		case err := <-done:
			if err == nil {
				err = errors.New("port-forward exited before the local listener became ready")
			}
			if command.Process != nil {
				_ = command.Process.Kill()
			}
			return "", nil, fmt.Errorf("OPENBAO_UNAVAILABLE: port-forward failed: %w", err)
		case <-deadline.C:
			stop()
			return "", nil, errors.New("OPENBAO_UNAVAILABLE: port-forward did not become ready")
		case <-ticker.C:
		case <-ctx.Done():
			stop()
			return "", nil, ctx.Err()
		}
	}
}

func writeOpenBaoStatus(writer io.Writer, status openbao.Status) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(status)
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
