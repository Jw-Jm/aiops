//go:build pre_sp07_metrics_live

package bundle

import (
	"bytes"
	"context"
	"ops-platform/internal/profile"
	"os"
	"os/exec"
	"testing"
)

func TestMetricsCurrentDiscoveredNetworkUsesNativeControlPlane(t *testing.T) {
	path := os.Getenv("PRE_SP07_METRICS_DISCOVERY_CONFIG")
	if path == "" {
		t.Fatal("explicit current metrics-addon/v1 config required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("current Metrics config unavailable")
	}
	c, err := ReadMetricsAddonValues(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	p := profile.ResolvedProfile{Kubernetes: profile.KubernetesDiscovery{Context: "orbstack"}}
	run := func(ctx context.Context, program string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, program, args...).CombinedOutput()
	}
	if err := metricsDiscoveredNetwork(t.Context(), p, c, run); err != nil {
		t.Fatal(err)
	}
	changed := c
	changed.APIServerPort = 1
	if err := metricsDiscoveredNetwork(t.Context(), p, changed, run); err == nil {
		t.Fatal("stale API endpoint port accepted")
	}
	changed = c
	changed.NodeCIDRs = []string{"192.168.253.254/32"}
	if err := metricsDiscoveredNetwork(t.Context(), p, changed, run); err == nil {
		t.Fatal("undiscovered Node scope accepted")
	}
}
