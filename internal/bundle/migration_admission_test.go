package bundle

import (
	"testing"

	"ops-platform/internal/supplychain"
)

func migrationMaterials() []Material {
	return []Material{{Name: "db-migrate", Kind: "binary", Version: "1.0.0", Architecture: "linux/arm64", Digest: "sha256:5e9a23d88442a8076a2a1e5fef9b63ee24eb6f7192e393501ee820e6bd78c79a"},
		{Name: "db-migrate-source", Kind: "source", Version: "1.0.0", Architecture: "linux/arm64", Digest: "sha256:7eae032b1b99f0aec4b0ddbc6398e304fabeb68c176cf502c8c0f83bbc8d7639"}}
}

func TestOfflineMigrationToolRequiresSeparateExactSourceAdmission(t *testing.T) {
	// This first-party CLI has a separately reviewed Goose closure; it cannot
	// borrow the API/Worker's SDK admission or enter through a name allowlist.
	catalog := &supplychain.Catalog{}
	materials := migrationMaterials()
	if err := validateCatalogAdmissionWithCatalog(materials, catalog); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func([]Material) []Material{
		func(m []Material) []Material { return m[:1] },
		func(m []Material) []Material { m[1].Digest = "sha256:" + string(make([]byte, 64)); return m },
		func(m []Material) []Material { m[0].Architecture = "linux/amd64"; return m },
		func(m []Material) []Material { m[0].Kind = "container-image"; return m },
		func(m []Material) []Material { m[1].Version = "1.1.0"; return m },
	} {
		if err := validateCatalogAdmissionWithCatalog(alter(migrationMaterials()), catalog); err == nil {
			t.Fatal("migration material bypassed independent closure admission")
		}
	}
}
