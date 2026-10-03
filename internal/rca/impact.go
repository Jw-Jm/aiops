package rca

import (
	"context"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"slices"
	"time"
)

// QueryImpact executes a reviewed Recipe plan against the SP04 kernel. It
// neither discovers arbitrary adjacency nor walks a graph. The DIMM plan only
// selects the fixed component_of -> hosts shape already returned by the kernel,
// then asks that same kernel for each hosted Node's one-hop impact.
func QueryImpact(ctx context.Context, g *graph.Graph, r Recipe, q graph.Query) (graph.Result, error) {
	if g == nil {
		return graph.Result{}, graph.ErrNotReady
	}
	baseline, err := BuiltinVersion(r.Name, r.Version)
	if err != nil || !sameJSON(r, baseline) {
		return graph.Result{}, ErrRecipe
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(r.Budget.TimeoutMs)*time.Millisecond)
	defer cancel()
	q.QueryKind, q.MaxDepth, q.MaxNodes, q.MaxEdges = "impact", r.GraphDepth, r.Budget.MaxNodes, r.Budget.MaxEdges
	q.ExpectedOwnerEpoch = g.OwnerEpoch()
	out, err := g.Query(bounded, q)
	if err != nil || r.GraphPlan == "single-query/v1" || out.Partial || out.Freshness != "fresh" {
		return out, err
	}
	kinds := map[string]string{}
	for _, n := range out.Nodes {
		kinds[n.CanonicalID] = n.Kind
	}
	nodes := []string{}
	for _, component := range out.Edges {
		if component.Kind != "component_of" || component.From != q.CanonicalID || kinds[component.To] != "PhysicalServer" {
			continue
		}
		for _, host := range out.Edges {
			if host.Kind == "hosts" && host.From == component.To && kinds[host.To] == "Node" {
				nodes = append(nodes, host.To)
			}
		}
	}
	slices.Sort(nodes)
	nodes = slices.Compact(nodes)
	for _, node := range nodes {
		leftNodes, leftEdges := r.Budget.MaxNodes-len(out.Nodes), r.Budget.MaxEdges-len(out.Edges)
		if leftNodes < 1 || leftEdges < 1 {
			out.Partial, out.Budgets.Truncated = true, true
			out.Budgets.TruncationReasons = append(out.Budgets.TruncationReasons, "recipe_shared_budget")
			break
		}
		stage := q
		stage.CanonicalID, stage.MaxDepth, stage.MaxNodes, stage.MaxEdges = node, 1, leftNodes+1, leftEdges
		stage.CursorRevision = &out.GraphRevision
		next, err := g.Query(bounded, stage)
		if err != nil {
			return graph.Result{}, err
		}
		for _, n := range next.Nodes {
			if _, exists := kinds[n.CanonicalID]; !exists {
				kinds[n.CanonicalID] = n.Kind
				out.Nodes = append(out.Nodes, n)
			}
		}
		for _, e := range next.Edges {
			if !slices.ContainsFunc(out.Edges, func(existing resource.Relation) bool {
				return existing.From == e.From && existing.To == e.To && existing.Kind == e.Kind
			}) {
				out.Edges = append(out.Edges, e)
			}
		}
		out.IndirectlyAffected = append(out.IndirectlyAffected, next.DirectlyAffected...)
		out.Partial = out.Partial || next.Partial
		if next.Freshness != "fresh" {
			out.Freshness = next.Freshness
		}
		out.DegradedSources = append(out.DegradedSources, next.DegradedSources...)
		out.Warnings = append(out.Warnings, next.Warnings...)
		out.Budgets.Truncated = out.Budgets.Truncated || next.Budgets.Truncated
		out.Budgets.TruncationReasons = append(out.Budgets.TruncationReasons, next.Budgets.TruncationReasons...)
	}
	slices.Sort(out.IndirectlyAffected)
	out.IndirectlyAffected = slices.Compact(out.IndirectlyAffected)
	out.Budgets.NodeCount, out.Budgets.EdgeCount = len(out.Nodes), len(out.Edges)
	return out, nil
}
