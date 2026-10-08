package profile

import (
	"context"
	"fmt"
	"strings"
)

func Resolve(ctx context.Context, input InputProfile, discovery Discovery) (ResolvedProfile, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedProfile{}, err
	}
	if input.Kind == "template" {
		return ResolvedProfile{}, fmt.Errorf("PROFILE_INPUT_NOT_DETECTED: repository templates cannot be used directly; run profile detect or provide an operator-edited detected profile")
	}
	if input.Selected == "" {
		input.Selected = "core"
	}
	if err := input.ValidateTemplate(); err != nil {
		return ResolvedProfile{}, err
	}
	if input.Architecture != discovery.Kubernetes.Architecture {
		return ResolvedProfile{}, profileConflict("kubernetes", "the discovered architecture differs from the input lock")
	}
	if input.Runtime.ImageImporter != "" && discovery.Runtime.ImageImporter != "" && input.Runtime.ImageImporter != discovery.Runtime.ImageImporter {
		return ResolvedProfile{}, profileConflict("runtime", "the discovered image importer differs from the input lock")
	}
	if input.Discovery != nil {
		if input.Discovery.Context != discovery.Kubernetes.Context || input.Discovery.ClusterUID != discovery.Kubernetes.ClusterUID {
			return ResolvedProfile{}, profileConflict("kubernetes", "the current cluster identity differs from the discovery profile")
		}
		if input.Discovery.ServerVersion != discovery.Kubernetes.ServerVersion {
			return ResolvedProfile{}, profileConflict("kubernetes", "the current server version differs from the discovery profile; detect again")
		}
	}
	if input.Selected == "virtualization" || input.Selected == "full" {
		switch discovery.Kubernetes.KubeVirt {
		case "supported":
		case "unsupported":
			return ResolvedProfile{}, fmt.Errorf("KUBEVIRT_KUBERNETES_UNSUPPORTED: Kubernetes %s has no supported KubeVirt release in the frozen matrix", discovery.Kubernetes.ServerVersion)
		default:
			return ResolvedProfile{}, fmt.Errorf("UPSTREAM_COMPATIBILITY_UNVERIFIED: Kubernetes %s has no frozen, verified KubeVirt/CDI matrix", discovery.Kubernetes.ServerVersion)
		}
	}
	resolved := ResolvedProfile{
		SchemaVersion: 1,
		Kind:          "resolved",
		ProfileID:     input.ProfileID,
		Environment:   input.Environment,
		Architecture:  input.Architecture,
		Selected:      input.Selected,
		Kubernetes:    discovery.Kubernetes,
		Runtime:       input.Runtime,
		Components:    make(map[string]ResolvedComponent, len(input.Components)),
		Model:         input.Model,
		Installable:   true,
	}
	if input.Kubernetes.StorageClass != "" {
		resolved.Kubernetes.StorageClass = input.Kubernetes.StorageClass
	}
	if resolved.Architecture == "" {
		resolved.Architecture = discovery.Kubernetes.Architecture
	}
	if resolved.Runtime.ImageImporter == "" {
		resolved.Runtime.ImageImporter = discovery.Runtime.ImageImporter
	}
	if input.Discovery != nil {
		resolved.Discovery = *input.Discovery
	}
	if input.Selected == "core" {
		resolved.Kubernetes.KubeVirt = "unverified"
	}
	for name, component := range input.Components {
		if input.Selected == "core" && (name == "kubevirt" || name == "cdi") {
			resolved.Components[name] = ResolvedComponent{Mode: "disabled", Compatibility: "unverified"}
			continue
		}
		if !isEnabled(component, input.Selected) {
			component.Mode = "disabled"
		}
		if component.Mode == "detect" {
			return ResolvedProfile{}, profileConflict(name, "detect mode must be replaced by an explicit external, bundled, or disabled choice")
		}
		candidates := selectedComponentCandidates(component, discovery.Components[name])
		if component.Mode == "disabled" {
			resolved.Components[name] = ResolvedComponent{Mode: "disabled", Compatibility: component.Compatibility}
			continue
		}
		if len(candidates) > 1 {
			return ResolvedProfile{}, profileConflict(name, "multiple candidate instances were discovered")
		}
		switch component.Mode {
		case "external":
			candidate, err := selectExternalCandidate(name, component, candidates)
			if err != nil {
				return ResolvedProfile{}, err
			}
			if component.Version != "" && component.Version != candidate.Version || component.Digest != "" && component.Digest != candidate.Digest ||
				component.Endpoint != "" && component.Endpoint != candidate.Endpoint || component.Image != "" && component.Image != candidate.Image ||
				component.ObjectUID != "" && component.ObjectUID != candidate.ObjectUID {
				return ResolvedProfile{}, profileConflict(name, "profile lock does not match the currently discovered image")
			}
			resolvedComponent := ResolvedComponent{
				Mode: "external", Version: candidate.Version, Digest: candidate.Digest, Image: candidate.Image,
				Endpoint: candidate.Endpoint, Namespace: candidate.Namespace, Name: candidate.Name, ObjectUID: candidate.ObjectUID,
				Evidence: append([]string(nil), candidate.Evidence...), AdmissionState: "external", Compatibility: candidate.Compatibility,
			}
			if resolvedComponent.Compatibility == "" {
				resolvedComponent.Compatibility = "supported"
			}
			resolved.Components[name] = resolvedComponent
		case "bundled":
			if len(candidates) != 0 {
				return ResolvedProfile{}, profileConflict(name, "profile explicitly selects bundled while an existing instance is present")
			}
			lock, ok := discovery.Locks[name]
			if !ok {
				return ResolvedProfile{}, profileConflict(name, "an exact arm64 version/digest lock is missing from the Component Catalog")
			}
			if component.Version != "" && component.Version != lock.Version || component.Digest != "" && component.Digest != lock.Digest {
				return ResolvedProfile{}, profileConflict(name, "profile lock does not match the Component Catalog")
			}
			if strings.TrimSpace(component.Endpoint) == "" {
				return ResolvedProfile{}, profileConflict(name, "bundled endpoint must be defined by the profile")
			}
			resolved.Components[name] = ResolvedComponent{
				Mode: "bundled", Version: lock.Version, Digest: lock.Digest, Image: lock.Image,
				Namespace: component.Namespace, Name: component.Name,
				Endpoint: component.Endpoint, Evidence: append([]string(nil), component.Evidence...), AdmissionState: lock.State,
			}
			if lock.State != "qualified" {
				resolved.Installable = false
			}
		default:
			return ResolvedProfile{}, profileConflict(name, "component mode must be external, bundled, or disabled")
		}
	}
	if err := resolved.Validate(); err != nil {
		return ResolvedProfile{}, err
	}
	if err := validateProfileSchema(resolved); err != nil {
		return ResolvedProfile{}, err
	}
	return resolved, nil
}

