package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ops-platform/internal/bundle"
	"ops-platform/internal/profile"
)

type runtimeNode struct {
	Metadata struct {
		Name, UID         string
		DeletionTimestamp *string
	} `json:"metadata"`
	Status struct {
		NodeInfo   struct{ Architecture, OperatingSystem, ContainerRuntimeVersion string } `json:"nodeInfo"`
		Conditions []struct{ Type, Status string }                                         `json:"conditions"`
		Images     []struct{ Names []string }                                              `json:"images"`
	} `json:"status"`
}

// PreloadedContainerd admits an existing standard Kubernetes cluster. It never
// accesses a node socket or pulls an image. Operators first run signed import
// locally on EVERY node; installation requires every exact ref in kubelet's
// image inventory. A missing/truncated/stale inventory fails closed.
type PreloadedContainerd struct {
	Run     Runner
	profile profile.ResolvedProfile
	nodes   map[string]string
}

func (d *PreloadedContainerd) execute(ctx context.Context, program string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if d.Run != nil {
		return d.Run(ctx, program, args...)
	}
	return Run(ctx, program, args...)
}

func (d *PreloadedContainerd) observe(ctx context.Context, p profile.ResolvedProfile) ([]runtimeNode, error) {
	if p.Kubernetes.Context == "" || p.Kubernetes.ClusterUID == "" || p.Runtime.ImageImporter != "containerd_ctr" {
		return nil, errors.New("containerd requires explicit resolved context, cluster UID and importer")
	}
	raw, err := d.execute(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(raw)) != p.Kubernetes.ClusterUID {
		return nil, errors.New("PROFILE_COMPONENT_CONFLICT: current cluster UID differs")
	}
	raw, err = d.execute(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "--raw", "/version")
	if err != nil {
		return nil, err
	}
	var version struct {
		GitVersion string `json:"gitVersion"`
	}
	if json.Unmarshal(raw, &version) != nil || version.GitVersion != p.Kubernetes.ServerVersion {
		return nil, errors.New("PROFILE_COMPONENT_CONFLICT: Kubernetes version differs")
	}
	raw, err = d.execute(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "nodes", "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []runtimeNode `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil || len(list.Items) == 0 {
		return nil, errors.New("containerd node inventory unavailable")
	}
	seen := map[string]bool{}
	for _, node := range list.Items {
		ready := false
		for _, c := range node.Status.Conditions {
			ready = ready || c.Type == "Ready" && c.Status == "True"
		}
		if node.Metadata.Name == "" || node.Metadata.UID == "" || seen[node.Metadata.Name] || node.Metadata.DeletionTimestamp != nil || !ready || node.Status.NodeInfo.OperatingSystem != "linux" || node.Status.NodeInfo.Architecture != p.Architecture || !strings.HasPrefix(node.Status.NodeInfo.ContainerRuntimeVersion, "containerd://") {
			return nil, fmt.Errorf("CAPABILITY_DISABLED: node %s is not Ready homogeneous Linux/containerd or is being replaced", node.Metadata.Name)
		}
		seen[node.Metadata.Name] = true
	}
	return list.Items, nil
}

func (d *PreloadedContainerd) Probe(ctx context.Context, p profile.ResolvedProfile) (string, error) {
	nodes, err := d.observe(ctx, p)
	if err != nil {
		return "", err
	}
	if p.Kubernetes.StorageClass == "" {
		return "", errors.New("STORAGE_UNAVAILABLE: resolved StorageClass required for bundled core")
	}
	raw, err := d.execute(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "storageclass", p.Kubernetes.StorageClass, "-o", "json")
	if err != nil {
		return "", err
	}
	var storage struct {
		Metadata    struct{ Name, UID string }
		Provisioner string
	}
	if json.Unmarshal(raw, &storage) != nil || storage.Metadata.Name != p.Kubernetes.StorageClass || storage.Metadata.UID == "" || storage.Provisioner == "" {
		return "", errors.New("STORAGE_UNAVAILABLE: selected StorageClass identity/provisioner unavailable")
	}
	d.profile = p
	d.nodes = map[string]string{}
	for _, n := range nodes {
		d.nodes[n.Metadata.Name] = n.Metadata.UID
	}
	return "containerd_ctr", nil
}

func (d *PreloadedContainerd) ImageOperation() string { return "verify-preloaded-all-nodes-no-pull" }
func (d *PreloadedContainerd) NodeIdentities() map[string]string {
	copy := map[string]string{}
	for name, uid := range d.nodes {
		copy[name] = uid
	}
	return copy
}
func (d *PreloadedContainerd) Import(ctx context.Context, image bundle.ImageArtifact) error {
	return d.Verify(ctx, image)
}

func (d *PreloadedContainerd) Verify(ctx context.Context, image bundle.ImageArtifact) error {
	if len(d.nodes) == 0 {
		return errors.New("containerd cluster must be probed before admission")
	}
	nodes, err := d.observe(ctx, d.profile)
	if err != nil {
		return err
	}
	if len(nodes) != len(d.nodes) {
		return errors.New("PROFILE_COMPONENT_CONFLICT: node set changed during installation")
	}
	for _, n := range nodes {
		if d.nodes[n.Metadata.Name] != n.Metadata.UID {
			return errors.New("PROFILE_COMPONENT_CONFLICT: node UID changed during installation")
		}
		found := false
		for _, item := range n.Status.Images {
			for _, ref := range item.Names {
				found = found || canonicalDockerReference(ref) == canonicalDockerReference(image.Reference)
			}
		}
		if !found {
			return fmt.Errorf("OFFLINE_IMAGE_MISSING: node=%s uid=%s image=%s; run signed node-local import and wait for kubelet inventory", n.Metadata.Name, n.Metadata.UID, image.Reference)
		}
	}
	return nil
}

