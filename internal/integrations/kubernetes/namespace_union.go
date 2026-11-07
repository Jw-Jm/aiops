package kubernetes

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sync"
	"time"
)

// namespaceUnion publishes the union of one namespaced Kind collected from
// several namespaces under a single GVR identity. The Graph replaces a GVR as
// one set, so per-namespace publishers must not each publish their own subset.
// Publication is serialised because the projection sink is not reentrant.
type namespaceUnion struct {
	gvr       GVR
	order     []string
	publish   func(Snapshot) error
	mu        sync.Mutex
	objects   map[string][]unstructured.Unstructured
	states    map[string]GVRState
	published bool
	// deleted forwards per-namespace deletion deltas exactly once on the next
	// union publication.
	deleted []unstructured.Unstructured
}

func newNamespaceUnion(gvr GVR, namespaces []string, publish func(Snapshot) error) *namespaceUnion {
	return &namespaceUnion{
		gvr:     gvr,
		order:   append([]string(nil), namespaces...),
		publish: publish,
		objects: map[string][]unstructured.Unstructured{},
		states:  map[string]GVRState{},
	}
}

// sink records one namespace's latest snapshot and republishes the union. A
// namespace that never listed keeps the whole GVR unready: a partial union is
// not a complete replacement of this Kind.
func (u *namespaceUnion) sink(namespace string) func(Snapshot) error {
	return func(snapshot Snapshot) error {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.states[namespace] = snapshot.State
		if !snapshot.ObservationOnly {
			u.objects[namespace] = snapshot.Objects
			u.published = true
			u.deleted = append(u.deleted, snapshot.Deleted...)
		}
		state := u.mergedState()
		if snapshot.ObservationOnly {
			return u.publish(Snapshot{GVR: u.gvr, State: state, ObservationOnly: true})
		}
		all := []unstructured.Unstructured{}
		for _, ns := range u.order {
			all = append(all, u.objects[ns]...)
		}
		if err := u.publish(Snapshot{GVR: u.gvr, Objects: all, Deleted: u.deleted, State: state}); err != nil {
			return err
		}
		u.deleted = nil
		return u.publish(Snapshot{GVR: u.gvr, State: state, ObservationOnly: true})
	}
}

func (u *namespaceUnion) mergedState() GVRState {
	var merged GVRState
	merged.WatchConnected = true
	merged.WatchContinuous = true
	complete := true
	for _, ns := range u.order {
		state, ok := u.states[ns]
		if !ok || state.LastListCompletedAt.IsZero() {
			complete = false
			// An unlisted namespace has no connected, continuous watch.
			merged.WatchConnected = false
			merged.WatchContinuous = false
			continue
		}
		if merged.LastListCompletedAt.IsZero() || state.LastListCompletedAt.Before(merged.LastListCompletedAt) {
			merged.LastListCompletedAt = state.LastListCompletedAt
		}
		if state.LastWatchEventAt.After(merged.LastWatchEventAt) {
			merged.LastWatchEventAt = state.LastWatchEventAt
		}
		if state.LastWatchProgressAt.After(merged.LastWatchProgressAt) {
			merged.LastWatchProgressAt = state.LastWatchProgressAt
		}
		if merged.LastConnectivityProbeAt.IsZero() || state.LastConnectivityProbeAt.Before(merged.LastConnectivityProbeAt) {
			merged.LastConnectivityProbeAt = state.LastConnectivityProbeAt
		}
		if state.ProjectionQueueLag > merged.ProjectionQueueLag {
			merged.ProjectionQueueLag = state.ProjectionQueueLag
		}
		if !state.WatchConnected {
			merged.WatchConnected = false
		}
		if !state.WatchContinuous {
			merged.WatchContinuous = false
		}
		if merged.LastError == "" && state.LastError != "" {
			merged.LastError = state.LastError
		}
	}
	if !complete {
		// Any unlisted namespace means this set is not a complete replacement.
		merged.LastListCompletedAt = time.Time{}
	}
	return merged
}
