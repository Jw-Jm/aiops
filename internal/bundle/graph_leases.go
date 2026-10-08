package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"ops-platform/internal/profile"
)

type GraphLeaseBinding struct {
	Name       string `json:"name"`
	Tenant     string `json:"tenant"`
	ClusterUID string `json:"clusterUid"`
	UID        string `json:"uid"`
}
type GraphLeaseBootstrapReceipt struct {
	NamespaceUID   string              `json:"namespaceUid"`
	InstallationID string              `json:"installationId"`
	ProfileDigest  string              `json:"profileDigest"`
	BusinessDigest string              `json:"businessDigest"`
	Bindings       []GraphLeaseBinding `json:"bindings"`
}

func plannedGraphLeases(b BusinessValues) ([]GraphLeaseBinding, error) {
	if b.values == nil || object(object(b.values, "sp04"), "kubernetesAPI")["localCollector"] != true {
		return nil, errors.New("formal local collector configuration required for Graph Lease initialization")
	}
	var result []GraphLeaseBinding
	seen := map[string]bool{}
	for _, item := range object(b.values, "sp04")["clusters"].([]any) {
		c := item.(map[string]any)
		name := textValue(c, "LeaseName")
		if name == "" || seen[name] {
			return nil, errors.New("each cluster requires its own explicit Graph Lease identity")
		}
		seen[name] = true
		result = append(result, GraphLeaseBinding{Name: name, Tenant: textValue(c, "Tenant"), ClusterUID: textValue(c, "ClusterUID")})
	}
	if len(result) != len(object(b.values, "sp04")["leaseNames"].([]any)) {
		return nil, errors.New("Graph Lease names must exactly match configured clusters")
	}
	return result, nil
}

// BootstrapGraphLeases creates only explicitly named local installation
// Leases. Workers retain get/update access, never create or namespace-wide
// writes. Partial initialization preserves resources and fails closed.
func BootstrapGraphLeases(ctx context.Context, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) (GraphLeaseBootstrapReceipt, error) {
	var receipt GraphLeaseBootstrapReceipt
	if run == nil {
		return receipt, errors.New("Kubernetes command runner required")
	}
	bindings, err := plannedGraphLeases(b)
	if err != nil {
		return receipt, err
	}
	identity, err := currentNamespaceIdentity(ctx, p, b, run)
	if err != nil {
		return receipt, err
	}
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "namespace", "kube-system", "-o", "json")
	var cluster map[string]any
	if err != nil || json.Unmarshal(raw, &cluster) != nil {
		return receipt, errors.New("native cluster identity unavailable")
	}
	for _, binding := range bindings {
		if binding.ClusterUID != textValue(object(cluster, "metadata"), "uid") {
			return receipt, errors.New("local collector cluster UID differs from native target")
		}
	}
	for _, resource := range append([]GraphLeaseBinding{{Name: "ops-graph-lease-bootstrap-checkpoint"}}, bindings...) {
		kind := "lease"
		if resource.Name == "ops-graph-lease-bootstrap-checkpoint" {
			kind = "configmap"
		}
		raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", kind, resource.Name, "--ignore-not-found", "-o", "json")
		if err != nil || len(raw) > 0 {
			return receipt, errors.New("Graph Lease bootstrap refuses existing or unreadable identities")
		}
	}
	dir, err := os.MkdirTemp("", "ops-graph-lease-bootstrap-")
	if err != nil {
		return receipt, errors.New("Graph Lease temporary manifest unavailable")
	}
	defer os.RemoveAll(dir)
	receipt = GraphLeaseBootstrapReceipt{NamespaceUID: identity.UID, InstallationID: identity.InstallationID, ProfileDigest: jsonDigest(p), BusinessDigest: registeredBusinessInstallationDigest(b)}
	for _, binding := range bindings {
		manifest := map[string]any{"apiVersion": "coordination.k8s.io/v1", "kind": "Lease", "metadata": map[string]any{"name": binding.Name, "namespace": b.Namespace(), "labels": map[string]string{"ops.platform.io/managed-by": "opsctl-bootstrap", "ops.platform.io/installation-id": identity.InstallationID}, "annotations": map[string]string{"ops.platform/owner-epoch": "0", "ops.platform.io/tenant": binding.Tenant, "ops.platform.io/cluster-uid": binding.ClusterUID}}, "spec": map[string]any{"leaseDurationSeconds": 15}}
		raw, _ := json.Marshal(manifest)
		file := filepath.Join(dir, "lease.json")
		if os.WriteFile(file, raw, 0600) != nil {
			return receipt, errors.New("Graph Lease manifest write failed")
		}
		created, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "create", "-f", file, "-o", "json")
		var live map[string]any
		if err != nil || json.Unmarshal(created, &live) != nil || textValue(object(live, "metadata"), "uid") == "" {
			return receipt, errors.New("Graph Lease creation incomplete; retain partial resources")
		}
		binding.UID = textValue(object(live, "metadata"), "uid")
		receipt.Bindings = append(receipt.Bindings, binding)
	}
	raw, _ = json.Marshal(receipt)
	if _, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "create", "configmap", "ops-graph-lease-bootstrap-checkpoint", "--from-literal=receipt.json="+string(raw)); err != nil {
		return receipt, errors.New("Graph Lease checkpoint creation incomplete; retain Leases")
	}
	_, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "label", "configmap", "ops-graph-lease-bootstrap-checkpoint", "ops.platform.io/installation-id="+identity.InstallationID)
	return receipt, err
}

