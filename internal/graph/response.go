package graph

import (
	"context"
	"slices"
	"time"
)

// RevalidateResponse fences the immutable query snapshot after authorization
// and native Lease checks, immediately before serializing its response. A
// collection recovery may downgrade freshness without replacing published data;
// a changed generation/epoch requires a new query rather than mixing snapshots.
func (g *Graph) RevalidateResponse(out *Result) error {
	if out == nil {
		return ErrNotReady
	}
	g.expireExternal(g.now())
	g.mu.RLock()
	defer g.mu.RUnlock()
	now := g.now()
	if g.current == nil || !now.Before(g.deadline) {
		return ErrNotReady
	}
	if out.OwnerInstance != g.instance || out.GraphRevision.OwnerEpoch != g.epoch || out.GraphRevision.GraphGeneration != g.current.revision.GraphGeneration {
		return ErrStale
	}
	if !g.ready(now) {
		out.Freshness = "stale"
		out.Partial = true
		if !slices.Contains(out.Warnings, "collection_recovering") {
			out.Warnings = append(out.Warnings, "collection_recovering")
		}
		for _, gvr := range g.required {
			state := g.states[gvr.Key()]
			if state.LastListCompletedAt.IsZero() || !state.WatchContinuous || !state.WatchConnected || state.LastError != "" || state.LastConnectivityProbeAt.IsZero() || now.Sub(state.LastConnectivityProbeAt) > 45*time.Second || state.ProjectionQueueLag > 60*time.Second {
				if !slices.Contains(out.DegradedSources, gvr.Key()) {
					out.DegradedSources = append(out.DegradedSources, gvr.Key())
				}
			}
		}
	}
	return nil
}

// WithCurrentResponse holds the immutable local graph revision across a
// confirmed database commit. Collection/revocation cannot change its readiness
// or source state between final validation and publication. The database call
// is bounded by both the caller's budget and the remaining owner lifetime.
func (g *Graph) WithCurrentResponse(ctx context.Context, out Result, commit func(context.Context) error) error {
	if commit == nil {
		return ErrNotReady
	}
	g.expireExternal(g.now())
	g.mu.RLock()
	defer g.mu.RUnlock()
	now := g.now()
	if g.current == nil || !now.Before(g.deadline) {
		return ErrNotReady
	}
	if out.OwnerInstance != g.instance || out.GraphRevision.OwnerEpoch != g.epoch || out.GraphRevision.GraphGeneration != g.current.revision.GraphGeneration || !g.ready(now) || len(g.degraded) > 0 || out.Partial || out.Freshness != "fresh" || len(out.DegradedSources) > 0 {
		return ErrStale
	}
	bounded, cancel := context.WithTimeout(ctx, g.deadline.Sub(now))
	defer cancel()
	return commit(bounded)
}
