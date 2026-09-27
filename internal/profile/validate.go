package profile

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?(-[0-9A-Za-z][0-9A-Za-z.-]*)?(\+[0-9A-Za-z.-]+)?$`)
	digestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type InputProfile struct {
	SchemaVersion int                       `yaml:"schemaVersion" json:"schemaVersion"`
	Kind          string                    `yaml:"kind" json:"kind"`
	ProfileID     string                    `yaml:"profileId" json:"profileId"`
	Environment   string                    `yaml:"environment" json:"environment"`
	Architecture  string                    `yaml:"architecture" json:"architecture"`
	Context       string                    `yaml:"context" json:"context"`
	Selected      string                    `yaml:"selectedProfile" json:"selectedProfile"`
	Kubernetes    KubernetesDiscovery       `yaml:"kubernetes" json:"kubernetes"`
	Runtime       RuntimeInput              `yaml:"runtime" json:"runtime"`
	Components    map[string]ComponentInput `yaml:"components" json:"components"`
	Model         ModelInput                `yaml:"model" json:"model"`
	Profiles      map[string][]string       `yaml:"profiles" json:"profiles"`
	Discovery     *DiscoveryEvidence        `yaml:"discovery" json:"discovery,omitempty"`
}

type ComponentInput struct {
	Mode             string   `yaml:"mode" json:"mode"`
	Version          string   `yaml:"version,omitempty" json:"version,omitempty"`
	VersionCandidate string   `yaml:"versionCandidate,omitempty" json:"versionCandidate,omitempty"`
	Digest           string   `yaml:"digest,omitempty" json:"digest,omitempty"`
	Image            string   `yaml:"image,omitempty" json:"image,omitempty"`
	Endpoint         string   `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	Namespace        string   `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Name             string   `yaml:"name,omitempty" json:"name,omitempty"`
	DetectExisting   bool     `yaml:"detectExisting,omitempty" json:"detectExisting,omitempty"`
	EnabledIn        []string `yaml:"enabledIn,omitempty" json:"enabledIn,omitempty"`
	DisabledReason   string   `yaml:"disabledReason,omitempty" json:"disabledReason,omitempty"`
	Evidence         []string `yaml:"evidence,omitempty" json:"evidence,omitempty"`
	Compatibility    string   `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
}

type KubernetesDiscovery struct {
	Distribution      string   `yaml:"distribution" json:"distribution"`
	ServerVersion     string   `yaml:"version,omitempty" json:"version,omitempty"`
	Architecture      string   `yaml:"architecture,omitempty" json:"architecture,omitempty"`
	Context           string   `yaml:"context,omitempty" json:"context,omitempty"`
	ClusterUID        string   `yaml:"clusterUid,omitempty" json:"clusterUid,omitempty"`
	StorageClass      string   `yaml:"storageClass,omitempty" json:"storageClass,omitempty"`
	CRDs              []string `yaml:"crds,omitempty" json:"crds,omitempty"`
	KubeVirt          string   `yaml:"kubevirtCompatibility,omitempty" json:"kubevirtCompatibility,omitempty"`
	KubeVirtInstalled bool     `yaml:"kubevirtInstalled" json:"kubevirtInstalled"`
	KubeVirtVersion   string   `yaml:"kubevirtVersion,omitempty" json:"kubevirtVersion,omitempty"`
	CDIInstalled      bool     `yaml:"cdiInstalled" json:"cdiInstalled"`
	CDIVersion        string   `yaml:"cdiVersion,omitempty" json:"cdiVersion,omitempty"`
}

type RuntimeInput struct {
	PublicEgress  string `yaml:"publicEgress" json:"publicEgress"`
	CPUBudget     int    `yaml:"cpuBudget" json:"cpuBudget"`
	MemoryMiB     int    `yaml:"memoryMiB" json:"memoryMiB"`
	ImageImporter string `yaml:"imageImporter,omitempty" json:"imageImporter,omitempty"`
}

type ModelInput struct {
	API                    string `yaml:"api" json:"api"`
	Endpoint               string `yaml:"endpoint" json:"endpoint"`
	ProviderForDevelopment string `yaml:"providerForDevelopment" json:"providerForDevelopment"`
	Model                  string `yaml:"model" json:"model"`
	BundleWeights          bool   `yaml:"bundleWeights" json:"bundleWeights"`
}

type DiscoveryEvidence struct {
	ObservedAt    string `yaml:"observedAt" json:"observedAt"`
	Context       string `yaml:"context" json:"context"`
	ClusterUID    string `yaml:"clusterUid" json:"clusterUid"`
	ServerVersion string `yaml:"serverVersion" json:"serverVersion"`
}

