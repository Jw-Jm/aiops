package graph

import (
	"context"
	"encoding/json"
	"errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/integrations/kubernetes"
	"strconv"
	"sync"
	"testing"
	"time"
)

type faultMirror struct {
	mu    sync.Mutex
	value OwnershipMirror
	fail  bool
}

func (m *faultMirror) Load(context.Context, string, string) (OwnershipMirror, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return OwnershipMirror{}, errors.New("mirror unavailable")
	}
	return m.value, nil
}
func (m *faultMirror) Record(_ context.Context, v OwnershipMirror) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("mirror unavailable")
	}
	m.value = v
	return nil
}
func TestLeaseIsOnlyAuthorityRestartStandbyPartitionAndRecreation(t *testing.T) {
	var mu sync.Mutex
	version := 1
	status := 200
	doc := LeaseDocument{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	doc.Metadata.Name = "graph"
	doc.Metadata.Namespace = "owned"
	doc.Metadata.UID = "lease-original"
	doc.Metadata.ResourceVersion = "1"
	doc.Metadata.Annotations = map[string]string{"ops.platform/owner-epoch": "0"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		if r.Method == "PUT" {
			var next LeaseDocument
			if json.NewDecoder(r.Body).Decode(&next) != nil || next.Metadata.ResourceVersion != doc.Metadata.ResourceVersion {
				w.WriteHeader(409)
				return
			}
			version++
			next.Metadata.ResourceVersion = strconv.Itoa(version)
			doc = next
		}
		json.NewEncoder(w).Encode(doc)
	}))
	defer server.Close()
	client, err := kubernetes.NewClient(server.URL, server.Client(), 20, 50)
	if err != nil {
		t.Fatal(err)
	}
	mirror := &faultMirror{}
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	warm := func(instance string) *Graph {
		g := New("tenant-a", "cluster-a", instance, []kubernetes.GVR{gvr})
		if err := g.Replace(context.Background(), 0, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{object("Pod", "apps", "web", "pod-a", nil)}, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}}); err != nil {
			t.Fatal(err)
		}
		return g
	}
	active := warm("pod-process-1")
	first := &Lease{Client: client, Graph: active, Mirror: mirror, Namespace: "owned", Name: "graph", Endpoint: "https://127.0.0.1:9444"}
	ctx := context.Background()
	if err := first.Tick(ctx); err != nil || active.OwnerEpoch() != 1 {
		t.Fatalf("acquire %v epoch=%d", err, active.OwnerEpoch())
	}
	// A database route cannot elect a new process, even when the route expired.
	mirror.mu.Lock()
	mirror.value.ExpiresAt = time.Now().Add(-time.Hour)
	mirror.mu.Unlock()
	restart := warm("same-pod-new-process")
	second := &Lease{Client: client, Graph: restart, Mirror: mirror, Namespace: "owned", Name: "graph", Endpoint: "https://127.0.0.1:9444"}
	if err := second.Tick(ctx); err == nil || restart.OwnerEpoch() != 0 {
		t.Fatal("DB mirror elected pod restart")
	}
	mu.Lock()
	doc.Spec.RenewTime = time.Now().Add(-time.Minute)
	mu.Unlock()
	unsynced := New("tenant-a", "cluster-a", "standby-unsynced", []kubernetes.GVR{gvr})
	third := &Lease{Client: client, Graph: unsynced, Mirror: mirror, Namespace: "owned", Name: "graph", Endpoint: "https://127.0.0.1:9444"}
	if err := third.Tick(ctx); err == nil {
		t.Fatal("unsynced standby became active")
	}
	if err := second.Tick(ctx); err != nil || restart.OwnerEpoch() != 2 {
		t.Fatalf("restart takeover %v epoch=%d", err, restart.OwnerEpoch())
	}
	if err := first.Confirm(ctx, 1); err == nil {
		t.Fatal("old active survived fencing")
	}
	mu.Lock()
	status = 503
	mu.Unlock()
	if err := second.Tick(ctx); err == nil {
		t.Fatal("API partition retained lease")
	}
	restart.mu.RLock()
	deadline := restart.deadline
	restart.mu.RUnlock()
	if !deadline.IsZero() {
		t.Fatal("partition retained query deadline")
	}
	mu.Lock()
	status = 404
	mu.Unlock()
	if err := second.Tick(ctx); err == nil {
		t.Fatal("deleted Lease elected owner")
	}
	mu.Lock()
	status = 200
	doc.Metadata.UID = "recreated"
	doc.Metadata.Annotations["ops.platform/owner-epoch"] = "0"
	mu.Unlock()
	if err := second.Tick(ctx); err == nil {
		t.Fatal("recreated Lease reset epoch")
	}
}
