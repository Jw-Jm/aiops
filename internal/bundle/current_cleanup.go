package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/profile"
)

type CleanupResource struct {
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	Namespace           string `json:"namespace,omitempty"`
	UID                 string `json:"uid"`
	ResourceVersion     string `json:"resourceVersion"`
	ConfigurationDigest string `json:"configurationDigest,omitempty"`
}
type CurrentCleanupReport struct {
	Namespace        string            `json:"namespace"`
	NamespaceUID     string            `json:"namespaceUid"`
	InstallationID   string            `json:"installationId"`
	BundleID         string            `json:"bundleId"`
	Releases         []string          `json:"releases"`
	Resources        []CleanupResource `json:"resources"`
	ProtectedClaims  []CleanupResource `json:"protectedClaims"`
	ProtectedVolumes []CleanupResource `json:"protectedVolumes"`
	Checkpoints      []CleanupResource `json:"checkpoints"`
	Applied          bool              `json:"applied"`
}

// CleanupCurrent verifies the installed Bundle and fixed installation scope
// before removing its Helm releases. Namespace, bootstrap credentials, PV/PVC,
// database contents and locked archive objects are retained for reinstallation.
func CleanupCurrent(ctx context.Context, m Manifest, trust TrustRoot, p profile.ResolvedProfile, b BusinessValues, run CommandRunner, apply bool) (CurrentCleanupReport, error) {
	v, err := prepareCleanupArtifact(ctx, m, trust)
	if err != nil {
		return CurrentCleanupReport{}, err
	}
	defer v.close()
	if err = validateInstallProfile(p, v.manifest.Architecture); err != nil {
		return CurrentCleanupReport{}, err
	}
	report, err := planCurrentCleanup(ctx, v.manifest, p, b, run)
	if err != nil || !apply {
		return report, err
	}
	return applyCurrentCleanup(ctx, v.manifest, p, b, run, report)
}

func applyCurrentCleanup(ctx context.Context, m Manifest, p profile.ResolvedProfile, b BusinessValues, run CommandRunner, report CurrentCleanupReport) (CurrentCleanupReport, error) {
	// Reinspect the complete plan immediately before the first mutation.
	current, err := planCurrentCleanup(ctx, m, p, b, run)
	if err != nil || jsonDigest(current) != jsonDigest(report) {
		return report, errors.New("cleanup installation changed after planning")
	}
	for _, release := range report.Releases {
		if _, err = run(ctx, "helm", "uninstall", release, "--kube-context", p.Kubernetes.Context, "--namespace", b.Namespace(), "--wait", "--timeout", "5m"); err != nil {
			return report, fmt.Errorf("cleanup release %s: %w", release, err)
		}
	}
	for _, claim := range append(append([]CleanupResource{}, report.ProtectedClaims...), report.ProtectedVolumes...) {
		args := []string{"--context", p.Kubernetes.Context, "get", claim.Kind, claim.Name, "-o", "json"}
		if claim.Namespace != "" {
			args = append(args, "-n", claim.Namespace)
		}
		raw, e := run(ctx, "kubectl", args...)
		var live map[string]any
		if e != nil || json.Unmarshal(raw, &live) != nil || textValue(object(live, "metadata"), "uid") != claim.UID || jsonDigest(object(live, "spec")) != claim.ConfigurationDigest {
			return report, errors.New("cleanup protected storage identity/configuration changed")
		}
	}
	// Use API UID/resourceVersion preconditions, rather than an unguarded delete
	// or editing a receipt to make a later installation accept different state.
	dir, err := os.MkdirTemp("", "ops-current-cleanup-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(dir)
	for i, checkpoint := range report.Checkpoints {
		body, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]string{"uid": checkpoint.UID, "resourceVersion": checkpoint.ResourceVersion}})
		file := filepath.Join(dir, fmt.Sprintf("delete-%d.json", i))
		if err = os.WriteFile(file, body, 0600); err != nil {
			return report, err
		}
		path := "/api/v1/namespaces/" + b.Namespace() + "/configmaps/" + checkpoint.Name
		if _, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "delete", "--raw="+path, "-f", file); err != nil {
			return report, fmt.Errorf("cleanup checkpoint %s: %w", checkpoint.Name, err)
		}
	}
	report.Applied = true
	return report, nil
}

func cleanupResource(live map[string]any, kind, name, namespace string) (CleanupResource, error) {
	meta := object(live, "metadata")
	r := CleanupResource{Kind: kind, Name: name, Namespace: namespace, UID: textValue(meta, "uid"), ResourceVersion: textValue(meta, "resourceVersion")}
	if r.UID == "" || r.ResourceVersion == "" {
		return r, errors.New("cleanup requires live UID and resourceVersion")
	}
	return r, nil
}

