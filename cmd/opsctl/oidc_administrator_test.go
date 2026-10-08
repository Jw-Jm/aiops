package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestOIDCAdministratorRequiresExplicitStageAndIdentity(t *testing.T) {
	err := run(context.Background(), []string{"bootstrap", "oidc-administrator"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "OIDC administrator requires --stage, --profile, --config, --secrets-file, --ca and --token-file") {
		t.Fatalf("formal verified administrator/retirement entry missing: %v", err)
	}
}
