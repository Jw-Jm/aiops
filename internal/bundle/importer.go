package bundle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"ops-platform/internal/profile"
)

// RuntimeImporter never pulls an image. Import consumes a previously verified
// local OCI archive, and Verify must prove the exact digest exists in the store.
type RuntimeImporter interface {
	Probe(context.Context, profile.ResolvedProfile) (string, error)
	Import(context.Context, ImageArtifact) error
	Verify(context.Context, ImageArtifact) error
}

type ImageArtifact struct {
	Name         string
	Path         string
	Reference    string
	Digest       string
	Architecture string
}

type ImportReport struct {
	BundleID       string            `json:"bundleId"`
	Driver         string            `json:"driver"`
	Imported       []string          `json:"imported"`
	ImageOperation string            `json:"imageOperation,omitempty"`
	Verified       []string          `json:"verified,omitempty"`
	VerifiedNodes  map[string]string `json:"verifiedNodes,omitempty"`
}

// PlanImport verifies the signed Bundle and every material, then returns the
// exact image references that would be imported. It has no runtime side
// effects and is used to establish the empty-cache precondition before import.
func PlanImport(ctx context.Context, m Manifest, trust TrustRoot, p profile.ResolvedProfile) ([]ImageArtifact, error) {
	verified, err := prepare(ctx, m, trust)
	if err != nil {
		return nil, fmt.Errorf("checkpoint=verify-bundle: %w", err)
	}
	defer verified.close()
	images, err := planImages(verified, p)
	if err != nil {
		return nil, fmt.Errorf("checkpoint=validate-materials: %w", err)
	}
	for i := range images {
		images[i].Path = ""
	}
	return images, nil
}

// Import verifies the entire Bundle and every OCI dependency before touching
// the runtime, even if some images are already cached.
func Import(ctx context.Context, m Manifest, trust TrustRoot, p profile.ResolvedProfile, runtime RuntimeImporter) (ImportReport, error) {
	verified, err := prepare(ctx, m, trust)
	if err != nil {
		return ImportReport{}, fmt.Errorf("checkpoint=verify-bundle: %w", err)
	}
	defer verified.close()
	return importVerified(ctx, verified, p, runtime)
}

func importVerified(ctx context.Context, v *verifiedPayload, p profile.ResolvedProfile, runtime RuntimeImporter) (ImportReport, error) {
	images, err := planImages(v, p)
	if err != nil {
		return ImportReport{}, fmt.Errorf("checkpoint=validate-materials: %w", err)
	}
	return importImages(ctx, v.manifest.BundleID, p, images, runtime)
}

func importImages(ctx context.Context, bundleID string, p profile.ResolvedProfile, images []ImageArtifact, runtime RuntimeImporter) (ImportReport, error) {
	if runtime == nil {
		return ImportReport{}, errors.New("checkpoint=probe-runtime: runtime importer is required")
	}
	driver, err := runtime.Probe(ctx, p)
	if err != nil {
		return ImportReport{}, fmt.Errorf("checkpoint=probe-runtime: %w", err)
	}
	if (driver != "orbstack_shared_store" && driver != "containerd_ctr") || p.Runtime.ImageImporter != driver {
		return ImportReport{}, fmt.Errorf("checkpoint=probe-runtime: CAPABILITY_DISABLED: runtime does not match resolved importer %q", p.Runtime.ImageImporter)
	}
	report := ImportReport{BundleID: bundleID, Driver: driver, Imported: []string{}}
	if mode, ok := runtime.(interface{ ImageOperation() string }); ok {
		report.ImageOperation = mode.ImageOperation()
	}
	if nodes, ok := runtime.(interface{ NodeIdentities() map[string]string }); ok {
		report.VerifiedNodes = nodes.NodeIdentities()
	}
	for _, image := range images {
		if err := runtime.Import(ctx, image); err != nil {
			return report, fmt.Errorf("checkpoint=import-image material=%s: %w", image.Name, err)
		}
		if err := runtime.Verify(ctx, image); err != nil {
			return report, fmt.Errorf("checkpoint=verify-image material=%s: %w", image.Name, err)
		}
		if report.ImageOperation == "verify-preloaded-all-nodes-no-pull" {
			report.Verified = append(report.Verified, image.Name)
		} else {
			report.Imported = append(report.Imported, image.Name)
		}
	}
	return report, nil
}

