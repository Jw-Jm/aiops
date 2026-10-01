package profile

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestProfileReadersRejectTrailingDocumentsAndSchemaDrift(t *testing.T) {
	input := testProfile("external")
	raw, _ := yaml.Marshal(input)
	if _, err := ReadProfile(strings.NewReader(string(raw) + "\n---\n{}\n")); err == nil {
		t.Fatal("input profile reader ignored a second document")
	}
	resolved, err := Resolve(context.Background(), input, testDiscovery())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = yaml.Marshal(resolved)
	if _, err := ReadResolvedProfile(strings.NewReader(string(raw))); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{
		string(raw) + "\n---\n{}\n",
		strings.Replace(string(raw), "architecture: arm64", "architecture: mips", 1),
		strings.Replace(string(raw), "publicEgress: deny", "publicEgress: unknown", 1),
		string(raw) + "unknownField: true\n",
	} {
		if _, err := ReadResolvedProfile(strings.NewReader(malformed)); err == nil {
			t.Fatal("resolved profile reader accepted contract drift or another document")
		}
	}
}
