package bundle

import (
	"testing"

	"ops-platform/internal/supplychain"
)

func migrationMaterials() []Material {
	return []Material{{Name: "db-migrate", Kind: "binary", Version: "1.1.0", Architecture: "linux/arm64", Digest: "sha256:5e9a23d88442a8076a2a1e5fef9b63ee24eb6f7192e393501ee820e6bd78c79a"},
		{Name: "db-migrate-source", Kind: "source", Version: "1.1.0", Architecture: "linux/arm64", Digest: "sha256:1593549bab206e0406655774c401846f904ec21f2d97c34037a50a009b0d4a85"}}
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
		func(m []Material) []Material { m[1].Version = "2.0.0"; return m },
	} {
		if err := validateCatalogAdmissionWithCatalog(alter(migrationMaterials()), catalog); err == nil {
			t.Fatal("migration material bypassed independent closure admission")
		}
	}
}

func TestAMD64MigrationRequiresIndependentlyRebuiltAndTestedPair(t *testing.T) {
	materials := []Material{{Name: "db-migrate", Kind: "binary", Version: "1.1.0", Architecture: "linux/amd64", Digest: "sha256:d8360db3cc19d682523b7d1ad1440b2e398d881d08fc35ec9ec62394bced35c8"}, {Name: "db-migrate-source", Kind: "source", Version: "1.1.0", Architecture: "linux/amd64", Digest: "sha256:37c9a2cfe7eb63c878ae9e7a38b3058334c62c7f2802bf847ad74ef29aa5c9ab"}}
	if err := validateMigrationMaterialAdmission(materials); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func([]Material){
		func(m []Material) { m[0].Digest = migrationMaterials()[0].Digest },
		func(m []Material) { m[1].Digest = migrationMaterials()[1].Digest },
		func(m []Material) { m[1].Architecture = "linux/arm64" },
		func(m []Material) {
			m[0].Architecture = "linux/amd64-unreviewed"
			m[1].Architecture = m[0].Architecture
		},
	} {
		copy := append([]Material(nil), materials...)
		alter(copy)
		if err := validateMigrationMaterialAdmission(copy); err == nil {
			t.Fatal("migration architecture or source/binary identity bypassed admission")
		}
	}
}