func validateInstallProfile(p profile.ResolvedProfile, architecture string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !p.Installable {
		return errors.New("candidate components prevent installation")
	}
	if p.Selected != "core" || (p.Environment != "development" && p.Environment != "production") {
		return errors.New("CAPABILITY_DISABLED: only nonvirtual core installation is implemented")
	}
	if p.Kubernetes.Distribution == "" || (p.Runtime.ImageImporter != "containerd_ctr" && (p.Kubernetes.Distribution != "orbstack" || p.Runtime.ImageImporter != "orbstack_shared_store")) {
		return errors.New("CAPABILITY_DISABLED: requires verified containerd_ctr or OrbStack shared store; internal_registry is not implemented")
	}
	if p.Runtime.PublicEgress != "deny" {
		return errors.New("offline profile requires publicEgress deny")
	}
	if (p.Architecture != "arm64" && p.Architecture != "amd64") || architecture != "linux/"+p.Architecture || p.Kubernetes.Architecture != p.Architecture {
		return errors.New("Bundle, Profile and Kubernetes architectures differ")
	}
	for name, component := range p.Components {
		switch component.Mode {
		case "disabled":
		case "external":
		case "bundled":
			if component.AdmissionState != "qualified" {
				return fmt.Errorf("candidate component %s prevents installation", name)
			}
		default:
			return fmt.Errorf("component %s mode is unresolved", name)
		}
		if (name == "kubevirt" || name == "cdi" || name == "deepflow") && component.Mode != "disabled" {
			return fmt.Errorf("CAPABILITY_DISABLED: core cannot activate %s", name)
		}
	}
	return nil
}

func planImages(v *verifiedPayload, p profile.ResolvedProfile) ([]ImageArtifact, error) {
	if err := validateInstallProfile(p, v.manifest.Architecture); err != nil {
		return nil, err
	}
	images := []ImageArtifact{}
	found := map[string]bool{}
	for _, material := range v.manifest.Materials {
		if material.Kind != "container-image" {
			continue
		}
		file := filepath.Join(v.path, filepath.FromSlash(material.PayloadRef))
		digest, reference, err := validateOCIArchive(file, v.manifest.Architecture)
		if err != nil {
			return nil, fmt.Errorf("OCI material %s: %w", material.Name, err)
		}
		if v.catalog == nil {
			return nil, errors.New("verified Component Catalog is required")
		}
		if component, known := v.catalog.Component(material.Name); known {
			if component.Digest != digest || component.Version != material.Version {
				return nil, fmt.Errorf("OCI material %s differs from the qualified catalog image/version", material.Name)
			}
		}
		profileName := material.Name
		if material.Name == "victoria-metrics" {
			profileName = "victoriaMetrics"
		}
		if material.Name == "victoria-logs" {
			profileName = "victoriaLogs"
		}
		externalFallback := false
		if component, ok := p.Components[profileName]; ok {
			if component.Mode == "disabled" {
				return nil, fmt.Errorf("disabled material %s cannot be imported", material.Name)
			}
			if component.Mode == "bundled" {
				if component.Digest != digest || !strings.HasSuffix(component.Image, "@"+digest) || component.Version != material.Version {
					return nil, fmt.Errorf("OCI material %s does not match resolved image/version", material.Name)
				}
				reference = component.Image
			}
			// An external service is locked to its observed version. Its
			// standard fallback archive is independently catalog locked and
			// must not touch the external service's shared image cache. Its
			// full OCI closure is still validated above before skipping import.
			externalFallback = component.Mode == "external"
		}
		if reference == "" || !strings.HasSuffix(reference, "@"+digest) {
			return nil, fmt.Errorf("OCI material %s needs an immutable image reference", material.Name)
		}
		found[profileName] = true
		if externalFallback {
			continue
		}
		images = append(images, ImageArtifact{Name: material.Name, Path: file, Reference: reference, Digest: digest, Architecture: v.manifest.Architecture})
	}
	for name, component := range p.Components {
		if component.Mode == "bundled" && !found[name] {
			return nil, fmt.Errorf("bundled component %s is missing its OCI material", name)
		}
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Name < images[j].Name })
	return images, nil
}
