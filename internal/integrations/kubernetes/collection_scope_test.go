package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRequiredCollectionResourcesHaveExplicitNativeScope(t *testing.T) {
	namespaced := 0
	for _, gvr := range CoreRequiredGVRs() {
		if !gvr.Collected() {
			t.Fatalf("required resource %s has no explicit native scope", gvr.Key())
		}
		if gvr.Namespaced() {
			namespaced++
		}
	}
	if len(CoreRequiredGVRs()) != 17 || namespaced != 11 {
		t.Fatalf("unexpected scope split: gvr=%d namespaced=%d", len(CoreRequiredGVRs()), namespaced)
	}
	for _, gvr := range []GVR{
		{"", "v1", "pods", "Pod"}, {"", "v1", "services", "Service"}, {"", "v1", "configmaps", "ConfigMap"},
		{"", "v1", "persistentvolumeclaims", "PersistentVolumeClaim"}, {"", "v1", "events", "Event"},
		{"apps", "v1", "deployments", "Deployment"}, {"apps", "v1", "replicasets", "ReplicaSet"},
		{"apps", "v1", "statefulsets", "StatefulSet"}, {"apps", "v1", "daemonsets", "DaemonSet"},
		{"batch", "v1", "jobs", "Job"}, {"discovery.k8s.io", "v1", "endpointslices", "EndpointSlice"},
	} {
		if !gvr.Namespaced() {
			t.Fatalf("%s must be collected inside a namespace", gvr.Key())
		}
	}
	for _, gvr := range []GVR{
		{"", "v1", "nodes", "Node"}, {"", "v1", "persistentvolumes", "PersistentVolume"},
		{"storage.k8s.io", "v1", "storageclasses", "StorageClass"}, {"storage.k8s.io", "v1", "csidrivers", "CSIDriver"},
		{"storage.k8s.io", "v1", "csinodes", "CSINode"}, {"storage.k8s.io", "v1", "volumeattachments", "VolumeAttachment"},
	} {
		if gvr.Namespaced() {
			t.Fatalf("%s is cluster-scoped", gvr.Key())
		}
	}
	if got := (GVR{Version: "v1", Resource: "pods"}).PathIn("ops-a"); got != "/api/v1/namespaces/ops-a/pods" {
		t.Fatalf("core namespaced path = %s", got)
	}
	if got := (GVR{Group: "apps", Version: "v1", Resource: "deployments"}).PathIn("ops-a"); got != "/apis/apps/v1/namespaces/ops-a/deployments" {
		t.Fatalf("group namespaced path = %s", got)
	}
}

func TestRunRejectsAmbiguousCollectionScope(t *testing.T) {
	client, err := NewClient("https://127.0.0.1:1", &http.Client{Timeout: time.Second}, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	sink := func(Snapshot) error { return nil }
	cases := map[string]struct {
		gvr        GVR
		namespaces []string
	}{
		"namespaced-without-scope": {GVR{Version: "v1", Resource: "pods"}, nil},
		"cluster-with-scope":       {GVR{Version: "v1", Resource: "nodes"}, []string{"ops-a"}},
		"unclassified-resource":    {GVR{Group: "example.com", Version: "v1", Resource: "widgets"}, []string{"ops-a"}},
		"duplicate-scope":          {GVR{Version: "v1", Resource: "pods"}, []string{"ops-a", "ops-a"}},
		"empty-scope":              {GVR{Version: "v1", Resource: "pods"}, []string{""}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			if err := client.Run(ctx, tc.gvr, tc.namespaces, sink); err == nil {
				t.Fatal("ambiguous collection scope accepted")
			}
		})
	}
}

