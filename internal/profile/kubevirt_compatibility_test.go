package profile

import (
	"context"
	"strings"
	"testing"
)

func TestKubeVirtCompatibilityGateFailsClosedBeforeInstallation(t *testing.T) {
	matrix, err := LoadKubeVirtCompatibilityMatrix("../../docs/compatibility/kubevirt-kubernetes-matrix.yaml")
	if err != nil {
		t.Fatalf("load frozen KubeVirt compatibility matrix: %v", err)
	}

	cases := []struct {
		name        string
		server      string
		decision    string
		wantResolve string
	}{
		{name: "OrbStack supported minor", server: "v1.35.6+orb1", decision: "supported"},
		{name: "known unsupported minor", server: "v1.27.9", decision: "unsupported", wantResolve: "KUBEVIRT_KUBERNETES_UNSUPPORTED"},
		{name: "unknown server version", server: "not-a-version", decision: "unverified", wantResolve: "UPSTREAM_COMPATIBILITY_UNVERIFIED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := DecideKubeVirtCompatibility(tc.server, matrix)
			if decision.Decision != tc.decision {
				t.Fatalf("decision for Kubernetes %q = %q, want %q", tc.server, decision.Decision, tc.decision)
			}
			if tc.wantResolve == "" {
				if decision.KubeVirtVersion != "1.9.0" {
					t.Fatalf("supported Kubernetes %q resolved KubeVirt version %q, want exact 1.9.0", tc.server, decision.KubeVirtVersion)
				}
				return
			}

			input := InputProfile{
				SchemaVersion: 1,
				Kind:          "detected",
				ProfileID:     "test-virtualization",
				Environment:   "development",
				Architecture:  "arm64",
				Selected:      "virtualization",
				Components:    map[string]ComponentInput{"kubevirt": {Mode: "bundled", EnabledIn: []string{"virtualization"}}},
			}
			discovery := Discovery{Kubernetes: KubernetesDiscovery{
				Distribution:  "orbstack",
				ServerVersion: tc.server,
				Architecture:  "arm64",
				Context:       "orbstack",
				ClusterUID:    "test-cluster",
				KubeVirt:      decision.Decision,
			}}
			_, resolveErr := Resolve(context.Background(), input, discovery)
			if resolveErr == nil || !strings.Contains(resolveErr.Error(), tc.wantResolve) {
				t.Fatalf("Resolve error = %v, want %s before any installation", resolveErr, tc.wantResolve)
			}
		})
	}
}
