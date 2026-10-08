package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeRejectsUnknownIdentityMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(p, []byte(`{"Namespace":"owned","CAFile":"ca","CRLFile":"crl","CertificateFile":"cert","PrivateKeyFile":"key","ServerName":"worker","IdentityMode":"unknown"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SP04_RUNTIME_FILE", p)
	if _, err := loadSP04(); err == nil {
		t.Fatal("unknown identity mode was silently accepted")
	}
}

// A configured production identity mode must never silently fall back to a
// mounted bootstrap certificate when TokenReview/PKI inputs are missing.
func TestRuntimeWorkloadIdentityRequiresConfiguredMode(t *testing.T) {
	for _, mode := range []string{"openbao-kubernetes", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("OPENBAO_ADDR", "")
			c := SP04Config{IdentityMode: mode, Namespace: "owned"}
			if _, _, err := c.tls(context.Background(), true); err == nil {
				t.Fatal("runtime accepted missing workload PKI configuration")
			}
		})
	}
}
