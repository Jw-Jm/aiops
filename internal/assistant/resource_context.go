// Package assistant contains the platform-owned conversation context primitives.
package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/resource"
)

type ResourceSelector struct {
	CanonicalID string `json:"canonicalId,omitempty"`
	Name        string `json:"name,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
}
type Candidate struct {
	CanonicalID string    `json:"canonicalId"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Namespace   string    `json:"namespace"`
	ObservedAt  time.Time `json:"observedAt"`
}
type Resolution struct {
	Status              string         `json:"status"`
	Candidates          []Candidate    `json:"candidates"`
	MissingFields       []string       `json:"missingFields"`
	Truncated           bool           `json:"truncated"`
	ObservationRevision graph.Revision `json:"observationRevision"`
	SnapshotReadAt      time.Time      `json:"snapshotReadAt"`
	Freshness           string         `json:"freshness"`
	// Only the resolver can construct a binding from this observation.
	scope          graph.Scope
	grantDigest    string
	subject        string
	resolvedDigest string
}
type TimeRange struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Timezone string    `json:"timezone"`
}
type ResourceContext struct {
	SchemaVersion        string         `json:"schemaVersion"`
	ContextID            uuid.UUID      `json:"contextId"`
	TenantID             string         `json:"tenantId"`
	OwnerSubject         string         `json:"ownerSubject"`
	ClusterUID           string         `json:"clusterUid"`
	Resources            []Candidate    `json:"resources"`
	Symptom              string         `json:"symptom"`
	TimeRange            TimeRange      `json:"timeRange"`
	ResolvedAt           time.Time      `json:"resolvedAt"`
	ObservationRevision  graph.Revision `json:"observationRevision"`
	GrantSnapshotDigest  string         `json:"grantSnapshotDigest"`
	ExecutionScopeDigest string         `json:"executionScopeDigest"`
	ExecutionScope       graph.Scope    `json:"executionScope"`
	ContextDigest        string         `json:"contextDigest"`
}

type ResourceResolver struct {
	Authorization analytics.ScopeReader
	Graph         analytics.ResourceReader
}

func DecodeResourceSelector(raw []byte) (ResourceSelector, error) {
	var selector ResourceSelector
	if len(raw) > 4096 || contract.Validate("https://ops.local/schemas/resource-selector/v1", raw) != nil {
		return selector, analytics.ErrInvalid
	}
	if json.Unmarshal(raw, &selector) != nil {
		return selector, analytics.ErrInvalid
	}
	return selector, nil
}

