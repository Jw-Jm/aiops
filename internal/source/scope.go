package source

import (
	"maps"
	"slices"
	"strings"
	"unicode"
)

// DataScopeMapping is a declaration, never a query grant. SP-04 adapters must
// verify backend isolation before enabling any query capability.
type DataScopeMapping struct {
	NativeTenant   string              `json:"nativeTenant,omitempty"`
	Scopes         map[string][]string `json:"scopes,omitempty"`
	RequiredLabels map[string]string   `json:"requiredLabels,omitempty"`
}

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

func sameDataScope(a, b DataScopeMapping) bool {
	return a.NativeTenant == b.NativeTenant && maps.Equal(a.RequiredLabels, b.RequiredLabels) &&
		maps.EqualFunc(a.Scopes, b.Scopes, slices.Equal[[]string])
}
