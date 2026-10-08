package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ops-platform/internal/bundle"
	"ops-platform/internal/profile"
)

func containerdProfile() profile.ResolvedProfile {
	return profile.ResolvedProfile{Architecture: "arm64", Runtime: profile.RuntimeInput{ImageImporter: "containerd_ctr"}, Kubernetes: profile.KubernetesDiscovery{Context: "standard-k8s", ClusterUID: "cluster-uid", ServerVersion: "v1.35.5", StorageClass: "standard"}}
}
func nodeInventory(ref string) []runtimeNode {
	var nodes []runtimeNode
	for _, name := range []string{"control-plane", "worker"} {
		var n runtimeNode
		n.Metadata.Name, n.Metadata.UID = name, name+"-uid"
		n.Status.NodeInfo.Architecture, n.Status.NodeInfo.OperatingSystem, n.Status.NodeInfo.ContainerRuntimeVersion = "arm64", "linux", "containerd://2.1.4"
		n.Status.Conditions = append(n.Status.Conditions, struct{ Type, Status string }{"Ready", "True"})
		n.Status.Images = append(n.Status.Images, struct{ Names []string }{[]string{ref}})
		nodes = append(nodes, n)
	}
	return nodes
}
func clusterRunner(t *testing.T, nodes *[]runtimeNode, commands *[]string) Runner {
	t.Helper()
	return func(_ context.Context, program string, args ...string) ([]byte, error) {
		command := program + " " + strings.Join(args, " ")
		*commands = append(*commands, command)
		switch {
		case strings.Contains(command, "namespace kube-system"):
			return []byte("cluster-uid"), nil
		case strings.Contains(command, "/version"):
			return []byte(`{"gitVersion":"v1.35.5"}`), nil
		case strings.Contains(command, "get nodes"):
			return json.Marshal(map[string]any{"items": *nodes})
		case strings.Contains(command, "get storageclass standard"):
			return []byte(`{"metadata":{"name":"standard","uid":"storage-uid"},"provisioner":"test.csi"}`), nil
		}
		return nil, errors.New("unexpected command: " + command)
	}
}
func TestPreloadedContainerdRequiresEveryNodeExactDigestAndStableIdentity(t *testing.T) {
	ctx := context.Background()
	image := bundle.ImageArtifact{Reference: "ops.local/api@sha256:" + strings.Repeat("a", 64)}
	for _, scenario := range []string{"valid", "missing-worker", "wrong-digest", "new-node", "rebuilt-node", "not-ready", "wrong-arch", "foreign-runtime", "deleting"} {
		t.Run(scenario, func(t *testing.T) {
			nodes := nodeInventory(image.Reference)
			calls := []string{}
			d := &PreloadedContainerd{Run: clusterRunner(t, &nodes, &calls)}
			if _, err := d.Probe(ctx, containerdProfile()); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing-worker":
				nodes[1].Status.Images = nil
			case "wrong-digest":
				nodes[1].Status.Images[0].Names = []string{"ops.local/api@sha256:" + strings.Repeat("b", 64)}
			case "new-node":
				n := nodes[1]
				n.Metadata.Name = "new"
				n.Metadata.UID = "new-uid"
				nodes = append(nodes, n)
			case "rebuilt-node":
				nodes[1].Metadata.UID = "replacement-uid"
			case "not-ready":
				nodes[1].Status.Conditions[0].Status = "False"
			case "wrong-arch":
				nodes[1].Status.NodeInfo.Architecture = "amd64"
			case "foreign-runtime":
				nodes[1].Status.NodeInfo.ContainerRuntimeVersion = "cri-o://1.35.0"
			case "deleting":
				stamp := "2026-10-08T00:00:00Z"
				nodes[1].Metadata.DeletionTimestamp = &stamp
			}
			if err := d.Import(ctx, image); (err == nil) != (scenario == "valid") {
				t.Fatalf("scenario %s: %v", scenario, err)
			}
			for _, c := range calls {
				if !strings.HasPrefix(c, "kubectl ") || strings.Contains(c, " exec ") || strings.Contains(c, "pull") {
					t.Fatalf("not read-only: %s", c)
				}
			}
		})
	}
}
func TestNodeLocalContainerdWitnessAndClosure(t *testing.T) {
	ctx := context.Background()
	digest := "sha256:" + strings.Repeat("a", 64)
	ref := "ops.local/api@" + digest
	id := strings.Repeat("c", 64)
	for _, scenario := range []string{"valid", "docker-hub-normalized", "foreign-socket", "wrong-node", "wrong-digest", "missing-layer"} {
		t.Run(scenario, func(t *testing.T) {
			ref := ref
			storedRef := ref
			if scenario == "docker-hub-normalized" {
				ref = "victoriametrics/victoria-logs@" + digest
				storedRef = "docker.io/" + ref
			}
			nodes := nodeInventory(ref)
			calls := []string{}
			base := clusterRunner(t, &nodes, &calls)
			d := &LocalContainerd{Address: "/run/containerd/containerd.sock", NodeName: "worker", NodeUID: "worker-uid", Snapshotter: "overlayfs"}
			if scenario == "wrong-node" {
				d.NodeUID = "replacement-uid"
			}
			d.Run = func(ctx context.Context, program string, args ...string) ([]byte, error) {
				c := program + " " + strings.Join(args, " ")
				if strings.Contains(c, "get pods") {
					calls = append(calls, c)
					return []byte(`{"items":[{"spec":{"nodeName":"worker"},"status":{"containerStatuses":[{"containerID":"containerd://` + id + `","state":{"running":{}}}]}}]}`), nil
				}
				if program != "ctr" {
					return base(ctx, program, args...)
				}
				calls = append(calls, c)
				switch {
				case strings.Contains(c, "containers info"):
					if scenario == "foreign-socket" {
						return nil, errors.New("not found")
					}
					return []byte(`{"ID":"` + id + `"}`), nil
				case strings.Contains(c, "--help"):
					return []byte("--local --platform"), nil
				case strings.Contains(c, "images import"):
					baseName := strings.Split(storedRef, "@")[0]
					if !strings.Contains(c, "--base-name "+baseName+" ") {
						return nil, errors.New("unnamed OCI digest creates noncanonical import-date alias unusable by CRI")
					}
					return nil, nil
				case strings.Contains(c, "images list"):
					if args[len(args)-1] != "name=="+storedRef {
						return []byte("REF TYPE DIGEST\n"), nil
					}
					target := digest
					if scenario == "wrong-digest" {
						target = "sha256:" + strings.Repeat("b", 64)
					}
					return []byte("REF TYPE DIGEST\n" + storedRef + " application/vnd.oci.image.manifest.v1+json " + target + "\n"), nil
				case strings.Contains(c, "images check"):
					if scenario == "missing-layer" || args[len(args)-1] != "name=="+storedRef {
						return nil, nil
					}
					return []byte(storedRef + "\n"), nil
				}
				return nil, errors.New("unexpected ctr command")
			}
			_, err := d.Probe(ctx, containerdProfile())
			if scenario == "foreign-socket" || scenario == "wrong-node" {
				if err == nil {
					t.Fatal("wrong socket/node accepted")
				}
				for _, c := range calls {
					if strings.Contains(c, "images import") && !strings.Contains(c, "--help") {
						t.Fatal("runtime mutated before witness")
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			image := bundle.ImageArtifact{Reference: ref, Digest: digest, Architecture: "linux/arm64", Path: "/verified/image.oci.tar"}
			if err = d.Import(ctx, image); err != nil {
				t.Fatal(err)
			}
			if err = d.Verify(ctx, image); (err == nil) != (scenario == "valid" || scenario == "docker-hub-normalized") {
				t.Fatalf("closure accepted incorrectly: %v", err)
			}
			for _, c := range calls {
				if strings.Contains(c, " pull ") || strings.Contains(c, " exec ") || strings.HasPrefix(c, "ssh ") {
					t.Fatalf("unexpected network/execution: %s", c)
				}
			}
		})
	}
}

func TestContainerdArchitectureMatrixRejectsMixedNodes(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			p := containerdProfile()
			p.Architecture = arch
			image := bundle.ImageArtifact{Reference: "ops.local/api@sha256:" + strings.Repeat("a", 64)}
			nodes := nodeInventory(image.Reference)
			for i := range nodes {
				nodes[i].Status.NodeInfo.Architecture = arch
			}
			calls := []string{}
			d := &PreloadedContainerd{Run: clusterRunner(t, &nodes, &calls)}
			if _, err := d.Probe(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if err := d.Verify(context.Background(), image); err != nil {
				t.Fatal(err)
			}
			other := "arm64"
			if arch == other {
				other = "amd64"
			}
			nodes[1].Status.NodeInfo.Architecture = other
			if err := d.Verify(context.Background(), image); err == nil {
				t.Fatal("mixed architecture accepted")
			}
		})
	}
}