// LocalContainerd imports only into an explicitly named local node's k8s.io
// store. A running Kubernetes container must be witnessed in that socket before
// any mutation. No SSH, node proxy, kubectl exec, registry or runtime pull exists.
type LocalContainerd struct {
	Run                                     Runner
	Address, NodeName, NodeUID, Snapshotter string
	probed                                  bool
	localFlag                               bool
}

var nodeNamePattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9.]*[a-z0-9])?$`)
var snapshotterPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func (d *LocalContainerd) execute(ctx context.Context, program string, args ...string) ([]byte, error) {
	timeout := 20 * time.Second
	if program == "ctr" {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if d.Run != nil {
		return d.Run(ctx, program, args...)
	}
	return Run(ctx, program, args...)
}
func (d *LocalContainerd) ctr(ctx context.Context, args ...string) ([]byte, error) {
	return d.execute(ctx, "ctr", append([]string{"--address", d.Address, "--namespace", "k8s.io"}, args...)...)
}
func (d *LocalContainerd) Probe(ctx context.Context, p profile.ResolvedProfile) (string, error) {
	d.probed = false
	if !filepath.IsAbs(d.Address) || filepath.Clean(d.Address) != d.Address || strings.ContainsAny(d.Address, "\x00\r\n") || !nodeNamePattern.MatchString(d.NodeName) || d.NodeUID == "" || !snapshotterPattern.MatchString(d.Snapshotter) {
		return "", errors.New("node-local containerd requires absolute Unix socket, node name/UID and explicit snapshotter")
	}
	cluster := &PreloadedContainerd{Run: d.Run}
	nodes, err := cluster.observe(ctx, p)
	if err != nil {
		return "", err
	}
	matched := false
	for _, n := range nodes {
		matched = matched || n.Metadata.Name == d.NodeName && n.Metadata.UID == d.NodeUID
	}
	if !matched {
		return "", errors.New("PROFILE_COMPONENT_CONFLICT: selected node name/UID not present")
	}
	raw, err := d.execute(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "pods", "--all-namespaces", "--field-selector", "spec.nodeName="+d.NodeName, "-o", "json")
	if err != nil {
		return "", err
	}
	var pods struct {
		Items []struct {
			Spec struct {
				NodeName string `json:"nodeName"`
			}
			Status struct {
				ContainerStatuses []struct {
					ContainerID string `json:"containerID"`
					State       struct {
						Running *json.RawMessage `json:"running"`
					} `json:"state"`
				} `json:"containerStatuses"`
			}
		} `json:"items"`
	}
	if json.Unmarshal(raw, &pods) != nil {
		return "", errors.New("node container witness unavailable")
	}
	witness := false
	for _, pod := range pods.Items {
		if pod.Spec.NodeName != d.NodeName {
			continue
		}
		for _, c := range pod.Status.ContainerStatuses {
			id := strings.TrimPrefix(c.ContainerID, "containerd://")
			if c.State.Running == nil || !strings.HasPrefix(c.ContainerID, "containerd://") || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(id) {
				continue
			}
			raw, err := d.ctr(ctx, "containers", "info", id)
			var local struct {
				ID string `json:"ID"`
			}
			if err == nil && json.Unmarshal(raw, &local) == nil && local.ID == id {
				witness = true
				break
			}
		}
		if witness {
			break
		}
	}
	if !witness {
		return "", errors.New("CAPABILITY_DISABLED: selected socket does not witness a running container on selected node")
	}
	raw, err = d.ctr(ctx, "images", "import", "--help")
	if err != nil {
		return "", err
	}
	d.localFlag = strings.Contains(string(raw), "--local")
	d.probed = true
	return "containerd_ctr", nil
}
func (d *LocalContainerd) ImageOperation() string { return "node-local-verified-oci-import-no-pull" }
func (d *LocalContainerd) NodeIdentities() map[string]string {
	return map[string]string{d.NodeName: d.NodeUID}
}
func (d *LocalContainerd) Import(ctx context.Context, image bundle.ImageArtifact) error {
	if !d.probed {
		return errors.New("node socket must be probed before import")
	}
	args := []string{"images", "import", "--digests", "--platform", image.Architecture, "--snapshotter", d.Snapshotter}
	if d.localFlag {
		args = append(args, "--local")
	}
	_, err := d.ctr(ctx, append(args, image.Path)...)
	return err
}
func (d *LocalContainerd) Verify(ctx context.Context, image bundle.ImageArtifact) error {
	if !d.probed {
		return errors.New("node socket must be probed before verification")
	}
	raw, err := d.ctr(ctx, "images", "list", "name=="+image.Reference)
	if err != nil {
		return err
	}
	matched := false
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == image.Reference && f[2] == image.Digest {
			matched = true
		}
	}
	if !matched {
		return errors.New("imported containerd reference/target digest differs")
	}
	raw, err = d.ctr(ctx, "images", "check", "--quiet", "--snapshotter", d.Snapshotter, "name=="+image.Reference)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(raw)) != image.Reference {
		return errors.New("imported containerd image is incomplete or not unpacked; refusing pull")
	}
	return nil
}
