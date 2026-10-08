package bundle

import (
	"encoding/json"
	"errors"
	"io/fs"

	componentcatalog "ops-platform/bundle"
)

func validateMigrationMaterialAdmission(materials []Material) error {
	var binary, source *Material
	for i := range materials {
		switch materials[i].Name {
		case "db-migrate":
			binary = &materials[i]
		case "db-migrate-source":
			source = &materials[i]
		}
	}
	if binary == nil && source == nil {
		return nil
	}
	evidence, err := componentcatalog.ComponentEvidence()
	if err != nil {
		return errors.New("embedded migration admission unavailable")
	}
	raw, err := fs.ReadFile(evidence, "third_party/admission/migration-tool-source.json")
	var admission struct {
		SchemaVersion                            int `json:"schemaVersion"`
		Name, Version, Architecture, Kind, State string
		BinarySHA256                             string `json:"binarySHA256"`
		SourceBundleSHA256                       string `json:"sourceBundleSHA256"`
	}
	if err != nil || json.Unmarshal(raw, &admission) != nil || admission.SchemaVersion != 1 || admission.Name != "db-migrate" || admission.Kind != "binary" || admission.State != "qualified" || !isSHA256(admission.BinarySHA256) || !isSHA256(admission.SourceBundleSHA256) {
		return errors.New("migration tool lacks qualified exact source/binary admission")
	}
	if source == nil || source.Kind != "source" || source.Version != admission.Version || source.Architecture != admission.Architecture || source.Digest != admission.SourceBundleSHA256 {
		return errors.New("migration tool requires its separate exact source/license closure")
	}
	if binary != nil && (binary.Kind != "binary" || binary.Version != admission.Version || binary.Architecture != admission.Architecture || binary.Digest != admission.BinarySHA256) {
		return errors.New("migration binary differs from offline-rebuilt and real-tested admission")
	}
	return nil
}
