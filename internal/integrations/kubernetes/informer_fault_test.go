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

func TestStreamingInitialSnapshotAndTerminalDelete(t *testing.T) {
	obj := func(uid, rv string) map[string]any {
		return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"uid": uid, "name": "web", "namespace": "apps", "resourceVersion": rv}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := json.NewEncoder(w)
		if r.URL.Query().Get("watch") != "true" {
			enc.Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "opaque-list"}, "items": []any{obj("deleted-between-list-watch", "old")}})
			return
		}
		for _, e := range []map[string]any{
			{"type": "ADDED", "object": obj("old-uid", "rv1")},
			{"type": "BOOKMARK", "object": map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"resourceVersion": "bookmark", "annotations": map[string]any{"k8s.io/initial-events-end": "true"}}}},
			{"type": "MODIFIED", "object": obj("old-uid", "rv1")},
			{"type": "DELETED", "object": obj("old-uid", "rv1")},
			{"type": "ADDED", "object": obj("old-uid", "late")},
			{"type": "ADDED", "object": obj("new-uid", "rv2")},
		} {
			enc.Encode(e)
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, server.Client(), 20, 50)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	deleted, rebuilt := false, false
	if err := client.Run(ctx, GVR{Version: "v1", Resource: "pods"}, func(s Snapshot) error {
		if s.ObservationOnly || !s.State.WatchContinuous {
			return nil
		}
		for _, o := range s.Objects {
			if o.GetUID() == "deleted-between-list-watch" {
				t.Fatal("old List published fresh after initial Watch")
			}
			if deleted && o.GetUID() == "old-uid" {
				t.Fatal("terminal UID resurrected")
			}
			if o.GetUID() == "new-uid" {
				rebuilt = true
			}
		}
		if len(s.Objects) == 0 {
			deleted = true
		}
		return nil
	}); err != nil && err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	if !deleted || !rebuilt {
		t.Fatalf("delete/recreate incomplete deleted=%t recreated=%t", deleted, rebuilt)
	}
}

func TestPermissionAndThrottleRemainStaleAndShareBudget(t *testing.T) {
	for _, status := range []int{403, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(status)
			}))
			defer server.Close()
			a, _ := NewClient(server.URL, server.Client(), 20, 50)
			b, _ := NewClient(server.URL, server.Client(), 20, 50)
			b.ShareRateBudget(a.budget)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			seen := false
			a.Run(ctx, GVR{Version: "v1", Resource: "pods"}, func(s Snapshot) error {
				if s.State.WatchContinuous || s.State.WatchConnected {
					t.Fatal("unavailable API fresh")
				}
				if s.State.LastError == "list_unavailable" {
					seen = true
				}
				return nil
			})
			if !seen || calls.Load() != 1 {
				t.Fatalf("unavailable state=%t calls=%d", seen, calls.Load())
			}
			if status == 429 {
				short, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer stop()
				if _, err := b.Do(short, "GET", "/api/v1/pods", nil); err == nil {
					t.Fatal("shared Retry-After bypassed")
				}
				if calls.Load() != 1 {
					t.Fatal("throttled request reached backend")
				}
			}
		})
	}
}

func TestSilentWatchAuthorizationProbeFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			json.NewEncoder(w).Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"resourceVersion": "initial", "annotations": map[string]string{"k8s.io/initial-events-end": "true"}}}})
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if r.URL.Query().Get("limit") == "1" {
			w.WriteHeader(403)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]string{"resourceVersion": "list"}, "items": []any{}})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, server.Client(), 20, 50)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	fresh, stale := false, false
	client.Run(ctx, GVR{Version: "v1", Resource: "pods"}, func(s Snapshot) error {
		if s.State.WatchContinuous {
			fresh = true
		}
		if fresh && !s.State.WatchContinuous && s.State.LastError == "connectivity_unverified" {
			stale = true
			cancel()
		}
		return nil
	})
	if !fresh || !stale {
		t.Fatalf("silent watch authorization unverified: initial=%t stale=%t", fresh, stale)
	}
}
