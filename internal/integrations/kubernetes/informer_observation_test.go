package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Progress bookmarks and a reconnect to the same authoritative snapshot must
// update health without repersisting unchanged facts or advancing Graph data.
func TestWatchHealthAndUnchangedResyncDoNotRepublishFacts(t *testing.T) {
	object := func(rv string) map[string]any {
		return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"uid": "pod-1", "name": "web", "namespace": "apps", "resourceVersion": rv}}
	}
	bookmark := func(initial bool) map[string]any {
		m := map[string]any{"resourceVersion": "opaque-bookmark"}
		if initial {
			m["annotations"] = map[string]string{"k8s.io/initial-events-end": "true"}
		}
		return map[string]any{"type": "BOOKMARK", "object": map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": m}}
	}
	var watches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoder := json.NewEncoder(w)
		if r.URL.Query().Get("watch") != "true" {
			_ = encoder.Encode(map[string]any{"metadata": map[string]string{"resourceVersion": "opaque-list"}, "items": []any{object("rv1")}})
			return
		}
		n := watches.Add(1)
		for _, event := range []any{map[string]any{"type": "ADDED", "object": object("rv1")}, bookmark(true), bookmark(false)} {
			_ = encoder.Encode(event)
			w.(http.Flusher).Flush()
		}
		if n == 1 {
			return
		}
		_ = encoder.Encode(map[string]any{"type": "MODIFIED", "object": object("rv2")})
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, server.Client(), 20, 50)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	seen := map[string]int{}
	observations := 0
	fresh := false
	disconnected := false
	_ = client.Run(ctx, GVR{Version: "v1", Resource: "pods"}, func(s Snapshot) error {
		if s.State.WatchContinuous {
			fresh = true
		}
		if fresh && !s.State.WatchConnected {
			disconnected = true
		}
		if s.ObservationOnly {
			observations++
			if len(s.Objects) != 0 {
				t.Error("health publication contains fact objects")
			}
			return nil
		}
		for _, o := range s.Objects {
			rv := o.GetResourceVersion()
			seen[rv]++
			if seen[rv] > 1 {
				t.Errorf("unchanged %s republished as new data", rv)
				cancel()
			}
			if rv == "rv2" {
				cancel()
			}
		}
		return nil
	})
	if seen["rv1"] != 1 || seen["rv2"] != 1 || observations < 3 || !fresh || !disconnected {
		t.Fatalf("data/health/resync boundary: versions=%v health=%d fresh=%t disconnected=%t", seen, observations, fresh, disconnected)
	}
}