type ResolvedProfile struct {
	SchemaVersion int                          `yaml:"schemaVersion" json:"schemaVersion"`
	Kind          string                       `yaml:"kind" json:"kind"`
	ProfileID     string                       `yaml:"profileId" json:"profileId"`
	Environment   string                       `yaml:"environment" json:"environment"`
	Architecture  string                       `yaml:"architecture" json:"architecture"`
	Selected      string                       `yaml:"selectedProfile" json:"selectedProfile"`
	Kubernetes    KubernetesDiscovery          `yaml:"kubernetes" json:"kubernetes"`
	Runtime       RuntimeInput                 `yaml:"runtime" json:"runtime"`
	Components    map[string]ResolvedComponent `yaml:"components" json:"components"`
	Model         ModelInput                   `yaml:"model" json:"model"`
	Discovery     DiscoveryEvidence            `yaml:"discovery" json:"discovery"`
	Installable   bool                         `yaml:"installable" json:"installable"`
}

type ResolvedComponent struct {
	Mode           string   `yaml:"mode" json:"mode"`
	Version        string   `yaml:"version,omitempty" json:"version,omitempty"`
	Digest         string   `yaml:"digest,omitempty" json:"digest,omitempty"`
	Image          string   `yaml:"image,omitempty" json:"image,omitempty"`
	Endpoint       string   `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	Namespace      string   `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Name           string   `yaml:"name,omitempty" json:"name,omitempty"`
	Evidence       []string `yaml:"evidence,omitempty" json:"evidence,omitempty"`
	Compatibility  string   `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
	AdmissionState string   `yaml:"admissionState,omitempty" json:"admissionState,omitempty"`
}

type Discovery struct {
	Kubernetes KubernetesDiscovery
	Components map[string][]ComponentCandidate
	Locks      map[string]ComponentLock
	Runtime    RuntimeInput
}

type ComponentCandidate struct {
	Namespace     string
	Name          string
	Endpoint      string
	Version       string
	Digest        string
	Image         string
	Compatible    bool
	Compatibility string
	Evidence      []string
}

type ComponentLock struct {
	Version      string
	Digest       string
	Image        string
	State        string
	ChartName    string
	ChartVersion string
	ChartDigest  string
	ChartSource  string
}

func (profile InputProfile) ValidateTemplate() error {
	if profile.SchemaVersion != 1 {
		return fmt.Errorf("schemaVersion must be 1")
	}
	if strings.TrimSpace(profile.ProfileID) == "" || strings.TrimSpace(profile.Environment) == "" || strings.TrimSpace(profile.Architecture) == "" {
		return fmt.Errorf("profileId, environment, and architecture are required")
	}
	if profile.Architecture != "arm64" && profile.Architecture != "amd64" {
		return fmt.Errorf("unsupported architecture %q", profile.Architecture)
	}
	if profile.Selected == "" {
		return fmt.Errorf("selectedProfile is required")
	}
	if len(profile.Components) == 0 {
		return fmt.Errorf("components must not be empty")
	}
	for name, component := range profile.Components {
		if name == "" {
			return fmt.Errorf("component name must not be empty")
		}
		if component.Mode != "detect" && component.Mode != "external" && component.Mode != "bundled" && component.Mode != "disabled" {
			return fmt.Errorf("component %q has invalid mode %q", name, component.Mode)
		}
	}
	return nil
}

func (profile ResolvedProfile) Validate() error {
	if profile.SchemaVersion != 1 || profile.Kind != "resolved" {
		return fmt.Errorf("resolved profile requires schemaVersion 1 and kind resolved")
	}
	if profile.ProfileID == "" || profile.Selected == "" || profile.Kubernetes.Context == "" || profile.Kubernetes.ServerVersion == "" || profile.Kubernetes.ClusterUID == "" {
		return fmt.Errorf("resolved profile has incomplete cluster identity")
	}
	for name, component := range profile.Components {
		if component.Mode == "detect" || component.Mode == "pending" || component.Version == "pending" || component.Digest == "pending" || component.Endpoint == "pending" {
			return fmt.Errorf("component %q contains unresolved detect/pending value", name)
		}
		if component.Mode == "disabled" {
			continue
		}
		if !versionPattern.MatchString(component.Version) {
			return fmt.Errorf("component %q has missing or inexact version %q", name, component.Version)
		}
		if !digestPattern.MatchString(component.Digest) {
			return fmt.Errorf("component %q has missing or invalid digest %q", name, component.Digest)
		}
		if strings.TrimSpace(component.Endpoint) == "" {
			return fmt.Errorf("component %q has empty endpoint", name)
		}
		if component.Mode == "bundled" && !strings.Contains(component.Image, "@sha256:") {
			return fmt.Errorf("component %q bundled image must be digest pinned", name)
		}
	}
	return nil
}