func preflightCurrentGraphLeases(ctx context.Context, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) error {
	expected, err := plannedGraphLeases(b)
	if err != nil {
		return err
	}
	identity, err := currentNamespaceIdentity(ctx, p, b, run)
	if err != nil {
		return err
	}
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "configmap", "ops-graph-lease-bootstrap-checkpoint", "-o", "json")
	var cm struct {
		Data     map[string]string `json:"data"`
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	var receipt GraphLeaseBootstrapReceipt
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &cm) != nil || json.Unmarshal([]byte(cm.Data["receipt.json"]), &receipt) != nil || cm.Metadata.Labels["ops.platform.io/installation-id"] != identity.InstallationID || receipt.NamespaceUID != identity.UID || receipt.InstallationID != identity.InstallationID || receipt.ProfileDigest != jsonDigest(p) || receipt.BusinessDigest != registeredBusinessInstallationDigest(b) || len(receipt.Bindings) != len(expected) {
		return errors.New("formal Graph Lease checkpoint identity/scope differs or is unavailable")
	}
	for i, binding := range receipt.Bindings {
		if binding.Name != expected[i].Name || binding.Tenant != expected[i].Tenant || binding.ClusterUID != expected[i].ClusterUID || binding.UID == "" {
			return errors.New("Graph Lease checkpoint binding differs")
		}
		raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "lease", binding.Name, "-o", "json")
		var live map[string]any
		if err != nil || json.Unmarshal(raw, &live) != nil {
			return errors.New("Graph Lease identity unavailable")
		}
		meta := object(live, "metadata")
		labels := object(meta, "labels")
		annotations := object(meta, "annotations")
		epoch, epochErr := strconv.ParseInt(textValue(annotations, "ops.platform/owner-epoch"), 10, 64)
		if textValue(meta, "uid") != binding.UID || textValue(labels, "ops.platform.io/managed-by") != "opsctl-bootstrap" || textValue(labels, "ops.platform.io/installation-id") != identity.InstallationID || textValue(annotations, "ops.platform.io/tenant") != binding.Tenant || textValue(annotations, "ops.platform.io/cluster-uid") != binding.ClusterUID || epochErr != nil || epoch < 0 {
			return errors.New("Graph Lease UID, installation or scope changed; audited recovery required")
		}
	}
	return nil
}
