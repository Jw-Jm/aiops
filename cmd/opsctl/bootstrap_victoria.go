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
	"strings"

	"github.com/google/uuid"
	"ops-platform/internal/bootstrap"
	"ops-platform/internal/bundle"
)

func runBootstrapVictoria(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap victoria-trust", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "", "explicit actual current resolved Profile")
	businessPath := flags.String("business-values", "", "validated current installation values")
	privatePath := flags.String("secrets-file", "", "external private native source trust/credentials")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *profilePath == "" || *businessPath == "" || *privatePath == "" {
		return errors.New("victoria-trust requires --profile, --business-values and --secrets-file")
	}
	p, err := readResolvedProfile(*profilePath)
	if err != nil {
		return err
	}
	f, err := os.Open(*businessPath)
	if err != nil {
		return errors.New("explicit current values unavailable")
	}
	business, err := bundle.ReadBusinessValues(f)
	f.Close()
	if err != nil {
		return err
	}
	targets, err := business.VictoriaTrustTargets(p)
	if err != nil || len(targets) != 2 {
		return errors.New("two current bundled HTTPS Victoria sources required")
	}
	info, err := os.Stat(*privatePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(*privatePath) {
		return errors.New("Victoria private input must be an external private regular file")
	}
	raw, err := readBoundedFile(*privatePath, 4<<20)
	if err != nil {
		return errors.New("Victoria private input unavailable")
	}
	var config bootstrap.VictoriaTrustInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("strict versioned native Victoria input required")
	}
	if err := config.Validate(business.Namespace()); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "namespace", business.Namespace(), "-o", "json")
	raw, err = command.Output()
	var namespace struct {
		Metadata struct {
			UID    string
			Labels map[string]string
		}
	}
	if err != nil || json.Unmarshal(raw, &namespace) != nil || namespace.Metadata.UID == "" || namespace.Metadata.Labels["ops.platform.io/managed-by"] != "opsctl-bootstrap" {
		return errors.New("formal namespace bootstrap required")
	}
	installationID := namespace.Metadata.Labels["ops.platform.io/installation-id"]
	if _, err := uuid.Parse(installationID); err != nil {
		return errors.New("current installation identity missing")
	}
	secrets := []bootstrap.EnvironmentSecret{}
	clientData := map[string]string{}
	for _, target := range targets {
		var source bootstrap.VictoriaTrust
		for _, candidate := range config.Sources {
			if candidate.Name == target.Name {
				source = candidate
			}
		}
		secrets = append(secrets, bootstrap.EnvironmentSecret{Name: target.ServerSecret, Data: source.ServerData()})
		clientData[target.CAKey], clientData[target.CredentialKey] = source.CA, source.ClientCredential()
	}
	secrets = append(secrets, bootstrap.EnvironmentSecret{Name: business.SourceCredentialsSecret(), Data: clientData})
	// Check every destination before any mutation; never overwrite another
	// initialization's TLS/credentials, even when its namespace name matches.
	for _, secret := range secrets {
		command = exec.CommandContext(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", business.Namespace(), "get", "secret", secret.Name, "--ignore-not-found=true", "-o", "jsonpath={.metadata.uid}")
		uid, err := command.Output()
		if err != nil || strings.TrimSpace(string(uid)) != "" {
			return errors.New("Victoria trust refuses existing or inaccessible Secret")
		}
	}
	receipt := map[string]any{"namespace": business.Namespace(), "namespaceUid": namespace.Metadata.UID, "installationId": installationID, "authentication": "native Basic over independent TLS", "sourceScopeActivation": "pending formal Registry and bounded positive/negative proof"}
	created := []map[string]string{}
	client := environmentKubectl{context: p.Kubernetes.Context}
	for _, secret := range secrets {
		uid, err := client.Create(ctx, map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": secret.Name, "namespace": business.Namespace(), "labels": map[string]string{"ops.platform.io/managed-by": "opsctl-bootstrap", "ops.platform.io/installation-id": installationID}}, "type": "Opaque", "stringData": secret.Data})
		if err != nil {
			receipt["createdSecrets"], receipt["state"] = created, "partial-retained"
			json.NewEncoder(stdout).Encode(receipt)
			return errors.New("Victoria trust partial resources retained for inspection")
		}
		created = append(created, map[string]string{"name": secret.Name, "uid": uid})
	}
	receipt["createdSecrets"] = created
	return json.NewEncoder(stdout).Encode(receipt)
}
