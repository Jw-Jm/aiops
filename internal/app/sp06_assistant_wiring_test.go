package app

import (
	"ops-platform/internal/modelprofile"
	"slices"
	"testing"
)

// A disabled assistant block is what the installer renders for every SP-02
// non-virtualized installation. Wiring handlers from its presence alone
// dereferenced a nil Profile and crash-looped the API, so the enabled flag and
// the Profile itself both have to gate the wiring.
func TestAssistantTaskKindsRequiresEnabledProfile(t *testing.T) {
	for name, config := range map[string]*AssistantRuntimeConfig{
		"absent":           nil,
		"disabled":         {Enabled: false, ModelProfileVersion: ""},
		"disabled-profile": {Enabled: false, Profile: &modelprofile.Profile{TaskKinds: []string{"data_query"}}},
		"enabled-no-profile": {Enabled: true, ModelProfileVersion: "v1@sha256:" +
			"0000000000000000000000000000000000000000000000000000000000000000"},
	} {
		t.Run(name, func(t *testing.T) {
			if kinds, ok := assistantTaskKinds(config); ok || kinds != nil {
				t.Fatalf("assistant wiring admitted %s: kinds=%v ok=%v", name, kinds, ok)
			}
		})
	}

	profile := &modelprofile.Profile{TaskKinds: []string{"data_query", "resource_troubleshooting"}}
	kinds, ok := assistantTaskKinds(&AssistantRuntimeConfig{Enabled: true, Profile: profile})
	if !ok || !slices.Equal(kinds, profile.TaskKinds) {
		t.Fatalf("enabled assistant lost its frozen task kinds: kinds=%v ok=%v", kinds, ok)
	}
	kinds[0] = "mutated"
	if profile.TaskKinds[0] != "data_query" {
		t.Fatal("frozen task kinds must be copied, not aliased")
	}
}
