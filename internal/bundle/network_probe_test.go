package bundle

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBootstrapEgressRequiresBothControlsAndCleansOnlyCreatedPod(t *testing.T) {
	for _, output := range []string{"INTERNAL_DNS_REACHABLE_PUBLIC_EGRESS_DENIED", "NETWORK_POLICY_NOT_ENFORCED", ""} {
		t.Run(output, func(t *testing.T) {
			deleted := false
			run := func(_ context.Context, program string, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if program != "kubectl" {
					return nil, errors.New("unexpected program")
				}
				switch {
				case strings.Contains(joined, "get service kube-dns"):
					return []byte(`{"spec":{"clusterIP":"10.96.0.10"}}`), nil
				case strings.Contains(joined, " run "):
					if !strings.Contains(joined, "--image-pull-policy=Never") || !strings.Contains(joined, "--override-type=strategic") || !strings.Contains(joined, "ops.platform.io/release=ops-platform") {
						t.Fatal("probe permits online pull or escapes policy")
					}
					return []byte(`{"metadata":{"uid":"owned"}}`), nil
				case strings.Contains(joined, "get pod"):
					return []byte(`{"metadata":{"uid":"owned"}}`), nil
				case strings.Contains(joined, "logs pod/"):
					return []byte(output), nil
				case strings.Contains(joined, "delete pod"):
					deleted = true
				}
				return nil, nil
			}
			err := verifyBootstrapEgress(context.Background(), "ops-system", importProfile(), []ImageArtifact{{Name: "postgresql", Reference: "local/pg@sha256:locked"}}, run)
			if (err == nil) != (output == "INTERNAL_DNS_REACHABLE_PUBLIC_EGRESS_DENIED") || !deleted {
				t.Fatalf("result=%v deleted=%v", err, deleted)
			}
		})
	}
}

func TestBootstrapEgressRefusesChangedProbeOwnership(t *testing.T) {
	deleted := false
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "get service"):
			return []byte(`{"spec":{"clusterIP":"10.96.0.10"}}`), nil
		case strings.Contains(joined, " run "):
			return []byte(`{"metadata":{"uid":"owned"}}`), nil
		case strings.Contains(joined, "get pod"):
			return []byte(`{"metadata":{"uid":"foreign"}}`), nil
		case strings.Contains(joined, "logs pod/"):
			return []byte("INTERNAL_DNS_REACHABLE_PUBLIC_EGRESS_DENIED"), nil
		case strings.Contains(joined, "delete pod"):
			deleted = true
		}
		return nil, nil
	}
	err := verifyBootstrapEgress(context.Background(), "ops-system", importProfile(), []ImageArtifact{{Name: "postgresql", Reference: "local/pg@sha256:locked"}}, run)
	if err == nil || deleted {
		t.Fatalf("changed ownership error=%v deleted=%v", err, deleted)
	}
}
