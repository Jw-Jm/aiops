package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"ops-platform/internal/profile"
)

type namespaceIdentity struct{ UID, InstallationID string }
type stageResource struct {
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	UID                 string `json:"uid"`
	Release             string `json:"release"`
	ConfigurationDigest string `json:"configurationDigest,omitempty"`
}

func stageConfigurationDigest(live map[string]any) string {
	return jsonDigest(map[string]any{"spec": live["spec"], "data": live["data"], "type": live["type"]})
}

type currentStageReceipt struct {
	Stage                    string          `json:"stage"`
	BundleID                 string          `json:"bundleId"`
	PayloadDigest            string          `json:"payloadDigest"`
	ProfileDigest            string          `json:"profileDigest"`
	BusinessDigest           string          `json:"businessDigest"`
	RegisteredBusinessDigest string          `json:"registeredBusinessDigest,omitempty"`
	NamespaceUID             string          `json:"namespaceUid"`
	InstallationID           string          `json:"installationId"`
	Resources                []stageResource `json:"resources"`
}

func currentNamespaceIdentity(ctx context.Context, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) (namespaceIdentity, error) {
	var identity namespaceIdentity
	for _, component := range p.Components {
		if component.Mode == "bundled" && component.Namespace != "" && component.Namespace != b.Namespace() {
			return identity, errors.New("bundled component namespace differs from current installation")
		}
	}
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "namespace", b.Namespace(), "-o", "json")
	var ns struct {
		Metadata struct {
			UID    string            `json:"uid"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err != nil || json.Unmarshal(raw, &ns) != nil || ns.Metadata.UID == "" || ns.Metadata.Labels["ops.platform.io/managed-by"] != "opsctl-bootstrap" {
		return identity, errors.New("current namespace must come from formal environment bootstrap")
	}
	id := ns.Metadata.Labels["ops.platform.io/installation-id"]
	if _, err := uuid.Parse(id); err != nil {
		return identity, errors.New("current namespace installation identity missing")
	}
	return namespaceIdentity{ns.Metadata.UID, id}, nil
}
func exactHelmOwner(live map[string]any, namespace, release string) bool {
	metadata := object(live, "metadata")
	annotations := object(metadata, "annotations")
	labels := object(metadata, "labels")
	return textValue(metadata, "uid") != "" && textValue(annotations, "meta.helm.sh/release-name") == release && textValue(annotations, "meta.helm.sh/release-namespace") == namespace && textValue(labels, "app.kubernetes.io/managed-by") == "Helm" && textValue(labels, "ops.platform.io/release") == release
}
func jsonDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func currentDependencyReleases(p profile.ResolvedProfile) []string {
	releases := []string{"ops-dependencies", "ops-platform"}
	for _, pair := range []struct{ Component, Release string }{{"victoriaMetrics", "victoria-metrics"}, {"victoriaLogs", "victoria-logs"}, {"vmalert", "vmalert"}} {
		if p.Components[pair.Component].Mode == "bundled" {
			releases = append(releases, pair.Release)
		}
	}
	return releases
}

// Namespace/Helm ownership and UID snapshots bind the two phases. No business
// table, protected storage or release from another namespace is adopted.
func currentStageInventory(ctx context.Context, p profile.ResolvedProfile, b BusinessValues, run CommandRunner, apiBootstrap ...bool) ([]stageResource, error) {
	var resources []stageResource
	for _, release := range currentDependencyReleases(p) {
		raw, err := run(ctx, "helm", "get", "manifest", release, "--kube-context", p.Kubernetes.Context, "--namespace", b.Namespace())
		if err != nil {
			return nil, errors.New("required dependency release unavailable")
		}
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		count := 0
		for {
			var obj map[string]any
			if err := decoder.Decode(&obj); err == io.EOF {
				break
			} else if err != nil {
				return nil, err
			}
			if len(obj) == 0 {
				continue
			}
			kind, name := textValue(obj, "kind"), textValue(object(obj, "metadata"), "name")
			meta := object(obj, "metadata")
			if !offlineResourceKinds[kind] || name == "" || strings.ContainsAny(name, "/ \\\n\r") || (textValue(meta, "namespace") != "" && textValue(meta, "namespace") != b.Namespace()) || object(meta, "annotations")["helm.sh/hook"] != nil {
				return nil, errors.New("dependency manifest outside fixed scope")
			}
			bootstrap := len(apiBootstrap) == 1 && apiBootstrap[0]
			if release == "ops-platform" && ((!bootstrap && kind != "NetworkPolicy") || (bootstrap && (kind == "StatefulSet" || (kind == "Deployment" && name != "ops-api")))) {
				return nil, errors.New("dependency phase contains business workloads")
			}
			args := []string{"--context", p.Kubernetes.Context, "get", kind, name, "-o", "json"}
			if kind != "ClusterRole" && kind != "ClusterRoleBinding" {
				args = append(args, "-n", b.Namespace())
			}
			liveRaw, err := run(ctx, "kubectl", args...)
			var live map[string]any
			if err != nil || json.Unmarshal(liveRaw, &live) != nil || !exactHelmOwner(live, b.Namespace(), release) {
				return nil, errors.New("dependency resource Helm ownership differs")
			}
			resources = append(resources, stageResource{Kind: kind, Name: name, UID: textValue(object(live, "metadata"), "uid"), Release: release, ConfigurationDigest: stageConfigurationDigest(live)})
			count++
		}
		if count == 0 {
			return nil, errors.New("dependency release manifest empty")
		}
		raw, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "pvc", "--selector=ops.platform.io/release="+release, "-o", "json")
		var claims struct {
			Items []struct {
				Metadata struct {
					Name   string            `json:"name"`
					UID    string            `json:"uid"`
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err != nil || json.Unmarshal(raw, &claims) != nil {
			return nil, errors.New("dependency storage identity unavailable")
		}
		for _, claim := range claims.Items {
			if claim.Metadata.UID == "" || claim.Metadata.Labels["ops.platform.io/release"] != release {
				return nil, errors.New("dependency PVC scope differs")
			}
			resources = append(resources, stageResource{Kind: "PersistentVolumeClaim", Name: claim.Metadata.Name, UID: claim.Metadata.UID, Release: release})
		}
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Release+"/"+resources[i].Kind+"/"+resources[i].Name < resources[j].Release+"/"+resources[j].Kind+"/"+resources[j].Name
	})
	return resources, nil
}
func createCurrentStageReceipt(ctx context.Context, m Manifest, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) error {
	identity, err := currentNamespaceIdentity(ctx, p, b, run)
	if err != nil {
		return err
	}
	resources, err := currentStageInventory(ctx, p, b, run)
	if err != nil {
		return err
	}
	receipt := currentStageReceipt{Stage: "dependencies-ready", BundleID: m.BundleID, PayloadDigest: m.Payload.Digest, ProfileDigest: jsonDigest(p), BusinessDigest: businessInstallationDigest(b), RegisteredBusinessDigest: registeredBusinessInstallationDigest(b), NamespaceUID: identity.UID, InstallationID: identity.InstallationID, Resources: resources}
	raw, _ := json.Marshal(receipt)
	if _, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "create", "configmap", "ops-installation-checkpoint", "--from-literal=receipt.json="+string(raw)); err != nil {
		return fmt.Errorf("checkpoint=record-dependencies: %w", err)
	}
	_, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "label", "configmap", "ops-installation-checkpoint", "ops.platform.io/installation-id="+identity.InstallationID)
	return err
}

func createCurrentAPIBootstrapReceipt(ctx context.Context, m Manifest, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) error {
	identity, err := currentNamespaceIdentity(ctx, p, b, run)
	if err != nil {
		return err
	}
	resources, err := currentStageInventory(ctx, p, b, run, true)
	if err != nil {
		return err
	}
	// The API phase cannot silently replace a dependency or its storage while
	// waiting for readiness. Compare every prior identity to the new snapshot.
	priorRaw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "configmap", "ops-installation-checkpoint", "-o", "json")
	var cm struct {
		Data map[string]string `json:"data"`
	}
	var prior currentStageReceipt
	if err != nil || json.Unmarshal(priorRaw, &cm) != nil || json.Unmarshal([]byte(cm.Data["receipt.json"]), &prior) != nil || prior.Stage != "dependencies-ready" || prior.BundleID != m.BundleID || prior.PayloadDigest != m.Payload.Digest || prior.ProfileDigest != jsonDigest(p) || prior.BusinessDigest != businessInstallationDigest(b) || prior.NamespaceUID != identity.UID || prior.InstallationID != identity.InstallationID {
		return errors.New("dependency checkpoint changed during API initialization")
	}
	current := map[string]stageResource{}
	for _, r := range resources {
		current[r.Release+"/"+r.Kind+"/"+r.Name] = r
	}
	for _, r := range prior.Resources {
		if current[r.Release+"/"+r.Kind+"/"+r.Name] != r {
			return errors.New("dependency resource identity/configuration changed during API initialization")
		}
	}
	receipt := currentStageReceipt{Stage: "bootstrap-api-ready", BundleID: m.BundleID, PayloadDigest: m.Payload.Digest, ProfileDigest: jsonDigest(p), BusinessDigest: businessInstallationDigest(b), RegisteredBusinessDigest: registeredBusinessInstallationDigest(b), NamespaceUID: identity.UID, InstallationID: identity.InstallationID, Resources: resources}
	raw, _ := json.Marshal(receipt)
	if _, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "create", "configmap", "ops-api-bootstrap-checkpoint", "--from-literal=receipt.json="+string(raw)); err != nil {
		return errors.New("API bootstrap checkpoint creation failed")
	}
	_, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "label", "configmap", "ops-api-bootstrap-checkpoint", "ops.platform.io/installation-id="+identity.InstallationID)
	return err
}

func verifyCurrentAPIBootstrapReceipt(ctx context.Context, m Manifest, p profile.ResolvedProfile, b *BusinessValues, run CommandRunner) error {
	if !b.registrationVerified {
		return errors.New("formal API source registration verification required before business activation")
	}
	identity, err := currentNamespaceIdentity(ctx, p, *b, run)
	if err != nil {
		return err
	}
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "configmap", "ops-api-bootstrap-checkpoint", "-o", "json")
	var cm struct {
		Data     map[string]string `json:"data"`
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	var receipt currentStageReceipt
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &cm) != nil || json.Unmarshal([]byte(cm.Data["receipt.json"]), &receipt) != nil || cm.Metadata.Labels["ops.platform.io/installation-id"] != identity.InstallationID {
		return errors.New("formal API bootstrap checkpoint required")
	}
	if receipt.Stage != "bootstrap-api-ready" || receipt.BundleID != m.BundleID || receipt.PayloadDigest != m.Payload.Digest || receipt.ProfileDigest != jsonDigest(p) || receipt.NamespaceUID != identity.UID || receipt.InstallationID != identity.InstallationID || receipt.RegisteredBusinessDigest == "" || receipt.RegisteredBusinessDigest != registeredBusinessInstallationDigest(*b) {
		return errors.New("API bootstrap Bundle/Profile/business scope/namespace binding differs")
	}
	if len(receipt.Resources) == 0 {
		return errors.New("API bootstrap resource snapshot empty")
	}
	allowed := map[string]bool{}
	for _, r := range currentDependencyReleases(p) {
		allowed[r] = true
	}
	identities := map[string]string{}
	apiFound := false
	for _, r := range receipt.Resources {
		if !allowed[r.Release] || (!offlineResourceKinds[r.Kind] && r.Kind != "PersistentVolumeClaim") || r.Name == "" || strings.ContainsAny(r.Name, "/ \\\n\r") || r.UID == "" {
			return errors.New("API bootstrap resource outside installation scope")
		}
		args := []string{"--context", p.Kubernetes.Context, "get", r.Kind, r.Name, "-o", "json"}
		if r.Kind != "ClusterRole" && r.Kind != "ClusterRoleBinding" {
			args = append(args, "-n", b.Namespace())
		}
		raw, err := run(ctx, "kubectl", args...)
		var live map[string]any
		if err != nil || json.Unmarshal(raw, &live) != nil || textValue(object(live, "metadata"), "uid") != r.UID {
			return errors.New("API bootstrap resource or storage UID changed")
		}
		if r.Kind != "PersistentVolumeClaim" && (r.ConfigurationDigest == "" || stageConfigurationDigest(live) != r.ConfigurationDigest) {
			return errors.New("API bootstrap resource configuration changed")
		}
		if r.Kind == "PersistentVolumeClaim" {
			if textValue(object(object(live, "metadata"), "labels"), "ops.platform.io/release") != r.Release {
				return errors.New("API bootstrap storage ownership differs")
			}
		} else if !exactHelmOwner(live, b.Namespace(), r.Release) {
			return errors.New("API bootstrap Helm ownership differs")
		}
		if r.Release == "ops-platform" {
			if r.Kind == "Deployment" {
				if r.Name != "ops-api" {
					return errors.New("API bootstrap unexpectedly contains a business consumer")
				}
				apiFound = true
			}
			key := r.Kind + "/" + r.Name
			if identities[key] != "" {
				return errors.New("API bootstrap resource repeated")
			}
			identities[key] = r.UID
		}
	}
	if !apiFound {
		return errors.New("API bootstrap Deployment identity missing")
	}
	b.bootstrapResources = identities
	return nil
}
func verifyCurrentStageReceipt(ctx context.Context, m Manifest, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) error {
	identity, err := currentNamespaceIdentity(ctx, p, b, run)
	if err != nil {
		return err
	}
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "configmap", "ops-installation-checkpoint", "-o", "json")
	var checkpoint struct {
		Data     map[string]string `json:"data"`
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	var receipt currentStageReceipt
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &checkpoint) != nil || json.Unmarshal([]byte(checkpoint.Data["receipt.json"]), &receipt) != nil || checkpoint.Metadata.Labels["ops.platform.io/installation-id"] != identity.InstallationID {
		return errors.New("formal dependency checkpoint required before business activation")
	}
	if receipt.Stage != "dependencies-ready" || receipt.BundleID != m.BundleID || receipt.PayloadDigest != m.Payload.Digest || receipt.ProfileDigest != jsonDigest(p) || receipt.BusinessDigest != businessInstallationDigest(b) || receipt.NamespaceUID != identity.UID || receipt.InstallationID != identity.InstallationID {
		return errors.New("dependency checkpoint Bundle/Profile/business/namespace binding differs")
	}
	resources, err := currentStageInventory(ctx, p, b, run)
	if err != nil {
		return err
	}
	if len(resources) == 0 || jsonDigest(resources) != jsonDigest(receipt.Resources) {
		return errors.New("dependency resource or storage UID changed before activation")
	}
	return nil
}

// Probe observation times are refreshed after dependency initialization. Only
// these two timestamps are excluded: all identities, scopes, query types,
// budgets, targets and trust references remain bound to the checkpoint.
func businessInstallationDigest(b BusinessValues) string {
	raw, _ := json.Marshal(b.values)
	var snapshot map[string]any
	if json.Unmarshal(raw, &snapshot) != nil {
		return ""
	}
	for _, item := range object(snapshot, "sp04")["sources"].([]any) {
		probe := object(item.(map[string]any), "ScopeProbe")
		delete(probe, "from")
		delete(probe, "to")
	}
	return jsonDigest(snapshot)
}
