package analytics

import (
	"slices"
	"strings"
	"unicode"

	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

func CloneScope(s graph.Scope) graph.Scope {
	s.Namespaces = slices.Clone(s.Namespaces)
	s.Resources = slices.Clone(s.Resources)
	return s
}

// RestrictScope only narrows a current grant. Empty intersections are rejected:
// an empty resource restriction must never accidentally mean unrestricted.
func RestrictScope(grant graph.Scope, selection Selection) (graph.Scope, error) {
	if grant.Tenant == "" || grant.Cluster == "" || grant.AuthorizationRevision == "" ||
		(!grant.ClusterScoped && len(grant.Namespaces) == 0) {
		return graph.Scope{}, ErrDenied
	}
	out := CloneScope(grant)
	out.ClusterScoped = selection.IncludeClusterScoped && grant.ClusterScoped
	if selection.IncludeClusterScoped && !grant.ClusterScoped {
		return graph.Scope{}, ErrDenied
	}
	if len(selection.Namespaces) > 50 || len(selection.CanonicalIDs) > 50 {
		return graph.Scope{}, ErrInvalid
	}
	if len(selection.Namespaces) > 0 {
		out.Namespaces = []string{}
		for _, ns := range selection.Namespaces {
			if !ValidText(ns, 63) || slices.Contains(out.Namespaces, ns) {
				return graph.Scope{}, ErrInvalid
			}
			if !slices.Contains(grant.Namespaces, ns) {
				return graph.Scope{}, ErrDenied
			}
			out.Namespaces = append(out.Namespaces, ns)
		}
	}
	if !out.ClusterScoped && len(out.Namespaces) == 0 {
		return graph.Scope{}, ErrDenied
	}
	if len(selection.CanonicalIDs) > 0 {
		out.Resources = []string{}
		for _, raw := range selection.CanonicalIDs {
			id, err := resource.ParseCanonicalID(raw)
			if err != nil || slices.Contains(out.Resources, raw) {
				return graph.Scope{}, ErrInvalid
			}
			if id.Tenant != grant.Tenant || id.Scope != grant.Cluster ||
				(len(grant.Resources) > 0 && !slices.Contains(grant.Resources, raw)) {
				return graph.Scope{}, ErrDenied
			}
			if !SupportedKind(id.Kind) || id.Domain != "k8s" {
				return graph.Scope{}, ErrUnsupported
			}
			out.Resources = append(out.Resources, raw)
		}
	}
	slices.Sort(out.Namespaces)
	slices.Sort(out.Resources)
	return out, nil
}

func ValidText(value string, max int) bool {
	return value != "" && len(value) <= max && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

// SupportedKind is deliberately a closed nonvirtual Kubernetes set for this increment.
func SupportedKind(kind string) bool {
	catalog, err := Catalog()
	return err == nil && slices.Contains(catalog.Datasets[0].Kinds, kind)
}

func ClusterScopedKind(kind string) bool {
	catalog, err := Catalog()
	return err == nil && slices.Contains(catalog.Datasets[0].ClusterScopedKinds, kind)
}
