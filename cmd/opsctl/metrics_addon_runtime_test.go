package main

import (
	"ops-platform/internal/bundle/drivers"
	"testing"
)

func TestMetricsAddonUsesProfileRuntime(t *testing.T) {
	runtime, err := metricsAddonRuntime("containerd_ctr")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.(*drivers.PreloadedContainerd); !ok {
		t.Fatal("native addon must verify preloaded nodes, without importing or pulling")
	}
	runtime, err = metricsAddonRuntime("orbstack_shared_store")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.(drivers.OrbStackSharedStore); !ok {
		t.Fatal("legacy OrbStack driver changed")
	}
	for _, unsupported := range []string{"", "docker", "unknown"} {
		if _, err := metricsAddonRuntime(unsupported); err == nil {
			t.Fatalf("unsupported importer accepted: %q", unsupported)
		}
	}
}
