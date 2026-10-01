package profile

import (
	"context"
	"testing"
)

func TestCoreCannotEnableDeferredVirtualization(t *testing.T) {
	input, discovery := testProfile("external"), testDiscovery()
	input.Components["kubevirt"] = ComponentInput{Mode: "bundled", EnabledIn: []string{"core"}, Compatibility: "supported"}
	input.Components["cdi"] = input.Components["kubevirt"]
	discovery.Kubernetes.KubeVirt = "supported"
	resolved, err := Resolve(context.Background(), input, discovery)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kubevirt", "cdi"} {
		if resolved.Components[name].Mode != "disabled" || resolved.Components[name].Compatibility != "unverified" {
			t.Fatalf("core activated %s", name)
		}
		mutated := resolved.Components[name]
		mutated.Mode = "external"
		resolved.Components[name] = mutated
		if resolved.Validate() == nil {
			t.Fatalf("edited core activated %s", name)
		}
		mutated.Mode = "disabled"
		resolved.Components[name] = mutated
	}
	if resolved.Kubernetes.KubeVirt != "unverified" {
		t.Fatal("matrix compatibility promoted to runtime acceptance")
	}
}
