package bundle

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSP07ActualBusinessValues(t *testing.T) {
	path := os.Getenv("SP07_NATIVE_BUSINESS_VALUES_FILE")
	if path == "" {
		t.Skip("actual external operator input required")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := ReadBusinessValues(f)
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &original) != nil {
		t.Fatal("invalid JSON")
	}
	profiles := object(original, "sp07")["admittedProfiles"].([]any)
	image := profiles[0].(map[string]any)["toolImageDigest"].(string)
	holmes := os.Getenv("SP07_NATIVE_INVESTIGATOR_IMAGE")
	if holmes == "" {
		t.Fatal("actual installed admitted investigator image required")
	}
	values := map[string]any{"components": map[string]any{}}
	if err = applyBusinessValues(values, b, map[string]string{"command-runner": image, "holmesgpt": holmes}); err != nil {
		t.Fatal(err)
	}
	if object(values, "sp07")["enabled"] != true {
		t.Fatal("SP07 not enabled through product installer")
	}
	if err = applyBusinessValues(map[string]any{}, b, map[string]string{"command-runner": "wrong@sha256:unverified", "holmesgpt": holmes}); err == nil {
		t.Fatal("unadmitted runner image accepted")
	}
}
