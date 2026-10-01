package profile

import (
	"context"
	"strings"
	"testing"
)

func TestDetectRecommendations(t *testing.T) {
	t.Run("unique compatible instances are external", func(t *testing.T) {
		input := testProfile("detect")
		discovery := testDiscovery()
		result, err := Detect(input, discovery)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"victoriaMetrics", "victoriaLogs"} {
			if got := result.Components[name].Mode; got != "external" {
				t.Fatalf("%s mode = %q, want external", name, got)
			}
		}
	})

	t.Run("missing instances are bundled", func(t *testing.T) {
		input := testProfile("detect")
		input.Components = map[string]ComponentInput{
			"postgresql": {Mode: "detect"},
			"keycloak":   {Mode: "detect"},
			"seaweedfs":  {Mode: "detect"},
			"openbao":    {Mode: "detect"},
		}
		result, err := Detect(input, Discovery{Kubernetes: testDiscovery().Kubernetes})
		if err != nil {
			t.Fatal(err)
		}
		for name, component := range result.Components {
			if component.Mode != "bundled" {
				t.Fatalf("%s mode = %q, want bundled", name, component.Mode)
			}
		}
	})
}

func TestDetectRejectsAmbiguityAndIncompatibility(t *testing.T) {
	t.Run("multiple candidates", func(t *testing.T) {
		input := testProfile("external")
		discovery := testDiscovery()
		discovery.Components["victoriaLogs"] = append(discovery.Components["victoriaLogs"], discovery.Components["victoriaLogs"][0])
		if _, err := Resolve(context.Background(), input, discovery); err == nil || !strings.Contains(err.Error(), "PROFILE_COMPONENT_CONFLICT") {
			t.Fatalf("Resolve error = %v, want PROFILE_COMPONENT_CONFLICT", err)
		}
	})

	t.Run("explicit bundled conflicts with existing service", func(t *testing.T) {
		input := testProfile("bundled")
		if _, err := Resolve(context.Background(), input, testDiscovery()); err == nil || !strings.Contains(err.Error(), "PROFILE_COMPONENT_CONFLICT") {
			t.Fatalf("Resolve error = %v, want PROFILE_COMPONENT_CONFLICT", err)
		}
	})

	t.Run("incompatible external service", func(t *testing.T) {
		input := testProfile("external")
		discovery := testDiscovery()
		discovery.Components["victoriaLogs"][0].Compatible = false
		if _, err := Resolve(context.Background(), input, discovery); err == nil || !strings.Contains(err.Error(), "PROFILE_COMPONENT_CONFLICT") {
			t.Fatalf("Resolve error = %v, want PROFILE_COMPONENT_CONFLICT", err)
		}
	})
}

func TestResolveRequiresExactLocksAndDetectedInput(t *testing.T) {
	t.Run("rejects unresolved mode and incomplete locks", func(t *testing.T) {
		input := testProfile("bundled")
		input.Components["postgresql"] = ComponentInput{Mode: "detect", Version: "pending", Endpoint: ""}
		if _, err := Resolve(context.Background(), input, Discovery{Kubernetes: testDiscovery().Kubernetes}); err == nil {
			t.Fatal("Resolve accepted unresolved component")
		}
	})

	t.Run("resolved profile contains concrete endpoint and image digest", func(t *testing.T) {
		input := testProfile("external")
		resolved, err := Resolve(context.Background(), input, testDiscovery())
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"victoriaMetrics", "victoriaLogs"} {
			component := resolved.Components[name]
			if component.Mode != "external" || component.Version == "" || !strings.HasPrefix(component.Digest, "sha256:") || component.Endpoint == "" {
				t.Fatalf("%s is not precisely resolved: %#v", name, component)
			}
		}
	})
}

func TestResolveRejectsDetectedLockDrift(t *testing.T) {
	for _, field := range []string{"endpoint", "image", "architecture", "server", "importer"} {
		t.Run(field, func(t *testing.T) {
			input, discovery := testProfile("external"), testDiscovery()
			component := input.Components["victoriaMetrics"]
			switch field {
			case "endpoint":
				component.Endpoint = "http://different.monitoring.svc:8428"
			case "image":
				component.Image = "different/repository@" + discovery.Components["victoriaMetrics"][0].Digest
			case "architecture":
				input.Architecture = "amd64"
			case "server":
				input.Discovery.ServerVersion = "v1.35.5+orb1"
			case "importer":
				input.Runtime.ImageImporter = "internal_registry"
			}
			input.Components["victoriaMetrics"] = component
			if _, err := Resolve(context.Background(), input, discovery); err == nil || !strings.Contains(err.Error(), "PROFILE_COMPONENT_CONFLICT") {
				t.Fatalf("%s drift was accepted: %v", field, err)
			}
		})
	}
}