// Resolve performs exact authorized lookup. It does not invent identities from
// names, read resource attributes into a prompt, or create a Finding/Incident.
func (r ResourceResolver) Resolve(ctx context.Context, binding analytics.Binding, selector ResourceSelector) (Resolution, error) {
	result := Resolution{Candidates: []Candidate{}, MissingFields: []string{}}
	raw, err := json.Marshal(selector)
	if err != nil {
		return result, analytics.ErrInvalid
	}
	if _, err := DecodeResourceSelector(raw); err != nil {
		return result, err
	}
	if r.Graph == nil || !analytics.ValidText(binding.Subject, 512) || binding.OwnerEpoch < 1 {
		return result, analytics.ErrInvalid
	}
	scope, err := analytics.RestrictScope(binding.Grant, binding.Selection)
	if err != nil {
		return result, err
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	grantDigest := graph.ScopeDigest(binding.Grant)
	if err := analytics.CheckGrant(bounded, r.Authorization, binding.Subject, scope, grantDigest); err != nil {
		return result, err
	}
	if selector.CanonicalID == "" && (selector.Name == "" || selector.Kind == "") {
		result.Status = "clarification"
		if selector.Name == "" {
			result.MissingFields = append(result.MissingFields, "name")
		}
		if selector.Kind == "" {
			result.MissingFields = append(result.MissingFields, "kind")
		}
		return result, nil
	}
	q := graph.Query{QueryKind: "list", Scope: scope, ExpectedOwnerEpoch: binding.OwnerEpoch,
		RelationKinds: []string{}, MaxDepth: 1, MaxNodes: 10, MaxEdges: 1, Name: selector.Name, Kind: selector.Kind,
		Namespace: selector.Namespace, Sort: "canonicalId", Order: "asc"}
	if selector.CanonicalID != "" {
		id, err := resource.ParseCanonicalID(selector.CanonicalID)
		if err != nil {
			return result, analytics.ErrInvalid
		}
		if id.Tenant != scope.Tenant || id.Scope != scope.Cluster {
			return result, analytics.ErrDenied
		}
		if id.Domain != "k8s" || !analytics.SupportedKind(id.Kind) {
			return result, analytics.ErrUnsupported
		}
		if selector.Name != "" || selector.Kind != "" || selector.Namespace != "" {
			return result, analytics.ErrInvalid
		}
		q.Kind = id.Kind
		q.CanonicalIDs = []string{selector.CanonicalID}
	} else if !analytics.SupportedKind(selector.Kind) {
		return result, analytics.ErrUnsupported
	}
	if q.Namespace != "" && !scopeNamespace(scope, q.Namespace) {
		return result, analytics.ErrDenied
	}
	if analytics.ClusterScopedKind(q.Kind) {
		if q.Namespace != "" {
			return result, analytics.ErrUnsupported
		}
		if !scope.ClusterScoped {
			return result, analytics.ErrDenied
		}
	} else if len(scope.Namespaces) == 0 {
		return result, analytics.ErrDenied
	}
	if q.Namespace != "" {
		scope.Namespaces = []string{q.Namespace}
		scope.ClusterScoped = false
		q.Scope = analytics.CloneScope(scope)
	}
	out, err := r.Graph.Query(bounded, q)
	if err != nil {
		return result, err
	}
	if err := analytics.CheckGrant(bounded, r.Authorization, binding.Subject, scope, grantDigest); err != nil {
		return result, err
	}
	if err := bounded.Err(); err != nil {
		return result, err
	}
	if out.SchemaVersion != "resource-graph/v2" || out.GraphRevision.OwnerEpoch != binding.OwnerEpoch ||
		out.GraphRevision.GraphGeneration < 1 || out.CollectedAt.IsZero() {
		return result, analytics.ErrSource
	}
	result.ObservationRevision, result.SnapshotReadAt, result.Freshness = out.GraphRevision, out.CollectedAt, out.Freshness
	if !analytics.CompleteResourceList(out) {
		result.Status = "degraded"
		return result, nil
	}
	if out.Summary.Total < len(out.Nodes) || len(out.Nodes) > 10 ||
		(out.Summary.Total > len(out.Nodes) && out.NextCanonicalID == "") {
		return result, analytics.ErrSource
	}
	seen := map[string]bool{}
	for _, entity := range out.Nodes {
		id, err := resource.ParseCanonicalID(entity.CanonicalID)
		if err != nil || !scope.Allows(entity.CanonicalID, entity.Namespace) || id.Domain != "k8s" ||
			id.Kind != q.Kind || entity.Kind != id.Kind || entity.UpdatedAt.IsZero() ||
			(q.Name != "" && q.Name != entity.Name) || (q.Namespace != "" && q.Namespace != entity.Namespace) ||
			(len(q.CanonicalIDs) > 0 && q.CanonicalIDs[0] != entity.CanonicalID) || seen[entity.CanonicalID] {
			return Resolution{}, analytics.ErrSource
		}
		seen[entity.CanonicalID] = true
		result.Candidates = append(result.Candidates, Candidate{entity.CanonicalID, entity.Kind, entity.Name, entity.Namespace, entity.UpdatedAt})
	}
	result.Truncated = out.Summary.Total > len(out.Nodes)
	switch {
	case out.Summary.Total == 0:
		result.Status = "not_found"
	case out.Summary.Total == 1 && len(result.Candidates) == 1:
		result.Status = "resolved"
		result.scope, result.grantDigest, result.subject = analytics.CloneScope(scope), grantDigest, binding.Subject
		result.resolvedDigest = graph.ScopeDigest(result)
	default:
		result.Status = "clarification"
	}
	return result, nil
}

func scopeNamespace(scope graph.Scope, ns string) bool {
	for _, allowed := range scope.Namespaces {
		if allowed == ns {
			return true
		}
	}
	return false
}

// NewContext binds one resolved live identity. Selecting a clarification candidate
// requires another exact-ID Resolve so UID replacement and authority are rechecked.
func NewContext(resolution Resolution, symptom string, window TimeRange) (ResourceContext, error) {
	if resolution.Status != "resolved" || len(resolution.Candidates) != 1 || resolution.Truncated ||
		resolution.scope.Tenant == "" || resolution.subject == "" || resolution.grantDigest == "" ||
		resolution.resolvedDigest != graph.ScopeDigest(resolution) {
		return ResourceContext{}, analytics.ErrInvalid
	}
	if !utf8.ValidString(symptom) || strings.TrimSpace(symptom) == "" || utf8.RuneCountInString(symptom) > 4000 || len(symptom) > 16<<10 {
		return ResourceContext{}, analytics.ErrInvalid
	}
	if window.From.IsZero() || window.To.IsZero() || !window.From.Before(window.To) || window.To.Sub(window.From) > time.Hour {
		return ResourceContext{}, analytics.ErrInvalid
	}
	if _, err := time.LoadLocation(window.Timezone); err != nil || window.Timezone == "" || window.Timezone == "Local" {
		return ResourceContext{}, analytics.ErrInvalid
	}
	candidate := resolution.Candidates[0]
	if !resolution.scope.Allows(candidate.CanonicalID, candidate.Namespace) {
		return ResourceContext{}, analytics.ErrDenied
	}
	if _, err := uuid.Parse(resolution.scope.Tenant); err != nil {
		return ResourceContext{}, analytics.ErrInvalid
	}
	symptom = tools.RedactText(symptom)
	window.From, window.To = window.From.UTC(), window.To.UTC()
	executionScope := analytics.CloneScope(resolution.scope)
	executionScope.Resources = []string{candidate.CanonicalID}
	executionScope.Namespaces = []string{}
	executionScope.ClusterScoped = candidate.Namespace == ""
	if candidate.Namespace != "" {
		executionScope.Namespaces = []string{candidate.Namespace}
	}
	contextID, err := uuid.NewV7()
	if err != nil {
		return ResourceContext{}, err
	}
	out := ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: contextID,
		TenantID: resolution.scope.Tenant, OwnerSubject: resolution.subject, ClusterUID: resolution.scope.Cluster,
		Resources: append([]Candidate{}, candidate), Symptom: symptom, TimeRange: window,
		ResolvedAt: resolution.SnapshotReadAt, ObservationRevision: resolution.ObservationRevision,
		GrantSnapshotDigest: resolution.grantDigest, ExecutionScope: executionScope,
		ExecutionScopeDigest: graph.ScopeDigest(executionScope)}
	out.ContextDigest = graph.ScopeDigest(out)
	if err := ValidateContext(out); err != nil {
		return ResourceContext{}, err
	}
	return out, nil
}

