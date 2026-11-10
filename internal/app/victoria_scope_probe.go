package app

import (
	"context"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"slices"
)

// Graph identities only nominate canaries. VerifyVictoria still requires a real
// nonempty current source response and every negative isolation check.
func victoriaProbeCandidates(probe evidence.Query, binding evidence.Binding, nodes []resource.Entity) []evidence.Query {
	out := []evidence.Query{}
	if !slices.Contains(binding.ScopeMapping.Scopes["namespace"], probe.Namespace) || len(binding.ScopeMapping.Scopes["cluster"]) != 1 {
		return out
	}
	seen := map[string]bool{}
	for _, node := range nodes {
		id, err := resource.ParseCanonicalID(node.CanonicalID)
		if err != nil || node.Kind != "Pod" || node.Namespace != probe.Namespace || id.Kind != "Pod" || id.Domain != "k8s" || id.Tenant != binding.Tenant || id.Scope != binding.ScopeMapping.Scopes["cluster"][0] || seen[node.CanonicalID] {
			continue
		}
		candidate := probe
		candidate.ResourceCanonicalID = node.CanonicalID
		out = append(out, candidate)
		seen[node.CanonicalID] = true
		if len(out) == 8 {
			break
		}
	}
	return out
}

func verifyCurrentVictoriaProbe(ctx context.Context, binding evidence.Binding, probe evidence.Query, owner *graph.Graph, verify func(context.Context, evidence.Query) error) error {
	if owner == nil {
		return evidence.ErrScopeUnverified
	}
	scope := probe.Scope
	scope.Namespaces = []string{probe.Namespace}
	scope.ClusterScoped = false
	scope.Resources = nil
	result, err := owner.Query(ctx, graph.Query{QueryKind: "list", Kind: "Pod", Namespace: probe.Namespace, Scope: scope, ExpectedOwnerEpoch: owner.OwnerEpoch(), MaxDepth: 1, MaxNodes: 50, MaxEdges: 1, Sort: "updatedAt", Order: "desc"})
	if err != nil {
		return err
	}
	for _, candidate := range victoriaProbeCandidates(probe, binding, result.Nodes) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = verify(ctx, candidate); err == nil {
			return nil
		}
	}
	return evidence.ErrScopeUnverified
}
