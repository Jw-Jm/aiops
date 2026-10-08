//go:build pre_sp07_live

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/bundle"
)

func TestFormalInstalledSourceBindingVerification(t *testing.T) {
	profilePath := os.Getenv("PRE_SP07_BINDING_PROFILE")
	valuesPath := os.Getenv("PRE_SP07_BINDING_VALUES")
	tokenPath := os.Getenv("PRE_SP07_BINDING_TOKEN")
	if profilePath == "" || valuesPath == "" || tokenPath == "" {
		t.Fatal("actual installed Profile, business values and private OIDC token are required")
	}
	p, err := readResolvedProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.ReadBusinessValues(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifyInstalledSourceBindings(t.Context(), p, b, tokenPath); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil {
		t.Fatal("invalid input")
	}
	cluster := document["sp04"].(map[string]any)["clusters"].([]any)[0].(map[string]any)
	cluster["SourceID"] = uuid.Must(uuid.NewV7()).String()
	negative, _ := json.Marshal(document)
	missing, err := bundle.ReadBusinessValues(bytes.NewReader(negative))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifyInstalledSourceBindings(t.Context(), p, missing, tokenPath); err == nil {
		t.Fatal("unregistered source identity accepted by actual API binding path")
	}
	t.Log("Actual OIDC-authenticated installed SourceRegistration API accepted exact bindings and rejected an unregistered Source ID; no DB seed or query qualification claim")
}
