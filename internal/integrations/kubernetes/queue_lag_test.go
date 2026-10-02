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

func TestSlowProjectionPreservesWatchReceiveTimeAndMeasuresQueueLag(t *testing.T) {
	var watches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"1"},"items":[]}`))
			return
		}
		if watches.Add(1) > 1 {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		enc := json.NewEncoder(w)
		enc.Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"resourceVersion": "1", "annotations": map[string]any{"k8s.io/initial-events-end": "true"}}}})
		for _, rv := range []string{"2", "3"} {
			enc.Encode(map[string]any{"type": "MODIFIED", "object": map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"namespace": "apps", "name": "web", "uid": "pod-a", "resourceVersion": rv}}})
		}
		enc.Encode(map[string]any{"type": "ERROR", "object": map[string]any{"code": 410}})
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	c, _ := NewClient(server.URL, server.Client(), 20, 50)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	measured := false
	completedPublication := false
	recovered := false
	c.Run(ctx, GVR{Version: "v1", Resource: "pods"}, func(s Snapshot) error {
		t.Logf("snapshot objects=%d error=%s continuous=%t", len(s.Objects), s.State.LastError, s.State.WatchContinuous)
		if s.ObservationOnly && s.State.ProjectionQueueLag >= 100*time.Millisecond {
			completedPublication = true
		}
		if measured && s.State.LastError == "watch_initializing" {
			if s.State.ProjectionQueueLag != 0 {
				t.Errorf("authoritative recovery retained old queue lag: %s", s.State.ProjectionQueueLag)
			}
			recovered = true
			cancel()
		}
		if len(s.Objects) == 0 {
			return nil
		}
		switch s.Objects[0].GetResourceVersion() {
		case "2":
			time.Sleep(120 * time.Millisecond)
		case "3":
			if s.State.ProjectionQueueLag < 100*time.Millisecond {
				t.Errorf("slow sink lag ignored: %s", s.State.ProjectionQueueLag)
			}
			observed, err := time.Parse(time.RFC3339Nano, s.Objects[0].GetAnnotations()["ops.internal/observed-at"])
			if err != nil || time.Since(observed) < 100*time.Millisecond {
				t.Errorf("source receive time replaced with processing time: %s %v", time.Since(observed), err)
			}
			if !s.State.LastWatchEventAt.Equal(observed) {
				t.Error("watch event timestamp differs from source receive timestamp")
			}
			measured = true
		}
		return nil
	})
	if !measured || !completedPublication || !recovered {
		t.Fatalf("watch lag/recovery missing: queued=%t published=%t recovered=%t", measured, completedPublication, recovered)
	}
}
