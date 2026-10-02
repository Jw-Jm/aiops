package kubernetes

import (
	"context"
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResourceSyncerListWatch410AndProjection(t *testing.T) {
	var mu sync.Mutex
	lists := 0
	watches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("watch") == "true" {
			watches++
			if watches == 1 {
				json.NewEncoder(w).Encode(map[string]any{"type": "ERROR", "object": map[string]any{"code": 410}})
				return
			}
			<-r.Context().Done()
			return
		}
		lists++
		json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "opaque-list"}, "items": []any{map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "web", "namespace": "apps", "uid": "pod-1", "resourceVersion": "rv-x"}, "spec": map[string]any{"nodeName": "node-1", "containers": []any{map[string]any{"env": []any{map[string]any{"value": "secret"}}}}}}}})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []Snapshot
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	client.Run(ctx, CoreRequiredGVRs()[0], func(s Snapshot) error { snapshots = append(snapshots, s); return nil })
	if lists < 2 {
		t.Fatalf("410 failed to relist, lists=%d", lists)
	}
	stale := false
	ready := false
	for _, s := range snapshots {
		if !s.State.WatchContinuous {
			stale = true
		}
		if len(s.Objects) > 0 {
			ready = true
			raw, _ := json.Marshal(s.Objects)
			if string(raw) == "" || containsSecret(raw) {
				t.Fatal("sensitive container payload retained")
			}
		}
	}
	if !stale || !ready {
		t.Fatal("missing rebuild or ready projection")
	}
}
func containsSecret(raw []byte) bool {
	var v any
	json.Unmarshal(raw, &v)
	return string(raw) != "" && stringContains(string(raw), "secret")
}
func stringContains(a, b string) bool {
	for i := 0; i+len(b) <= len(a); i++ {
		if a[i:i+len(b)] == b {
			return true
		}
	}
	return false
}

func TestResourceProjectionRejectsMissingUIDAndSecret(t *testing.T) {
	for _, o := range []unstructured.Unstructured{{Object: map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"uid": "a", "name": "x"}, "data": map[string]any{"token": "secret"}}}, {Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "x"}}}} {
		projected, err := Project(o, time.Now())
		if o.GetUID() == "" {
			if err == nil {
				t.Fatal("missing UID accepted")
			}
		} else {
			raw, _ := json.Marshal(projected)
			if containsSecret(raw) {
				t.Fatal("secret data retained")
			}
		}
	}
	for _, gvr := range CoreRequiredGVRs() {
		if gvr.Group == "kubevirt.io" || gvr.Group == "cdi.kubevirt.io" {
			t.Fatal("virtualization required")
		}
	}
}

func TestNativeListItemsInheritListTypeMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"opaque"},"items":[{"metadata":{"uid":"p1","name":"web","namespace":"apps","resourceVersion":"opaque"},"spec":{"nodeName":"node-1"}}]}`))
	}))
	defer server.Close()
	c, _ := NewClient(server.URL, server.Client(), 20, 50)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	seen := false
	c.Run(ctx, GVR{Version: "v1", Resource: "pods"}, func(s Snapshot) error {
		if len(s.Objects) > 0 && s.State.WatchConnected {
			seen = true
			if s.Objects[0].GetKind() != "Pod" || s.Objects[0].GetAPIVersion() != "v1" {
				t.Fatal("native list type missing")
			}
		}
		return nil
	})
	if !seen {
		t.Fatal("native Kubernetes list rejected")
	}
}

func TestProjectionRejectsNestedPayloadsAndCredentials(t *testing.T) {
	raw := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "p", "uid": "u", "labels": map[string]any{"app": "web", "password": "canary-secret"}}, "spec": map[string]any{"volumes": []any{map[string]any{"name": "data", "secret": map[string]any{"secretName": "credential-ref", "items": []any{map[string]any{"key": "canary-secret", "path": "secret"}}, "unexpected": "canary-secret"}}}}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False", "message": "token=canary-secret", "unexpected": "canary-secret"}}}}}
	out, err := Project(raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(out.Object)
	if strings.Contains(string(encoded), "canary-secret") {
		t.Fatalf("projection leaked nested credentials: %s", encoded)
	}
	if out.GetLabels()["app"] != "web" {
		t.Fatal("relation label missing")
	}
}
