package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSP04RuntimeRejectsTrailingConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sp04.json")
	valid := `{"Namespace":"owned","CAFile":"ca","CRLFile":"crl","CertificateFile":"cert","PrivateKeyFile":"key","ServerName":"worker"}`
	for _, suffix := range []string{` {}`, ` garbage`} {
		if err := os.WriteFile(path, []byte(valid+suffix), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SP04_RUNTIME_FILE", path)
		if _, err := loadSP04(); err == nil {
			t.Fatalf("accepted trailing configuration %q", suffix)
		}
	}
}