func planCurrentCleanup(ctx context.Context, m Manifest, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) (CurrentCleanupReport, error) {
	var result CurrentCleanupReport
	if run == nil || b.values == nil {
		return result, errors.New("cleanup requires explicit current business values and command runner")
	}
	identity, err := currentNamespaceIdentity(ctx, p, b, run)
	if err != nil {
		return result, err
	}
	result = CurrentCleanupReport{Namespace: b.Namespace(), NamespaceUID: identity.UID, InstallationID: identity.InstallationID, BundleID: m.BundleID, Resources: []CleanupResource{}, ProtectedClaims: []CleanupResource{}, Checkpoints: []CleanupResource{}}
	// Removing the platform first stops consumers before their dependencies.
	result.Releases = []string{"ops-platform"}
	for _, pair := range []struct{ component, release string }{{"vmalert", "vmalert"}, {"victoriaLogs", "victoria-logs"}, {"victoriaMetrics", "victoria-metrics"}} {
		if p.Components[pair.component].Mode == "bundled" {
			result.Releases = append(result.Releases, pair.release)
		}
	}
	result.Releases = append(result.Releases, "ops-dependencies")
	for _, name := range []string{"ops-installation-checkpoint", "ops-api-bootstrap-checkpoint", "ops-command-initialization-checkpoint"} {
		raw, e := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "configmap", name, "--ignore-not-found", "-o", "json")
		if e != nil {
			return result, e
		}
		if len(bytes.TrimSpace(raw)) == 0 && name == "ops-command-initialization-checkpoint" && object(b.values, "sp07") == nil {
			continue
		}
		var live, receipt map[string]any
		if json.Unmarshal(raw, &live) != nil || json.Unmarshal([]byte(textValue(object(live, "data"), "receipt.json")), &receipt) != nil || textValue(object(object(live, "metadata"), "labels"), "ops.platform.io/installation-id") != identity.InstallationID {
			return result, errors.New("cleanup formal checkpoint unavailable or foreign")
		}
		for key, expected := range map[string]string{"bundleId": m.BundleID, "payloadDigest": m.Payload.Digest, "profileDigest": jsonDigest(p), "namespaceUid": identity.UID, "installationId": identity.InstallationID} {
			if textValue(receipt, key) != expected {
				return result, errors.New("cleanup checkpoint Bundle/Profile/installation binding differs")
			}
		}
		stage := map[string]string{"ops-installation-checkpoint": "dependencies-ready", "ops-api-bootstrap-checkpoint": "bootstrap-api-ready", "ops-command-initialization-checkpoint": "command-initialization"}[name]
		if textValue(receipt, "stage") != stage {
			return result, errors.New("cleanup checkpoint stage differs")
		}
		base := b
		if object(b.values, "sp07") != nil {
			base.initializeCommands = true
		}
		if name == "ops-api-bootstrap-checkpoint" && textValue(receipt, "registeredBusinessDigest") != apiBootstrapBusinessDigest(base) {
			return result, errors.New("cleanup API checkpoint business scope differs")
		}
		if name == "ops-command-initialization-checkpoint" && (textValue(receipt, "businessDigest") != registeredBusinessInstallationDigest(b) || textValue(object(object(live, "metadata"), "labels"), "ops.platform.io/managed-by") != "opsctl-bootstrap") {
			return result, errors.New("cleanup command checkpoint business scope differs")
		}
		r, e := cleanupResource(live, "ConfigMap", name, b.Namespace())
		if e != nil {
			return result, e
		}
		result.Checkpoints = append(result.Checkpoints, r)
	}
	seen := map[string]bool{}
	for _, release := range result.Releases {
		raw, e := run(ctx, "helm", "get", "manifest", release, "--kube-context", p.Kubernetes.Context, "--namespace", b.Namespace())
		if e != nil {
			return result, e
		}
		resources, e := currentCleanupReleaseResources(ctx, raw, release, p, b, run)
		if e != nil {
			return result, e
		}
		for _, r := range resources {
			key := r.Kind + "/" + r.Namespace + "/" + r.Name
			if seen[key] {
				return result, errors.New("cleanup releases overlap")
			}
			seen[key] = true
			result.Resources = append(result.Resources, r)
		}
	}
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "pvc", "-o", "json")
	var claims struct {
		Items []map[string]any `json:"items"`
	}
	if err != nil || json.Unmarshal(raw, &claims) != nil {
		return result, errors.New("cleanup storage inventory unavailable")
	}
	for _, live := range claims.Items {
		meta := object(live, "metadata")
		r, e := cleanupResource(live, "PersistentVolumeClaim", textValue(meta, "name"), b.Namespace())
		if e != nil {
			return result, e
		}
		result.ProtectedClaims = append(result.ProtectedClaims, r)
		result.ProtectedClaims[len(result.ProtectedClaims)-1].ConfigurationDigest = jsonDigest(object(live, "spec"))
		volume := textValue(object(live, "spec"), "volumeName")
		if volume == "" {
			return result, errors.New("cleanup requires bound protected storage")
		}
		pvRaw, e := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "pv", volume, "-o", "json")
		var pv map[string]any
		if e != nil || json.Unmarshal(pvRaw, &pv) != nil {
			return result, errors.New("cleanup protected PV unavailable")
		}
		spec := object(pv, "spec")
		ref := object(spec, "claimRef")
		if textValue(spec, "persistentVolumeReclaimPolicy") != "Retain" || textValue(ref, "uid") != r.UID || textValue(ref, "namespace") != b.Namespace() || textValue(ref, "name") != r.Name {
			return result, errors.New("cleanup protected PV binding/retention differs")
		}
		v, e := cleanupResource(pv, "PersistentVolume", volume, "")
		if e != nil {
			return result, e
		}
		v.ConfigurationDigest = jsonDigest(spec)
		result.ProtectedVolumes = append(result.ProtectedVolumes, v)
	}
	return result, nil
}

