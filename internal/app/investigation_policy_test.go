package app

import (
	"context"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/graph"
	"ops-platform/internal/policy"
	"testing"
)

type scopePolicyProbe struct {
	seen      []configregistry.Scope
	reject    string
	different bool
}

func (p *scopePolicyProbe) Evaluate(_ context.Context, in policy.PolicyInput) (policy.PolicyDecision, error) {
	p.seen = append(p.seen, in.Scope)
	version := "frozen"
	if p.different && in.Scope.Namespace == "second" {
		version = "other"
	}
	return policy.PolicyDecision{Allow: in.Scope.Namespace != p.reject, PolicyVersion: version}, nil
}
func TestInvestigationPolicyChecksAllSelectedNamespacesWithoutClusterEscalation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		grant     graph.Scope
		reject    string
		different bool
		want      bool
		calls     int
	}{
		{"namespace", graph.Scope{Namespaces: []string{"apps"}}, "", false, true, 1},
		{"multiple", graph.Scope{Namespaces: []string{"apps", "second"}}, "", false, true, 2},
		{"outside denied", graph.Scope{Namespaces: []string{"apps", "outside"}}, "outside", false, false, 2},
		{"different frozen policy", graph.Scope{Namespaces: []string{"apps", "second"}}, "", true, false, 2},
		{"empty denied", graph.Scope{}, "", false, false, 0},
		{"cluster unchanged", graph.Scope{ClusterScoped: true}, "unused", false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &scopePolicyProbe{reject: tc.reject, different: tc.different}
			_, err := evaluateInvestigationPolicy(t.Context(), p, tc.grant, policy.PolicyInput{Scope: configregistry.Scope{Type: configregistry.ScopeCluster}})
			if (err == nil) != tc.want || len(p.seen) != tc.calls {
				t.Fatalf("err=%v scopes=%v", err, p.seen)
			}
			if !tc.grant.ClusterScoped {
				for _, s := range p.seen {
					if s.Type != configregistry.ScopeNamespace {
						t.Fatal("escalated to cluster")
					}
				}
			}
		})
	}
}
