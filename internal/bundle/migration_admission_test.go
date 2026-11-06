package bundle

import (
	"testing"

	"ops-platform/internal/supplychain"
)

const (
	arm64MigrationBinary = "sha256:5e9a23d88442a8076a2a1e5fef9b63ee24eb6f7192e393501ee820e6bd78c79a"
	arm64MigrationSource = "sha256:89cd8668e55114464c33a2e9154082bf020fef010da150cd47d6fef4c3d9a877"
	amd64MigrationBinary = "sha256:d8360db3cc19d682523b7d1ad1440b2e398d881d08fc35ec9ec62394bced35c8"
	amd64MigrationSource = "sha256:15b74f2d37bece08d35cc6b872d08b174f5ae4982824ead325aa5a5ac8d91897"
)

func migrationMaterials() []Material {
	return []Material{
		{Name: "db-migrate", Kind: "binary", Version: "1.1.0", Architecture: "linux/arm64", Digest: arm64MigrationBinary},
		{Name: "db-migrate-source", Kind: "source", Version: "1.1.0", Architecture: "linux/arm64", Digest: arm64MigrationSource},
	}
}

func TestOfflineMigrationToolRequiresSeparateExactSourceAdmission(t *testing.T) {
	// This first-party CLI has a separately reviewed Goose closure; it cannot
	// borrow the API/Worker's SDK admission or enter through a name allowlist.
	catalog := &supplychain.Catalog{}
	if err := validateCatalogAdmissionWithCatalog(migrationMaterials(), catalog); err != nil {
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
	materials := []Material{
		{Name: "db-migrate", Kind: "binary", Version: "1.1.0", Architecture: "linux/amd64", Digest: amd64MigrationBinary},
		{Name: "db-migrate-source", Kind: "source", Version: "1.1.0", Architecture: "linux/amd64", Digest: amd64MigrationSource},
	}
	if err := validateMigrationMaterialAdmission(materials); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func([]Material){
		func(m []Material) {
			m[1].Digest = "sha256:cc32f9b0306acaa09c58a96c24b734194247a8ce2f6c1ac65ff059c65e9fb438"
		},
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