func currentCleanupReleaseResources(ctx context.Context, raw []byte, release string, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) ([]CleanupResource, error) {
	resources := []CleanupResource{}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	scopes := commandCredentialScopes(&b)
	for {
		var manifest map[string]any
		err := decoder.Decode(&manifest)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(manifest) == 0 {
			continue
		}
		kind, meta := textValue(manifest, "kind"), object(manifest, "metadata")
		name := textValue(meta, "name")
		namespace := textValue(meta, "namespace")
		if name == "" || strings.ContainsAny(name, "/ \\\n\r") || !offlineResourceKinds[kind] || textValue(object(meta, "labels"), "ops.platform.io/release") != release || object(meta, "annotations")["helm.sh/hook"] != nil {
			return nil, errors.New("cleanup manifest outside supported release scope")
		}
		if kind == "Secret" || kind == "ConfigMap" {
			protected := map[string]bool{"ops-platform-bootstrap": true, "ops-platform-runtime": true, "ops-installation-checkpoint": true, "ops-api-bootstrap-checkpoint": true, "ops-command-initialization-checkpoint": true, "ops-graph-bootstrap-checkpoint": true, "ops-graph-lease-bootstrap-checkpoint": true, "ops-keycloak-auth": true, "ops-openbao-auth": true, "ops-postgresql-auth": true, "ops-seaweedfs-auth": true, "ops-seaweedfs-iam": true, "ops-victoria-metrics-security": true, "ops-victoria-logs-security": true}
			for _, pair := range []struct{ section, key string }{{"sp04", "apiIdentitySecret"}, {"sp04", "workerIdentitySecret"}, {"sp04", "sourceCredentialsSecret"}, {"sp06", "identitySecret"}, {"sp06", "modelKeySecret"}} {
				protected[textValue(object(b.values, pair.section), pair.key)] = true
			}
			if protected[name] {
				return nil, errors.New("cleanup release includes protected bootstrap/identity state")
			}
		}
		if kind == "ClusterRole" || kind == "ClusterRoleBinding" {
			if namespace != "" {
				return nil, errors.New("cleanup cluster resource has namespace")
			}
		} else {
			if namespace == "" {
				namespace = b.Namespace()
			}
			if namespace != b.Namespace() && (release != "ops-platform" || scopes[kind+"/"+name] != namespace) {
				return nil, errors.New("cleanup resource targets foreign namespace")
			}
		}
		args := []string{"--context", p.Kubernetes.Context, "get", kind, name, "-o", "json"}
		if namespace != "" {
			args = append(args, "-n", namespace)
		}
		current, err := run(ctx, "kubectl", args...)
		var live map[string]any
		if err != nil || json.Unmarshal(current, &live) != nil || !exactHelmOwner(live, b.Namespace(), release) {
			return nil, errors.New("cleanup live Helm ownership differs")
		}
		r, err := cleanupResource(live, kind, name, namespace)
		if err != nil {
			return nil, err
		}
		resources = append(resources, r)
	}
	if len(resources) == 0 {
		return nil, errors.New("cleanup release manifest empty")
	}
	return resources, nil
}