func TestNamespacedCollectionUsesNamespacedEndpointsOnly(t *testing.T) {
	var mu sync.Mutex
	paths := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths[r.URL.Path]++
		mu.Unlock()
		if !strings.Contains(r.URL.Path, "/namespaces/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		namespace := "ops-a"
		if strings.Contains(r.URL.Path, "/namespaces/ops-b/") {
			namespace = "ops-b"
		}
		if r.URL.Query().Get("watch") == "true" {
			encoder := json.NewEncoder(w)
			encoder.Encode(map[string]any{"type": "ADDED", "object": map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"uid": "pod-" + namespace, "name": "web-" + namespace, "namespace": namespace, "resourceVersion": "rv-1"}}})
			encoder.Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"resourceVersion": "rv-2", "annotations": map[string]any{"k8s.io/initial-events-end": "true"}}}})
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "opaque"}, "items": []any{map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"uid": "pod-" + namespace, "name": "web-" + namespace, "namespace": namespace, "resourceVersion": "rv-0"}}}})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	union := map[string]bool{}
	err = client.Run(ctx, GVR{Version: "v1", Resource: "pods"}, []string{"ops-a", "ops-b"}, func(s Snapshot) error {
		if s.ObservationOnly || len(s.Objects) == 0 {
			return nil
		}
		for _, o := range s.Objects {
			union[o.GetNamespace()] = true
		}
		if len(union) == 2 && !s.State.LastListCompletedAt.IsZero() && s.State.WatchContinuous {
			cancel()
		}
		return nil
	})
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	if !union["ops-a"] || !union["ops-b"] {
		t.Fatalf("namespace union incomplete: %v", union)
	}
	mu.Lock()
	defer mu.Unlock()
	for path := range paths {
		if !strings.Contains(path, "/namespaces/") {
			t.Fatalf("cluster-wide request issued for a namespaced Kind: %s", path)
		}
	}
}

func TestNamespaceUnionStaysUnreadyUntilEveryNamespaceListed(t *testing.T) {
	published := []Snapshot{}
	union := newNamespaceUnion(GVR{Version: "v1", Resource: "pods"}, []string{"ops-a", "ops-b"}, func(s Snapshot) error {
		published = append(published, s)
		return nil
	})
	listed := time.Now()
	if err := union.sink("ops-a")(Snapshot{GVR: union.gvr, State: GVRState{LastListCompletedAt: listed, WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: listed}}); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 || !published[1].State.LastListCompletedAt.IsZero() {
		t.Fatalf("partial namespace union published a complete list: %#v", published)
	}
	if published[1].State.WatchContinuous || published[1].State.WatchConnected {
		t.Fatal("partial namespace union reported a continuous watch")
	}
	published = published[:0]
	if err := union.sink("ops-b")(Snapshot{GVR: union.gvr, State: GVRState{LastListCompletedAt: listed, WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: listed}}); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 || published[1].State.LastListCompletedAt.IsZero() {
		t.Fatalf("complete namespace union did not publish readiness: %#v", published)
	}
}

func TestClusterWatchFiltersListAndWatchBeforeProjection(t *testing.T) {
	pod := func(namespace string) map[string]any {
		return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{
			"namespace": namespace, "name": "pod", "uid": namespace, "resourceVersion": "1",
		}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pods" {
			http.NotFound(w, r)
			return
		}
		enc := json.NewEncoder(w)
		if r.URL.Query().Get("watch") != "true" {
			_ = enc.Encode(map[string]any{"metadata": map[string]string{"resourceVersion": "1"}, "items": []any{pod("allowed"), pod("foreign")}})
			return
		}
		for _, ns := range []string{"allowed", "foreign"} {
			_ = enc.Encode(map[string]any{"type": "ADDED", "object": pod(ns)})
		}
		_ = enc.Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{
			"resourceVersion": "2", "annotations": map[string]string{"k8s.io/initial-events-end": "true"},
		}}})
		_ = enc.Encode(map[string]any{"type": "MODIFIED", "object": pod("foreign")})
		_ = enc.Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]string{"resourceVersion": "3"}}})
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	listed, watched := false, false
	_ = client.RunClusterWatch(ctx, GVR{Version: "v1", Resource: "pods"}, []string{"allowed"}, func(s Snapshot) error {
		if !s.ObservationOnly {
			listed = true
			if len(s.Objects) != 1 || s.Objects[0].GetNamespace() != "allowed" {
				t.Errorf("unexpected projected scope: %v", s.Objects)
			}
		}
		if s.State.WatchContinuous && watched {
			cancel()
		}
		watched = watched || s.State.WatchContinuous
		return nil
	})
	if !listed || !watched {
		t.Fatalf("expected a list and continuous watch, got list=%v watch=%v", listed, watched)
	}
}
