package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type KubeVirtCompatibilityMatrix struct {
	SchemaVersion int `yaml:"schemaVersion"`
	Source        struct {
		URL    string `yaml:"url"`
		Commit string `yaml:"commit"`
		Digest string `yaml:"digest"`
	} `yaml:"source"`
	KubernetesRows []struct {
		KubeVirtMinor string `yaml:"kubevirtMinor"`
		Supported     []int  `yaml:"supportedKubernetesMinors"`
		EOL           []int  `yaml:"eolKubernetesMinors"`
	} `yaml:"kubernetesRows"`
	Selected struct {
		KubeVirtVersion        string `yaml:"kubevirtVersion"`
		KubernetesMinors       []int  `yaml:"supportedKubernetesMinors"`
		CDIVersion             string `yaml:"cdiVersion"`
		CDICommit              string `yaml:"cdiCommit"`
		CDIDigest              string `yaml:"cdiDigest"`
		CDICompatibilitySource struct {
			URL    string `yaml:"url"`
			Commit string `yaml:"commit"`
			Path   string `yaml:"path"`
			Digest string `yaml:"digest"`
		} `yaml:"cdiCompatibilitySource"`
	} `yaml:"selected"`
}

type CompatibilityDecision struct {
	KubernetesVersion string
	KubeVirtVersion   string
	CDIVersion        string
	SupportedRange    string
	SourceDigest      string
	Decision          string
}

type VirtualizationCapability struct {
	KVM             bool     `json:"kvm" yaml:"kvm"`
	Emulation       bool     `json:"emulation" yaml:"emulation"`
	KubeVirtVersion string   `json:"kubevirtVersion" yaml:"kubevirtVersion"`
	CDIVersion      string   `json:"cdiVersion" yaml:"cdiVersion"`
	Architectures   []string `json:"architectures" yaml:"architectures"`
}

var (
	kubernetesVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+][0-9A-Za-z.-]+)?$`)
	commitHashPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256DigestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func LoadKubeVirtCompatibilityMatrix(path string) (KubeVirtCompatibilityMatrix, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return KubeVirtCompatibilityMatrix{}, fmt.Errorf("read KubeVirt compatibility matrix %q: %w", path, err)
	}
	var matrix KubeVirtCompatibilityMatrix
	if err := yaml.Unmarshal(contents, &matrix); err != nil {
		return KubeVirtCompatibilityMatrix{}, fmt.Errorf("decode KubeVirt compatibility matrix: %w", err)
	}
	if err := matrix.Validate(); err != nil {
		return KubeVirtCompatibilityMatrix{}, err
	}
	return matrix, nil
}

func (matrix KubeVirtCompatibilityMatrix) Validate() error {
	if matrix.SchemaVersion != 1 {
		return fmt.Errorf("KubeVirt compatibility matrix schemaVersion must be 1")
	}
	if matrix.Source.URL == "" || !commitHashPattern.MatchString(matrix.Source.Commit) || !sha256DigestPattern.MatchString(matrix.Source.Digest) {
		return fmt.Errorf("KubeVirt compatibility matrix source requires URL, full commit, and sha256 digest")
	}
	if len(matrix.KubernetesRows) == 0 {
		return fmt.Errorf("KubeVirt compatibility matrix has no official support rows")
	}
	selectedVersion := strings.TrimPrefix(matrix.Selected.KubeVirtVersion, "v")
	if !versionPattern.MatchString(selectedVersion) || !versionPattern.MatchString(matrix.Selected.CDIVersion) ||
		!commitHashPattern.MatchString(matrix.Selected.CDICommit) || !sha256DigestPattern.MatchString(matrix.Selected.CDIDigest) {
		return fmt.Errorf("selected KubeVirt/CDI compatibility lock is incomplete or inexact")
	}
	if matrix.Selected.CDICompatibilitySource.URL == "" || !commitHashPattern.MatchString(matrix.Selected.CDICompatibilitySource.Commit) ||
		matrix.Selected.CDICompatibilitySource.Path == "" || !sha256DigestPattern.MatchString(matrix.Selected.CDICompatibilitySource.Digest) {
		return fmt.Errorf("selected CDI compatibility source requires URL, full commit, path, and sha256 digest")
	}
	if len(matrix.Selected.KubernetesMinors) == 0 {
		return fmt.Errorf("selected KubeVirt release has no supported Kubernetes minors")
	}
	rowFound := false
	for _, row := range matrix.KubernetesRows {
		if row.KubeVirtMinor == selectedVersion[:strings.LastIndex(selectedVersion, ".")] {
			rowFound = true
			for _, minor := range matrix.Selected.KubernetesMinors {
				if !containsInt(row.Supported, minor) {
					return fmt.Errorf("selected KubeVirt release %s includes unsupported Kubernetes 1.%d", selectedVersion, minor)
				}
			}
		}
	}
	if !rowFound {
		return fmt.Errorf("selected KubeVirt release %s is absent from the official support rows", selectedVersion)
	}
	return nil
}

func DecideKubeVirtCompatibility(kubernetesVersion string, matrix KubeVirtCompatibilityMatrix) CompatibilityDecision {
	decision := CompatibilityDecision{
		KubernetesVersion: kubernetesVersion,
		SourceDigest:      matrix.Source.Digest,
		Decision:          "unverified",
	}
	if matrix.Validate() != nil {
		return decision
	}
	match := kubernetesVersionPattern.FindStringSubmatch(kubernetesVersion)
	if len(match) != 4 {
		return decision
	}
	major, majorErr := strconv.Atoi(match[1])
	minor, minorErr := strconv.Atoi(match[2])
	if majorErr != nil || minorErr != nil || major != 1 {
		return decision
	}
	decision.Decision = "unsupported"
	if containsInt(matrix.Selected.KubernetesMinors, minor) {
		decision.KubeVirtVersion = matrix.Selected.KubeVirtVersion
		decision.CDIVersion = matrix.Selected.CDIVersion
		decision.SupportedRange = supportedRange(matrix.Selected.KubernetesMinors)
		decision.Decision = "supported"
	}
	return decision
}

func compatibilityMatrixPath(catalogPath string) string {
	if catalogPath == "" {
		catalogPath = filepath.Join("bundle", "component-catalog.yaml")
	}
	root := filepath.Dir(filepath.Dir(catalogPath))
	return filepath.Join(root, "docs", "compatibility", "kubevirt-kubernetes-matrix.yaml")
}

func sameRelease(left, right string) bool {
	return strings.TrimPrefix(left, "v") == strings.TrimPrefix(right, "v")
}

func supportedRange(minors []int) string {
	ordered := append([]int(nil), minors...)
	sort.Ints(ordered)
	if len(ordered) == 1 {
		return fmt.Sprintf("Kubernetes 1.%d", ordered[0])
	}
	if len(ordered) > 1 {
		return fmt.Sprintf("Kubernetes 1.%d-1.%d", ordered[0], ordered[len(ordered)-1])
	}
	return ""
}
