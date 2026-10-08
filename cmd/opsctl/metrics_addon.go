package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/google/uuid"
	"ops-platform/internal/bundle"
	"ops-platform/internal/bundle/drivers"
)

func runMetricsAddon(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl addon install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	name := flags.String("name", "", "explicit addon name")
	resolved := flags.String("resolved", "", "resolved core/deepflow Profile")
	path := flags.String("bundle", "", "local signed Bundle")
	key := flags.String("key", "", "independent external verification key")
	config := flags.String("config", "", "explicit metrics-addon/v1 configuration")
	offline := flags.Bool("offline", false, "forbid online install")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *name != "metrics-server" || *resolved == "" || *path == "" || *key == "" || *config == "" || !*offline {
		return errors.New("addon install requires --name metrics-server --resolved --bundle --key --config --offline")
	}
	c, err := readMetricsConfig(*config)
	if err != nil {
		return err
	}
	p, err := readResolvedProfile(*resolved)
	if err != nil {
		return err
	}
	if err := ensureTrustKeyIsExternal(*path, *key); err != nil {
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
	sig, err := readBoundedFile(filepath.Join(*path, "bundle.lock.sig"), maxCLISignatureBytes)
	if err != nil {
		return err
	}
	m.Signature, err = decodeBase64Signature(sig)
	if err != nil {
		return err
	}
	m.PayloadPath = filepath.Join(*path, "payload.tar.zst")
	f, err := os.Open(*key)
	if err != nil {
		return err
	}
	trust, err := bundle.ReadTrustRoot(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	report, err := bundle.InstallMetricsAddon(ctx, m, trust, p, drivers.OrbStackSharedStore{}, drivers.Run, c)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"import": report, "namespace": c.Namespace, "addon": "metrics-server", "fullR3Acceptance": "pending independent real chain, source authorization, freshness and failure/recovery gates"})
}
func readMetricsConfig(path string) (bundle.MetricsAddonValues, error) {
	raw, err := readBoundedFile(path, 128<<10)
	if err != nil {
		return bundle.MetricsAddonValues{}, errors.New("Metrics config unavailable")
	}
	return bundle.ReadMetricsAddonValues(bytes.NewReader(raw))
}
func runBootstrapMetricsTrust(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap metrics-trust", flag.ContinueOnError)
	flags.SetOutput(stderr)
	config := flags.String("config", "", "explicit public addon configuration")
	secretPath := flags.String("secrets-file", "", "private external serving identity and independent kubelet CA")
	contextName := flags.String("context", "", "explicit Kubernetes context")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *config == "" || *secretPath == "" || *contextName == "" {
		return errors.New("Metrics trust bootstrap requires --config --secrets-file --context")
	}
	c, err := readMetricsConfig(*config)
	if err != nil {
		return err
	}
	info, err := os.Stat(*secretPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(*secretPath) {
		return errors.New("Metrics serving identity input must be a private external file")
	}
	raw, err := readBoundedFile(*secretPath, 128<<10)
	if err != nil {
		return errors.New("Metrics identity unavailable")
	}
	var identity struct {
		Certificate string `json:"certificate"`
		PrivateKey  string `json:"privateKey"`
		KubeletCA   string `json:"kubeletCA"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&identity) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("Metrics identity requires one strict private JSON document")
	}
	if err := bundle.ValidateMetricsAddonIdentity(c, []byte(identity.Certificate), []byte(identity.PrivateKey), identity.KubeletCA); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--context", *contextName, "get", "namespace", c.Namespace, "-o", "json")
	raw, err = cmd.Output()
	var ns struct {
		Metadata struct {
			UID    string            `json:"uid"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err != nil || json.Unmarshal(raw, &ns) != nil || ns.Metadata.UID == "" || ns.Metadata.Labels["ops.platform.io/managed-by"] != "opsctl-bootstrap" {
		return errors.New("Metrics trust namespace must come from formal environment bootstrap")
	}
	id := ns.Metadata.Labels["ops.platform.io/installation-id"]
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("Metrics installation identity unavailable")
	}
	// Preflight both resources before creating either. Existing trust is never
	// adopted or overwritten. A partial creation stays visible for recovery.
	for _, item := range []struct{ kind, name string }{{"secret", c.ServingTLSSecret}, {"configmap", c.KubeletCAConfigMap}} {
		cmd := exec.CommandContext(ctx, "kubectl", "--context", *contextName, "-n", c.Namespace, "get", item.kind, item.name, "--ignore-not-found", "-o", "json")
		out, err := cmd.Output()
		if err != nil || len(bytes.TrimSpace(out)) != 0 {
			return errors.New("Metrics trust resource exists or discovery failed; refusing overwrite")
		}
	}
	labels := map[string]any{"ops.platform.io/managed-by": "opsctl-bootstrap", "ops.platform.io/installation-id": id}
	metadata := func(name string) map[string]any {
		return map[string]any{"name": name, "namespace": c.Namespace, "labels": labels}
	}
	k := environmentKubectl{context: *contextName}
	secretUID, err := k.Create(ctx, map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": metadata(c.ServingTLSSecret), "type": "kubernetes.io/tls", "data": map[string]string{"tls.crt": base64.StdEncoding.EncodeToString([]byte(identity.Certificate)), "tls.key": base64.StdEncoding.EncodeToString([]byte(identity.PrivateKey))}})
	if err != nil {
		return err
	}
	cmUID, err := k.Create(ctx, map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": metadata(c.KubeletCAConfigMap), "data": map[string]string{"ca.crt": identity.KubeletCA}})
	receipt := map[string]any{"namespace": c.Namespace, "namespaceUid": ns.Metadata.UID, "installationId": id, "servingSecretUid": secretUID, "kubeletCAConfigMapUid": cmUID}
	if outErr := json.NewEncoder(stdout).Encode(receipt); outErr != nil {
		return outErr
	}
	return err
}