func TestResolveKeepsUnqualifiedComponentsNonInstallable(t *testing.T) {
	input := testProfile("bundled")
	input.Components = map[string]ComponentInput{
		"postgresql": {Mode: "bundled", Endpoint: "postgresql://ops-postgresql.ops-system.svc.cluster.local:5432"},
	}
	discovery := testDiscovery()
	discovery.Components = map[string][]ComponentCandidate{}
	locks, err := loadComponentLocks("../../bundle/component-catalog.yaml", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	discovery.Locks = locks
	// Keep a real candidate-state negative fixture after repository admission.
	postgresql := discovery.Locks["postgresql"]
	postgresql.State = "candidate"
	discovery.Locks["postgresql"] = postgresql
	resolved, err := Resolve(context.Background(), input, discovery)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Installable {
		t.Fatal("candidate Component Catalog entry must not be installable")
	}
	if got := resolved.Components["postgresql"].Version; got != "17.11" {
		t.Fatalf("PostgreSQL version = %q, want 17.11", got)
	}
}

func TestComponentLocksResolveVictoriaCatalogNamesAndCharts(t *testing.T) {
	locks, err := loadComponentLocks("../../bundle/component-catalog.yaml", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	for component, expected := range map[string]struct {
		version, chartName, chartVersion, chartDigest string
	}{
		"victoriaMetrics": {"v1.116.0", "victoria-metrics-single", "0.18.0", "sha256:7ee8361ee6cfca692f0ad836817ceefaa96a9c020a8255f1a8c4f081009da0f1"},
		"victoriaLogs":    {"v1.52.0", "victoria-logs-single", "0.13.9", "sha256:593b3f8e0d26eb925a0e3d59848d17a0abc94597802dc26ba8e0925584176a86"},
		"vmalert":         {"v1.116.0", "victoria-metrics-alert", "0.18.0", "sha256:e2cf619cc58ffac532447654d9d0c072dcbf1185f5c59b86c83472da3cd44c20"},
	} {
		lock, ok := locks[component]
		if !ok || lock.Version != expected.version || lock.ChartName != expected.chartName || lock.ChartVersion != expected.chartVersion || lock.ChartDigest != expected.chartDigest {
			t.Fatalf("%s Component Catalog lock = %#v, want %#v", component, lock, expected)
		}
		if lock.State != "qualified" {
			t.Fatalf("%s state = %q, want reviewed qualified development material", component, lock.State)
		}
	}
}

func TestResolveVirtualizationStopsWhenMatrixIsUnverified(t *testing.T) {
	input := testProfile("detect")
	input.Selected = "virtualization"
	input.Components = map[string]ComponentInput{"kubevirt": {Mode: "bundled", EnabledIn: []string{"virtualization", "full"}}}
	discovery := testDiscovery()
	discovery.Kubernetes.KubeVirt = "unverified"
	if _, err := Resolve(context.Background(), input, discovery); err == nil || !strings.Contains(err.Error(), "UPSTREAM_COMPATIBILITY_UNVERIFIED") {
		t.Fatalf("Resolve error = %v, want UPSTREAM_COMPATIBILITY_UNVERIFIED", err)
	}
}

func testProfile(mode string) InputProfile {
	return InputProfile{
		SchemaVersion: 1,
		Kind:          "detected",
		ProfileID:     "test",
		Environment:   "development",
		Architecture:  "arm64",
		Context:       "orbstack",
		Selected:      "core",
		Kubernetes:    KubernetesDiscovery{Distribution: "orbstack", ServerVersion: "v1.35.6+orb1", Architecture: "arm64", Context: "orbstack", ClusterUID: "test-cluster", StorageClass: "local-path", KubeVirt: "unverified"},
		Runtime:       RuntimeInput{PublicEgress: "deny", CPUBudget: 8, MemoryMiB: 18432, ImageImporter: "orbstack_shared_store"},
		Model:         ModelInput{API: "openai-compatible", Endpoint: "http://host.orb.internal:11434/v1", ProviderForDevelopment: "ollama-0.34.2", Model: "llama3.1:8b-16k", BundleWeights: false},
		Profiles:      map[string][]string{"core": {"platform"}, "deepflow": {"core", "deepflow"}, "virtualization": {"core", "kubevirt", "cdi"}, "full": {"core", "deepflow", "kubevirt", "cdi"}},
		Discovery:     &DiscoveryEvidence{ObservedAt: "2026-09-27T00:00:00Z", Context: "orbstack", ClusterUID: "test-cluster", ServerVersion: "v1.35.6+orb1"},
		Components: map[string]ComponentInput{
			"victoriaMetrics": {Mode: mode},
			"victoriaLogs":    {Mode: mode},
		},
	}
}

func testDiscovery() Discovery {
	return Discovery{
		Kubernetes: KubernetesDiscovery{Distribution: "orbstack", ServerVersion: "v1.35.6+orb1", Architecture: "arm64", Context: "orbstack", ClusterUID: "test-cluster", StorageClass: "local-path", KubeVirt: "unverified"},
		Runtime:    RuntimeInput{ImageImporter: "orbstack_shared_store"},
		Components: map[string][]ComponentCandidate{
			"victoriaMetrics": {{Namespace: "monitoring", Name: "vmsingle-vm", Endpoint: "http://vmsingle-vm.monitoring.svc:8429", Version: "v1.116.0", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Image: "victoriametrics/victoria-metrics@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Compatible: true}},
			"victoriaLogs":    {{Namespace: "monitoring", Name: "victoria-logs", Endpoint: "http://victoria-logs.monitoring.svc:9428", Version: "v1.52.0", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Image: "victoriametrics/victoria-logs@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Compatible: true}},
		},
	}
}
