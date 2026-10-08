package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	kube "ops-platform/internal/integrations/kubernetes"
)

func TestOfficialNodeMetricsRejectsTerminatingIdentity(t *testing.T) {
	now := time.Now().UTC()
	native := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": "owned-node", "uid": "current-node", "creationTimestamp": now.Add(-time.Hour).Format(time.RFC3339), "labels": map[string]any{"tenant": "a"}}}}
	expected := *native.DeepCopy()
	binding := evidence.Binding{ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": "a"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/nodes/owned-node" {
			t.Errorf("unexpected Node read: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(native.Object)
	}))
	defer server.Close()
	client, err := kube.NewClient(server.URL, server.Client(), 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readAdmittedNode(context.Background(), client, expected, binding); err != nil {
		t.Fatalf("healthy native identity rejected: %v", err)
	}
	stamp := metav1.NewTime(now)
	native.SetDeletionTimestamp(&stamp)
	if _, err := readAdmittedNode(context.Background(), client, expected, binding); err == nil {
		t.Fatal("native recheck accepted terminating Node with unchanged UID")
	}
}
