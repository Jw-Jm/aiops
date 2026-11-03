package source

import (
	"fmt"
	"maps"
	"ops-platform/internal/datascope"
	"slices"
	"strings"
	"unicode"
)

// DataScopeMapping is a declaration, never a query grant. SP-04 adapters must
// verify backend isolation before enabling any query capability.
type DataScopeMapping = datascope.Mapping

func validScopeBinding(backend string, mapping DataScopeMapping) bool {
	if backend != "" && !validOpaqueName(backend, 512) {
		return false
	}
	if (mapping.NativeTenant != "" || len(mapping.Scopes) > 0 || len(mapping.RequiredLabels) > 0) && backend == "" {
		return false
	}
	if mapping.NativeTenant != "" && !validScopeLiteral(mapping.NativeTenant) {
		return false
	}
	if len(mapping.Scopes) > 6 || len(mapping.RequiredLabels) > 32 {
		return false
	}
	for dimension, values := range mapping.Scopes {
		switch dimension {
		case "account", "project", "organization", "team", "cluster", "namespace":
		default:
			return false
		}
		if values == nil || len(values) > 128 {
			return false
		}
		seen := map[string]bool{}
		for _, value := range values {
			if !validScopeLiteral(value) || seen[value] {
				return false
			}
			seen[value] = true
		}
	}
	for key, value := range mapping.RequiredLabels {
		if !validScopeLiteral(key) || !validScopeLiteral(value) {
			return false
		}
	}
	return true
}

func validScopeLiteral(value string) bool {
	return validOpaqueName(value, 512) && !strings.ContainsAny(value, "*|~[]{}()^$\\") &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

// validateTypeScope is the single write-boundary check that keeps every
// registration entry point identical to the runtime collection model.
//
// A Kubernetes source is collected only from an explicit cluster/namespace
// scope: resourcestore.FilterSnapshot and the SP-04 collector both reject a
// native tenant, and the collector lists namespaced resources inside the
// registered namespaces. Accepting a native tenant here would store a
// declaration that can never collect, and would misrepresent it as merely
// "verification pending".
func validateTypeScope(sourceType, backend string, mapping DataScopeMapping, clusterUID string) error {
	if !validScopeBinding(backend, mapping) {
		return ErrInvalidInput
	}
	if sourceType != "kubernetes" {
		return nil
	}
	if mapping.NativeTenant != "" {
		return fmt.Errorf("%w: Kubernetes sources have no native tenant isolation", ErrScopeIncompatible)
	}
	for dimension := range mapping.Scopes {
		if dimension != "cluster" && dimension != "namespace" {
			return fmt.Errorf("%w: Kubernetes sources support only cluster and namespace scope", ErrScopeIncompatible)
		}
	}
	if clusterUID == "" {
		return fmt.Errorf("%w: Kubernetes sources require an active cluster registration", ErrScopeIncompatible)
	}
	if len(mapping.Scopes["cluster"]) != 1 || mapping.Scopes["cluster"][0] != clusterUID {
		return fmt.Errorf("%w: Kubernetes cluster scope must be exactly the registered cluster", ErrScopeIncompatible)
	}
	if len(mapping.Scopes["namespace"]) == 0 {
		return fmt.Errorf("%w: Kubernetes sources require an explicit namespace scope", ErrScopeIncompatible)
	}
	return nil
}

func sameDataScope(a, b DataScopeMapping) bool {
	return a.NativeTenant == b.NativeTenant && maps.Equal(a.RequiredLabels, b.RequiredLabels) &&
		maps.EqualFunc(a.Scopes, b.Scopes, slices.Equal[[]string])
}
