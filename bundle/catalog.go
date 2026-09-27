package bundle

import _ "embed"

//go:embed component-catalog.yaml
var componentCatalogYAML []byte

// ComponentCatalog returns the catalog used by Bundle verification.
func ComponentCatalog() []byte {
	return append([]byte(nil), componentCatalogYAML...)
}
