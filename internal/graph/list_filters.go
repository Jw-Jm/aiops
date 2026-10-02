package graph

import (
	"errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/resource"
	"slices"
	"strings"
)

func QueryDigest(q Query) string {
	q.ExpectedOwnerEpoch = 0
	q.CursorRevision = nil
	q.AfterCanonicalID = ""
	return ScopeDigest(q)
}
func validateList(q Query) error {
	if q.QueryKind != "list" {
		return nil
	}
	if !slices.Contains([]string{"", "canonicalId", "name", "kind", "namespace", "health", "updatedAt"}, q.Sort) || !slices.Contains([]string{"", "asc", "desc"}, q.Order) || !slices.Contains([]string{"", "normal", "abnormal", "unknown", "degraded"}, q.Health) {
		return errors.New("INVALID_ARGUMENT")
	}
	if q.Label != "" {
		parts := strings.SplitN(q.Label, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || len(q.Label) > 384 {
			return errors.New("INVALID_ARGUMENT")
		}
	}
	return nil
}
func entityHealth(e resource.Entity) string {
	if health, ok := e.Attributes["health"].(string); ok {
		if health == "critical" {
			return "abnormal"
		}
		return health
	}
	status, ok := e.Attributes["status"].(map[string]any)
	if !ok {
		return "unknown"
	}
	conditions, _, _ := unstructured.NestedSlice(status, "conditions")
	for _, c := range conditions {
		m, _ := c.(map[string]any)
		if m["type"] == "Ready" {
			switch m["status"] {
			case "True":
				return "normal"
			case "False":
				return "abnormal"
			default:
				return "unknown"
			}
		}
	}
	phase, _ := status["phase"].(string)
	switch phase {
	case "Running", "Succeeded", "Bound", "Active":
		return "normal"
	case "Failed":
		return "abnormal"
	case "Pending":
		return "degraded"
	}
	return "unknown"
}
func listMatches(q Query, e resource.Entity) bool {
	if q.Kind != "" && q.Kind != e.Kind || q.Namespace != "" && q.Namespace != e.Namespace || q.Name != "" && q.Name != e.Name || q.Health != "" && q.Health != entityHealth(e) {
		return false
	}
	if q.Label != "" {
		pair := strings.SplitN(q.Label, "=", 2)
		if e.Labels[pair[0]] != pair[1] {
			return false
		}
	}
	return true
}
func listKey(q Query, e resource.Entity) string {
	switch q.Sort {
	case "name":
		return e.Name
	case "kind":
		return e.Kind
	case "namespace":
		return e.Namespace
	case "health":
		return entityHealth(e)
	case "updatedAt":
		return e.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	return e.CanonicalID
}
