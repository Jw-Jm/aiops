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
	if input.Discovery != nil {
		if input.Discovery.Context != discovery.Kubernetes.Context || input.Discovery.ClusterUID != discovery.Kubernetes.ClusterUID {
			return ResolvedProfile{}, profileConflict("kubernetes", "the current cluster identity differs from the discovery profile")
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
	if resolved.Architecture == "" {
		resolved.Architecture = discovery.Kubernetes.Architecture
	}
	if resolved.Runtime.ImageImporter == "" {
		resolved.Runtime.ImageImporter = discovery.Runtime.ImageImporter
	}
	if input.Discovery != nil {
		resolved.Discovery = *input.Discovery
	}
	for name, component := range input.Components {
		if !isEnabled(component, input.Selected) {
			component.Mode = "disabled"
		}
		if component.Mode == "detect" {
			return ResolvedProfile{}, profileConflict(name, "detect mode must be replaced by an explicit external, bundled, or disabled choice")
		}
		candidates := discovery.Components[name]
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
			if component.Version != "" && component.Version != candidate.Version || component.Digest != "" && component.Digest != candidate.Digest {
				return ResolvedProfile{}, profileConflict(name, "profile lock does not match the currently discovered image")
			}
			resolvedComponent := ResolvedComponent{
				Mode: "external", Version: candidate.Version, Digest: candidate.Digest, Image: candidate.Image,
				Endpoint: candidate.Endpoint, Namespace: candidate.Namespace, Name: candidate.Name,
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
	if candidate.Version == "" || !versionPattern.MatchString(candidate.Version) || !digestPattern.MatchString(candidate.Digest) || candidate.Endpoint == "" || !strings.Contains(candidate.Image, "@sha256:") {
		return ComponentCandidate{}, profileConflict(name, "existing instance is missing an exact version, image digest, or endpoint")
	}
	return candidate, nil
}
