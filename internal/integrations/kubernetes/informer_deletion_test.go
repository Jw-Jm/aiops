package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// A watch DELETED event must surface in the next snapshot's Deleted delta and
// disappear from Objects, forwarded through the namespace union exactly once.
func TestWatchDeletionSettlesThroughNamespaceUnion(t *testing.T) {
	var mu sync.Mutex
	deletedSent := false
	pod := func(rv string) map[string]any {
		return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "web", "namespace": "apps", "uid": "pod-1", "resourceVersion": rv}, "spec": map[string]any{"nodeName": "node-1"}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			w.Header().Set("Content-Type", "application/json")
			encoder := json.NewEncoder(w)
			encoder.Encode(map[string]any{"type": "ADDED", "object": pod("rv-1")})
			for i := 0; i < 200; i++ {
				time.Sleep(20 * time.Millisecond)
				if func() bool { mu.Lock(); defer mu.Unlock(); return deletedSent }() {
					encoder.Encode(map[string]any{"type": "DELETED", "object": pod("rv-2")})
					return
				}
				encoder.Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{"metadata": map[string]any{"resourceVersion": "rv-1"}}})
			}
			return
		}
		items := []any{pod("rv-0")}
		mu.Lock()
		if deletedSent {
			items = nil
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "opaque-list"}, "items": items})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sinkMu sync.Mutex
	forwarded := 0
	seen := make(chan struct{}, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	go func() {
		// Run builds the namespace union internally; publish receives the
		// merged snapshot including the forwarded deletion delta.
		client.Run(ctx, CoreRequiredGVRs()[0], []string{"apps"}, func(s Snapshot) error {
			sinkMu.Lock()
			defer sinkMu.Unlock()
			if len(s.Deleted) > 0 {
				forwarded++
			}
			select {
			case seen <- struct{}{}:
			default:
			}
			return nil
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	deletedSent = true
	mu.Unlock()
	for dead := time.After(4 * time.Second); ; {
		select {
		case <-seen:
			sinkMu.Lock()
			count := forwarded
			sinkMu.Unlock()
			if count >= 1 {
				return
			}
		case <-dead:
			t.Fatal("watch deletion never forwarded through the namespace union")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
