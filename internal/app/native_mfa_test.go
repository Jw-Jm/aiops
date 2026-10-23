package app

import (
	"ops-platform/internal/identity"
	"testing"
)

func TestNativeMFASecretBackendSelection(t *testing.T) {
	for _, name := range []string{"OPENBAO_MFA_TRANSIT_KEY", "OPS_MFA_SECRET_KEY_FILE", "OPENBAO_CA_FILE"} {
		t.Setenv(name, "")
	}
	sealer, err := nativeMFASealerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sealer.(identity.UnavailableSecretSealer); !ok {
		t.Fatal("missing backend must fail closed")
	}
	t.Setenv("OPENBAO_MFA_TRANSIT_KEY", "task-mfa")
	if _, err := nativeMFASealerFromEnv(); err == nil {
		t.Fatal("Transit without machine identity must not fall back")
	}
	t.Setenv("OPS_MFA_SECRET_KEY_FILE", "/missing")
	if _, err := nativeMFASealerFromEnv(); err == nil {
		t.Fatal("ambiguous backends must be rejected")
	}
}
