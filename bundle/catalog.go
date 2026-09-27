package bundle

import (
	"embed"
	"io/fs"
)

//go:embed component-catalog.yaml
var componentCatalogYAML []byte

//go:embed evidence
var componentEvidenceFiles embed.FS

// ComponentCatalog returns the catalog used by Bundle verification.
func ComponentCatalog() []byte {
	return append([]byte(nil), componentCatalogYAML...)
}

// ComponentEvidence returns the independently embedded evidence files used
// to verify qualified component admission.
func ComponentEvidence() (fs.FS, error) {
	return fs.Sub(componentEvidenceFiles, "evidence")
}
