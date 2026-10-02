package graph

import (
	"ops-platform/internal/integrations/kubernetes"
	"time"
)

func (g *Graph) availabilityFailure(epoch int64) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.current == nil {
		return "snapshot_missing"
	}
	if g.epoch != epoch {
		return "owner_epoch_changed"
	}
	if !time.Now().Before(g.deadline) {
		return "owner_deadline_expired"
	}
	return "owner_state_changed"
}

// CollectionHealth exposes only bounded GVR states, never source object data.
func (g *Graph) CollectionHealth() map[string]kubernetes.GVRState {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := map[string]kubernetes.GVRState{}
	for _, gvr := range g.required {
		out[gvr.Key()] = g.states[gvr.Key()]
	}
	return out
}
func (g *Graph) QualificationFailures(now time.Time) map[string]string {
	out := map[string]string{}
	for gvr, state := range g.CollectionHealth() {
		switch {
		case state.LastListCompletedAt.IsZero():
			out[gvr] = "list_missing"
		case state.LastError != "":
			out[gvr] = state.LastError
		case !state.WatchConnected:
			out[gvr] = "watch_disconnected"
		case !state.WatchContinuous:
			out[gvr] = "watch_initializing"
		case state.ProjectionQueueLag > 60*time.Second:
			out[gvr] = "projection_backlog"
		case state.LastConnectivityProbeAt.IsZero() || now.Sub(state.LastConnectivityProbeAt) > 45*time.Second:
			out[gvr] = "probe_expired"
		}
	}
	return out
}