func selectExternalCandidate(name string, input ComponentInput, candidates []ComponentCandidate) (ComponentCandidate, error) {
	filtered := make([]ComponentCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if input.Namespace != "" && input.Namespace != candidate.Namespace || input.Name != "" && input.Name != candidate.Name {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) != 1 {
		return ComponentCandidate{}, profileConflict(name, "external mode requires exactly one discovered instance matching the selected identity")
	}
	candidate := filtered[0]
	if !candidate.Compatible {
		return ComponentCandidate{}, profileConflict(name, "existing instance is incompatible or its compatibility is unknown")
	}
	if candidate.ObjectUID == "" || candidate.Namespace == "" || candidate.Name == "" {
		return ComponentCandidate{}, profileConflict(name, "existing instance is missing its observed Service object identity")
	}
	if candidate.Version == "" || !versionPattern.MatchString(candidate.Version) || !digestPattern.MatchString(candidate.Digest) || candidate.Endpoint == "" || !strings.Contains(candidate.Image, "@sha256:") {
		return ComponentCandidate{}, profileConflict(name, "existing instance is missing an exact version, image digest, or endpoint")
	}
	return candidate, nil
}

// An explicit namespace/name targets one installation. Unselected shared
// services are observed but never adopted, overwritten, or used as a fallback.
func selectedComponentCandidates(input ComponentInput, candidates []ComponentCandidate) []ComponentCandidate {
	out := make([]ComponentCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if input.Namespace != "" && input.Namespace != candidate.Namespace || input.Name != "" && input.Name != candidate.Name {
			continue
		}
		out = append(out, candidate)
	}
	return out
}
