package app

import (
	"context"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/policy"
)

type investigationPolicyEvaluator interface {
	Evaluate(context.Context, policy.PolicyInput) (policy.PolicyDecision, error)
}

// A namespace grant authorizes namespace reads, never a cluster-wide request.
// Every selected namespace must admit the same policy frozen by the Job.
func evaluateInvestigationPolicy(ctx context.Context, evaluator investigationPolicyEvaluator, grant graph.Scope, input policy.PolicyInput) (policy.PolicyDecision, error) {
	scopes := []configregistry.Scope{input.Scope}
	if input.Scope.Type == configregistry.ScopeCluster && !grant.ClusterScoped {
		if len(grant.Namespaces) == 0 {
			return policy.PolicyDecision{}, investigation.ErrDenied
		}
		scopes = nil
		for _, namespace := range grant.Namespaces {
			scopes = append(scopes, configregistry.Scope{Type: configregistry.ScopeNamespace, ClusterID: input.Scope.ClusterID, Namespace: namespace})
		}
	}
	var admitted policy.PolicyDecision
	for _, scope := range scopes {
		input.Scope = scope
		decision, err := evaluator.Evaluate(ctx, input)
		if err != nil || !decision.Allow || (admitted.PolicyVersion != "" && decision.PolicyVersion != admitted.PolicyVersion) {
			return decision, investigation.ErrDenied
		}
		admitted = decision
	}
	return admitted, nil
}
