package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSP05AnalyzerCannotAdmitCallerChosenBinaryHash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(p, []byte(`{"Namespace":"ops","CAFile":"ca","CRLFile":"crl","CertificateFile":"cert","PrivateKeyFile":"key","ServerName":"worker","SP05":{"Enabled":true,"Analyzer":{"Enabled":true,"SHA256":"`+strings.Repeat("a", 64)+`"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SP04_RUNTIME_FILE", p)
	_, err := loadSP04()
	if err == nil || !strings.Contains(err.Error(), "admitted binary") {
		t.Fatalf("caller-selected CLI hash passed admission: %v", err)
	}
}
