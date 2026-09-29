package drivers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ops-platform/internal/bundle"
	"ops-platform/internal/profile"
)

func TestProbeRequiresSharedRunningContainerWitness(t *testing.T) {
	p := profile.ResolvedProfile{Architecture: "arm64", Kubernetes: profile.KubernetesDiscovery{Distribution: "orbstack", Context: "orbstack", ClusterUID: "cluster-id", ServerVersion: "v1.35.6+orb1"}}
	for _, shared := range []bool{false, true} {
		driver := OrbStackSharedStore{Run: func(_ context.Context, program string, args ...string) ([]byte, error) {
			command := program + " " + strings.Join(args, " ")
			switch {
			case strings.Contains(command, "context inspect"):
				return []byte(`[{"Name":"orbstack","Endpoints":{"docker":{"Host":"unix:///orbstack.sock"}}}]`), nil
			case strings.Contains(command, "info"):
				return []byte("linux/aarch64"), nil
			case strings.Contains(command, "namespace kube-system"):
				return []byte("cluster-id"), nil
			case strings.Contains(command, "/version"):
				return []byte(`{"gitVersion":"v1.35.6+orb1","platform":"linux/arm64"}`), nil
			case strings.Contains(command, "get pods"):
				return []byte(`{"items":[{"status":{"containerStatuses":[{"containerID":"docker://container-123","state":{"running":{}}}]}}]}`), nil
			case strings.Contains(command, "container inspect"):
				if shared {
					return []byte("true"), nil
				}
				return nil, errors.New("container not found")
			}
			return nil, errors.New("unexpected command")
		}}
		result, err := driver.Probe(context.Background(), p)
		if shared && (err != nil || result != "orbstack_shared_store") {
			t.Fatalf("shared runtime: %s %v", result, err)
		}
		if !shared && err == nil {
			t.Fatal("matching architecture was accepted without a shared container witness")
		}
	}
}

func TestVerifyRefusesLostDigestWithoutPull(t *testing.T) {
	image := bundle.ImageArtifact{Reference: "ops.local/api@sha256:" + strings.Repeat("a", 64), Architecture: "linux/arm64"}
	calls := []string{}
	driver := OrbStackSharedStore{Run: func(_ context.Context, program string, args ...string) ([]byte, error) {
		calls = append(calls, program+" "+strings.Join(args, " "))
		return []byte(`[{"Os":"linux","Architecture":"arm64","RepoDigests":[]}]`), nil
	}}
	if err := driver.Verify(context.Background(), image); err == nil {
		t.Fatal("missing digest was accepted")
	}
	if len(calls) != 1 || strings.Contains(calls[0], "pull") {
		t.Fatalf("unexpected commands %v", calls)
	}
}

func TestVerifyNormalizesDockerHubNameWithoutChangingDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	image := bundle.ImageArtifact{Reference: "docker.io/library/postgres@" + digest, Architecture: "linux/arm64"}
	for _, test := range []struct {
		name, reported string
		accepted       bool
	}{
		{"short official name", "postgres@" + digest, true},
		{"explicit official name", "docker.io/library/postgres@" + digest, true},
		{"wrong repository", "quay.io/library/postgres@" + digest, false},
		{"wrong digest", "postgres@sha256:" + strings.Repeat("b", 64), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := OrbStackSharedStore{Run: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				return []byte(`[{"Os":"linux","Architecture":"arm64","RepoDigests":["` + test.reported + `"]}]`), nil
			}}
			if err := driver.Verify(context.Background(), image); (err == nil) != test.accepted {
				t.Fatalf("accepted=%v, error=%v", test.accepted, err)
			}
		})
	}
}