// ValidateContext checks integrity and internal bindings, not current authority.
// It is never a substitute for Job Context signing or disclosure authorization.
func ValidateContext(value ResourceContext) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 32<<10 || contract.Validate("https://ops.local/schemas/resource-investigation-context/v1", raw) != nil {
		return analytics.ErrInvalid
	}
	digest := value.ContextDigest
	value.ContextDigest = ""
	if graph.ScopeDigest(value) != digest || graph.ScopeDigest(value.ExecutionScope) != value.ExecutionScopeDigest {
		return analytics.ErrStale
	}
	if value.ExecutionScope.Tenant != value.TenantID || value.ExecutionScope.Cluster != value.ClusterUID ||
		len(value.Resources) != 1 || len(value.ExecutionScope.Resources) != 1 {
		return analytics.ErrInvalid
	}
	candidate := value.Resources[0]
	id, err := resource.ParseCanonicalID(candidate.CanonicalID)
	if err != nil || id.Domain != "k8s" || id.Kind != candidate.Kind || !analytics.SupportedKind(id.Kind) ||
		value.ExecutionScope.Resources[0] != candidate.CanonicalID || !value.ExecutionScope.Allows(candidate.CanonicalID, candidate.Namespace) {
		return analytics.ErrDenied
	}
	if candidate.Namespace == "" {
		if !value.ExecutionScope.ClusterScoped || len(value.ExecutionScope.Namespaces) != 0 {
			return analytics.ErrInvalid
		}
	} else if value.ExecutionScope.ClusterScoped || len(value.ExecutionScope.Namespaces) != 1 || value.ExecutionScope.Namespaces[0] != candidate.Namespace {
		return analytics.ErrInvalid
	}
	if !value.TimeRange.From.Before(value.TimeRange.To) || value.TimeRange.To.Sub(value.TimeRange.From) > time.Hour ||
		value.TimeRange.Timezone == "Local" {
		return analytics.ErrInvalid
	}
	if _, err := time.LoadLocation(value.TimeRange.Timezone); err != nil {
		return analytics.ErrInvalid
	}
	return nil
}
