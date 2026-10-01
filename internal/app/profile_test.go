package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ops-platform/internal/profile"
)

func TestRuntimeRejectsProfileEndpointAndAdmissionDrift(t *testing.T) {
	p := profile.ResolvedProfile{
		SchemaVersion: 1, Kind: "resolved", ProfileID: "fixture", Environment: "development", Architecture: "arm64", Selected: "core", Installable: true,
		Kubernetes: profile.KubernetesDiscovery{Distribution: "fixture", Context: "fixture", ClusterUID: "fixture", ServerVersion: "v1.35.6", KubeVirt: "unverified"},
		Runtime:    profile.RuntimeInput{PublicEgress: "deny", CPUBudget: 1, MemoryMiB: 1024},
		Model:      profile.ModelInput{API: "openai-compatible", Endpoint: "http://localhost:11434/v1", ProviderForDevelopment: "fixture", Model: "fixture"},
		Discovery:  profile.DiscoveryEvidence{ObservedAt: "2026-10-01T00:00:00Z", Context: "fixture", ClusterUID: "fixture", ServerVersion: "v1.35.6"},
		Components: map[string]profile.ResolvedComponent{"kubevirt": {Mode: "disabled", Compatibility: "unverified"}, "cdi": {Mode: "disabled", Compatibility: "unverified"}},
	}
	for name, endpoint := range map[string]string{"keycloak": "https://identity.example.test", "openbao": "https://bao.example.test", "seaweedfs": "http://archive.example.test"} {
		p.Components[name] = profile.ResolvedComponent{Mode: "external", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64), Endpoint: endpoint}
	}
	config := AppConfig{ProfilePath: filepath.Join(t.TempDir(), "profile.yaml"), OIDCIssuerURL: "https://identity.example.test/realms/ops"}
	write := func() {
		t.Helper()
		if err := profile.WriteYAML(config.ProfilePath, p); err != nil {
			t.Fatal(err)
		}
	}
	write()
	t.Setenv("OPENBAO_ADDR", p.Components["openbao"].Endpoint)
	t.Setenv("S3_ENDPOINT", p.Components["seaweedfs"].Endpoint)
	for _, worker := range []bool{false, true} {
		if err := config.validateRuntimeProfile(worker); err != nil {
			t.Fatal(err)
		}
	}
	config.OIDCIssuerURL = "https://different.example.test/realms/ops"
	if err := config.validateRuntimeProfile(false); err == nil {
		t.Fatal("API accepted issuer drift")
	}
	for _, name := range []string{"OPENBAO_ADDR", "S3_ENDPOINT"} {
		old := os.Getenv(name)
		t.Setenv(name, "https://different.example.test")
		if err := config.validateRuntimeProfile(true); err == nil {
			t.Fatalf("worker accepted %s drift", name)
		}
		t.Setenv(name, old)
	}
	p.Installable = false
	write()
	if err := config.validateRuntimeProfile(true); err == nil {
		t.Fatal("unqualified profile admitted")
	}
	p.Installable = true
	p.Components["cdi"] = profile.ResolvedComponent{Mode: "disabled", Compatibility: "supported"}
	write()
	if err := config.validateRuntimeProfile(true); err == nil {
		t.Fatal("virtualization compatibility bypass admitted")
	}
}
