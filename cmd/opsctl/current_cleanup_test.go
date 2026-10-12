package main

import (
	"bytes"
	"testing"
)

func TestCleanupRequiresExplicitInstalledArtifactAndScope(t *testing.T) {
	for _, args := range [][]string{{"cleanup"}, {"cleanup", "--apply"}, {"cleanup", "--namespace", "foreign"}, {"cleanup", "--release", "arbitrary"}} {
		var out, errs bytes.Buffer
		if err := run(t.Context(), args, &out, &errs); err == nil {
			t.Fatalf("incomplete or unbounded cleanup accepted: %v", args)
		}
		if out.Len() != 0 {
			t.Fatalf("incomplete cleanup produced a result: %s", out.String())
		}
	}
}
