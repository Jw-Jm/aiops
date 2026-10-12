package bundle

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed component-catalog.yaml
var componentCatalogYAML []byte

//go:embed component-catalog-amd64.yaml
var componentCatalogAMD64YAML []byte

//go:embed evidence
var componentEvidenceFiles embed.FS

// ComponentCatalog returns the catalog used by Bundle verification.
func ComponentCatalog() []byte {
	return append([]byte(nil), componentCatalogYAML...)
}

// ComponentCatalogForArchitecture selects an independently embedded catalog.
// Target selection never promotes another architecture's image or source lock.
func ComponentCatalogForArchitecture(architecture string) ([]byte, error) {
	switch architecture {
	case "linux/arm64":
		return ComponentCatalog(), nil
	case "linux/amd64":
		return append([]byte(nil), componentCatalogAMD64YAML...), nil
	default:
		return nil, fmt.Errorf("no embedded catalog for architecture %q", architecture)
	}
}

// ComponentEvidence returns the independently embedded evidence files used
// to verify qualified component admission.
func ComponentEvidence() (fs.FS, error) {
	return fs.Sub(componentEvidenceFiles, "evidence")
}
