package graph

import (
	"context"
	"encoding/json"
	"ops-platform/internal/upstream/ontology/api"
	"ops-platform/internal/upstream/ontology/service/diagnostic"
	"slices"
)

// Impact classification projects separate bounded upstream expansions. It does
// not walk edges or maintain adjacency; the locked diagnostic service owns all
// traversal and scope filtering remains in its reader.
func impactProjection(ctx context.Context, reader *ontologyReader, entry api.EntryRef, policy api.ExpansionPolicy, result *api.DiagnosticSubgraph) ([]string, []string, []string, error) {
	directReader := *reader
	directPolicy := policy
	directPolicy.MaxDepth = 1
	directPolicy.StorageMaxDepth = 1
	direct, err := diagnostic.NewService(&directReader).GetDiagnosticSubgraphContext(ctx, entry, directPolicy)
	if err != nil {
		return nil, nil, nil, err
	}
	directIDs := []string{}
	indirectIDs := []string{}
	dependencyIDs := []string{}
	impacted := map[string]bool{entry.CanonicalID: true}
	for _, n := range direct.Nodes {
		if n.CanonicalID != entry.CanonicalID {
			directIDs = append(directIDs, n.CanonicalID)
		}
	}
	for _, n := range result.Nodes {
		impacted[n.CanonicalID] = true
		if n.CanonicalID != entry.CanonicalID && !slices.Contains(directIDs, n.CanonicalID) {
			indirectIDs = append(indirectIDs, n.CanonicalID)
		}
	}
	dependencyReader := *reader
	dependencyReader.queryKind = "dependencies"
	remainingNodes := policy.MaxNodes - len(result.Nodes)
	remainingEdges := policy.MaxEdges - len(result.Edges)
	dependencyPolicy := policy
	dependencyPolicy.MaxNodes = remainingNodes + 1
	// With no edge budget remaining, do not silently overrun the global cap.
	if remainingEdges <= 0 {
		result.Partial = true
		result.Budgets.Truncated = true
		result.Budgets.TruncationReasons = append(result.Budgets.TruncationReasons, "impact_dependency_budget")
		return directIDs, indirectIDs, dependencyIDs, nil
	}
	dependencyPolicy.MaxEdges = remainingEdges
	dependencies, err := diagnostic.NewService(&dependencyReader).GetDiagnosticSubgraphContext(ctx, entry, dependencyPolicy)
	if err != nil {
		return nil, nil, nil, err
	}
	visible := map[string]bool{}
	for id := range impacted {
		visible[id] = true
	}
	for _, n := range dependencies.Nodes {
		if impacted[n.CanonicalID] {
			continue
		}
		dependencyIDs = append(dependencyIDs, n.CanonicalID)
		result.Nodes = append(result.Nodes, n)
		visible[n.CanonicalID] = true
	}
	for _, e := range dependencies.Edges {
		if !visible[e.From] || !visible[e.To] {
			continue
		}
		duplicate := false
		for _, existing := range result.Edges {
			if existing.From == e.From && existing.To == e.To && existing.Kind == e.Kind {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result.Edges = append(result.Edges, e)
		}
	}
	result.Partial = result.Partial || dependencies.Partial
	result.Budgets.Truncated = result.Budgets.Truncated || dependencies.Budgets.Truncated
	result.Budgets.TruncationReasons = append(result.Budgets.TruncationReasons, dependencies.Budgets.TruncationReasons...)
	result.Budgets.NodeCount = len(result.Nodes)
	result.Budgets.EdgeCount = len(result.Edges)
	reader.scopeLimited = reader.scopeLimited || directReader.scopeLimited || dependencyReader.scopeLimited
	slices.Sort(directIDs)
	slices.Sort(indirectIDs)
	slices.Sort(dependencyIDs)
	return directIDs, indirectIDs, dependencyIDs, nil
}

// Empty classification is explicit, including when an expansion is bounded.
func (r Result) MarshalJSON() ([]byte, error) {
	type wire Result
	if r.DirectlyAffected == nil {
		r.DirectlyAffected = []string{}
	}
	if r.IndirectlyAffected == nil {
		r.IndirectlyAffected = []string{}
	}
	if r.DependencyOnly == nil {
		r.DependencyOnly = []string{}
	}
	return json.Marshal(wire(r))
}
