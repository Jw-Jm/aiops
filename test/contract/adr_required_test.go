package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiredADRs(t *testing.T) {
	adrs := []struct {
		number string
		file   string
	}{
		{"0001", "0001-modular-monolith.md"},
		{"0002", "0002-two-role-model.md"},
		{"0003", "0003-manual-command-remediation.md"},
		{"0004", "0004-seaweedfs-object-storage.md"},
		{"0005", "0005-airgap-bundle.md"},
		{"0006", "0006-deepflow-boundary.md"},
		{"0007", "0007-openapi-source-of-truth.md"},
	}

	requiredSections := []string{
		"## Context and rationale",
		"## Decision",
		"## Alternatives considered",
		"## Consequences",
		"## Implementation boundaries",
		"## Rollback conditions",
	}

	for _, adr := range adrs {
		adr := adr
		t.Run(adr.number, func(t *testing.T) {
			path := filepath.Join("..", "..", "docs", "adr", adr.file)
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read required ADR %s: %v", adr.number, err)
			}

			text := string(contents)
			if !strings.Contains(text, "# ADR-"+adr.number+":") {
				t.Errorf("ADR %s must identify its number in the title", adr.number)
			}
			if !strings.Contains(text, "Status: Accepted") {
				t.Errorf("ADR %s must have Accepted status", adr.number)
			}
			for _, section := range requiredSections {
				if !strings.Contains(text, section+"\n") {
					t.Errorf("ADR %s is missing required section %q", adr.number, section)
				}
			}
		})
	}
}
