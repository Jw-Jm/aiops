package deepflow

import (
	"context"
	"ops-platform/internal/evidence"
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
func (a *Adapter) GetL7Context(context.Context, evidence.Query) (evidence.Result, error) {
	return evidence.Result{}, ErrDisabled
}
func (a *Adapter) GetTraceContext(context.Context, evidence.Query) (evidence.Result, error) {
	return evidence.Result{}, ErrDisabled
}
