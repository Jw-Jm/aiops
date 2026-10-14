package deepflow

import (
	"context"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"slices"
)

func (a *Adapter) GetResourceNetworkHealth(c context.Context, q evidence.Query) (evidence.Result, error) {
	q.Template = "GetResourceNetworkHealth"
	return a.Query(c, q)
}
func (a *Adapter) GetNetworkDependencies(c context.Context, q evidence.Query) (evidence.Result, error) {
	q.Template = "GetNetworkDependencies"
	return a.Query(c, q)
}
func (a *Adapter) GetNetworkPath(c context.Context, q evidence.Query) (evidence.Result, error) {
	q.Template = "GetNetworkPath"
	return a.Query(c, q)
}
func (a *Adapter) FindTCPRetransmission(c context.Context, q evidence.Query) (evidence.Result, error) {
	q.Template = "FindTCPRetransmission"
	return a.Query(c, q)
}
func (a *Adapter) FindPacketLoss(c context.Context, q evidence.Query) (evidence.Result, error) {
	q.Template = "FindPacketLoss"
	return a.Query(c, q)
}
func (a *Adapter) FindConnectionFailure(c context.Context, q evidence.Query) (evidence.Result, error) {
	q.Template = "FindConnectionFailure"
	return a.Query(c, q)
}
func (a *Adapter) GetL7Context(c context.Context, q evidence.Query) (evidence.Result, error) {
	q.Template = "GetL7Context"
	return a.Query(c, q)
}
func (a *Adapter) GetTraceContext(context.Context, evidence.Query) (evidence.Result, error) {
	return evidence.Result{}, ErrDisabled
}

func (a *Adapter) ScopedCapabilities(ctx context.Context, scope graph.Scope) (evidence.CapabilitySet, error) {
	if scope.Tenant != a.binding.Tenant || !slices.Contains(a.binding.ScopeMapping.Scopes["cluster"], scope.Cluster) || len(scope.Namespaces) == 0 {
		return nil, evidence.ErrScopeUnverified
	}
	for _, ns := range scope.Namespaces {
		if !slices.Contains(a.binding.ScopeMapping.Scopes["namespace"], ns) {
			return nil, evidence.ErrScopeUnverified
		}
	}
	return a.Capabilities(ctx)
}
