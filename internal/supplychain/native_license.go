package supplychain

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

// This finite registry is compiled independently of the input catalog. It is
// not extended by a catalog field, an arbitrary SPDX LicenseRef or scanner text.
//
//go:embed licenses/native-core-reviewed.json
var nativeLicenseRegistry []byte

// SP06 records are also a finite, independently compiled reviewed inventory.
// An input Catalog cannot create or broaden these identities.
//
//go:embed licenses/sp06-investigator-reviewed.json
var investigatorLicenseRegistry []byte

// These four publisher notice scopes belong only to the measured Metrics
// distribution. A Catalog entry cannot create additional permission scopes.
//
//go:embed licenses/pre-sp07-metrics-reviewed.json
var metricsLicenseRegistry []byte

// SP07 is scoped to the exact one-shot Runner image/source/notice identities.
//go:embed licenses/sp07-runner-reviewed.json
var commandRunnerLicenseRegistry []byte

type nativeLicenseScope struct {
	ID                              string `json:"id"`
	Component                       string `json:"component"`
	ComponentVersion                string `json:"componentVersion"`
	ImageDigest                     string `json:"imageDigest"`
	DependencyName                  string `json:"dependencyName"`
	Version                         string `json:"version"`
	Source                          string `json:"source"`
	SourceArchiveSHA256             string `json:"sourceArchiveSHA256"`
	CorrespondingSourceBundleSHA256 string `json:"correspondingSourceBundleSHA256"`
	NoticePath                      string `json:"noticePath"`
	NoticeDigest                    string `json:"noticeDigest"`
	ADR                             string `json:"adr"`
	ADRDigest                       string `json:"adrDigest"`
}

var nativeLicenses = loadNativeLicenseScopes()

func loadNativeLicenseScopes() map[string]nativeLicenseScope {
	var document struct {
		SchemaVersion int                  `json:"schemaVersion"`
		Licenses      []nativeLicenseScope `json:"licenses"`
	}
	result := make(map[string]nativeLicenseScope)
	for _, inventory := range [][]byte{nativeLicenseRegistry, investigatorLicenseRegistry, metricsLicenseRegistry, commandRunnerLicenseRegistry} {
		if err := json.Unmarshal(inventory, &document); err != nil || document.SchemaVersion != 1 {
			return nil // A malformed compiled registry rejects every native reference.
		}
		for _, record := range document.Licenses {
			if record.ID == "" || result[record.ID].ID != "" || !digestPattern.MatchString(record.ImageDigest) ||
				!digestPattern.MatchString(record.SourceArchiveSHA256) || !digestPattern.MatchString(record.NoticeDigest) ||
				!digestPattern.MatchString(record.ADRDigest) || !digestPattern.MatchString(record.CorrespondingSourceBundleSHA256) {
				return nil
			}
			result[record.ID] = record
		}
	}
	return result
}

func validateNativeLicenseParent(component Component, scope nativeLicenseScope) error {
	if component.Name != scope.Component || component.Version != scope.ComponentVersion || component.Digest != scope.ImageDigest ||
		component.CorrespondingSourceBundleSHA256 != scope.CorrespondingSourceBundleSHA256 ||
		component.SpecialLicenseADR != scope.ADR {
		return fmt.Errorf("native license reference %q does not match its reviewed image/source/ADR scope", scope.ID)
	}
	return nil
}

func validateNativeDependency(component Component, dependency Dependency) error {
	scope, found := nativeLicenses[dependency.License]
	if !found {
		if strings.Contains(dependency.License, "LicenseRef-") {
			return fmt.Errorf("native references cannot be combined with other license choices")
		}
		return nil
	}
	if err := validateNativeLicenseParent(component, scope); err != nil {
		return err
	}
	if dependency.Name != scope.DependencyName || dependency.Version != scope.Version || dependency.Source != scope.Source ||
		dependency.SourceType != "archive" || dependency.SourceArchiveSHA256 != scope.SourceArchiveSHA256 {
		return fmt.Errorf("native license reference %q does not match its reviewed dependency identity", scope.ID)
	}
	for _, file := range component.FileLicenses {
		if file.License == scope.ID && file.Path == scope.NoticePath && file.Digest == scope.NoticeDigest {
			return nil
		}
	}
	return fmt.Errorf("native license reference %q is missing its exact publisher notice inventory", scope.ID)
}

func validateNativeFile(component Component, file FileLicense) error {
	scope, found := nativeLicenses[file.License]
	if !found {
		if strings.Contains(file.License, "LicenseRef-") {
			return fmt.Errorf("native references cannot be combined with other license choices")
		}
		return nil
	}
	if err := validateNativeLicenseParent(component, scope); err != nil {
		return err
	}
	if file.Path != scope.NoticePath || file.Digest != scope.NoticeDigest {
		return fmt.Errorf("native license reference %q does not match its reviewed file notice", scope.ID)
	}
	return nil
}

func verifyEvidenceDigest(evidence fs.FS, name, expected string) error {
	if err := validateEvidenceFile(evidence, "license/source evidence", name); err != nil {
		return err
	}
	contents, err := fs.ReadFile(evidence, name)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(contents)
	if "sha256:"+hex.EncodeToString(digest[:]) != expected {
		return fmt.Errorf("reviewed license/source evidence %q checksum changed", name)
	}
	return nil
}

func validateNativeLicenseEvidence(component Component, evidence fs.FS) error {
	seen := map[string]bool{}
	for _, file := range component.FileLicenses {
		scope, found := nativeLicenses[file.License]
		if !found || seen[scope.ID] {
			continue
		}
		if err := verifyEvidenceDigest(evidence, scope.NoticePath, scope.NoticeDigest); err != nil {
			return err
		}
		if err := verifyEvidenceDigest(evidence, scope.ADR, scope.ADRDigest); err != nil {
			return err
		}
		seen[scope.ID] = true
	}
	return nil
}
