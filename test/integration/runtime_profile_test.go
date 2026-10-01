package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ops-platform/internal/profile"
)

// This describes only the owned loopback integration fixtures. It is not
// Kubernetes discovery or offline installation acceptance evidence.
func isolatedRuntimeProfile(t *testing.T, issuer, bao, archive string) string {
	t.Helper()
	p := profile.ResolvedProfile{
		SchemaVersion: 1, Kind: "resolved", ProfileID: "isolated-sp03-runtime", Environment: "development", Architecture: "arm64", Selected: "core", Installable: true,
		Kubernetes: profile.KubernetesDiscovery{Distribution: "isolated-fixture", Context: "isolated-fixture", ClusterUID: "isolated-fixture", ServerVersion: "v1.35.6+orb1", KubeVirt: "unverified"},
		Runtime:    profile.RuntimeInput{PublicEgress: "deny", CPUBudget: 8, MemoryMiB: 18432, ImageImporter: "orbstack_shared_store"},
		Model:      profile.ModelInput{API: "openai-compatible", Endpoint: "http://127.0.0.1:11434/v1", ProviderForDevelopment: "disabled-in-test", Model: "disabled-in-test"},
		Discovery:  profile.DiscoveryEvidence{ObservedAt: time.Now().UTC().Format(time.RFC3339), Context: "isolated-fixture", ClusterUID: "isolated-fixture", ServerVersion: "v1.35.6+orb1"},
		Components: map[string]profile.ResolvedComponent{
			"kubevirt": {Mode: "disabled", Compatibility: "unverified"}, "cdi": {Mode: "disabled", Compatibility: "unverified"},
		},
	}
	for name, c := range map[string]profile.ResolvedComponent{
		"keycloak":  {Version: "26.7.4", Digest: "sha256:1f91ac24e8d68b8189d5d53a8381464c1db0fcff479348d5de973a86b63d621c", Endpoint: strings.TrimSuffix(issuer, "/realms/ops")},
		"openbao":   {Version: "2.7.0", Digest: "sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6", Endpoint: bao},
		"seaweedfs": {Version: "4.47", Digest: "sha256:d4cf67729aa8777e1a43a5b61d72e5b96179e4b7bac9a221cb14cbc2036cb32e", Endpoint: archive},
	} {
		c.Mode, c.AdmissionState = "external", "external"
		if c.Endpoint == "" {
			c.Mode = "disabled"
		}
		p.Components[name] = c
	}
	path := filepath.Join(t.TempDir(), "resolved-profile.yaml")
	if err := profile.WriteYAML(path, p); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.ReadResolvedProfileFile(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRuntimeProfileFixtureMatchesLockedEndpoints(t *testing.T) {
	path := isolatedRuntimeProfile(t, "http://127.0.0.1:8080/realms/ops", "https://127.0.0.1:8200", "http://127.0.0.1:8333")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
