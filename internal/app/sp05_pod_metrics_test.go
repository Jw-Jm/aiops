package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	kube "ops-platform/internal/integrations/kubernetes"
)

func TestPodMetricNativeSourceScopeFencing(t *testing.T) {
	expected := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "owned", "namespace": "apps", "uid": "current"}}}
	b := evidence.Binding{ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": "a"}, Scopes: map[string][]string{"cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	for _, name := range []string{"current", "recreated", "foreign namespace", "foreign label", "foreign cluster"} {
		t.Run(name, func(t *testing.T) {
			native := *expected.DeepCopy()
			native.SetLabels(map[string]string{"tenant": "a"})
			cluster := "cluster-a"
			switch name {
			case "recreated":
				native.SetUID("replacement")
			case "foreign namespace":
				native.SetNamespace("other")
			case "foreign label":
				native.SetLabels(map[string]string{"tenant": "b"})
			case "foreign cluster":
				cluster = "cluster-b"
			}
			if got := admittedPodMetricIdentity(expected, native, cluster, b); got != (name == "current") {
				t.Fatalf("scope/UID fence: %s accepted=%t", name, got)
			}
		})
	}
}

func TestOfficialNativeGETPreservesIntegerIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/namespaces/apps/pods/owned" {
			t.Errorf("unexpected native read %s %s", r.Method, r.URL.Path)
			w.WriteHeader(403)
			return
		}
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Pod","status":{"containerStatuses":[{"restartCount":2}]}}`))
	}))
	defer server.Close()
	client, err := kube.NewClient(server.URL, server.Client(), 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	_, object, err := fixedOfficialGET(context.Background(), client, "/api/v1/namespaces/apps/pods/owned", 4096)
	if err != nil {
		t.Fatal(err)
	}
	statuses, _, _ := unstructured.NestedSlice(object.Object, "status", "containerStatuses")
	count, exists, err := unstructured.NestedInt64(statuses[0].(map[string]any), "restartCount")
	if err != nil || !exists || count != 2 {
		t.Fatal("native restartCount decoded with wrong numeric type")
	}
}

func TestOfficialVersionGETAcceptsNativeKindlessVersionContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/version" {
			t.Errorf("unexpected official version read")
			w.WriteHeader(403)
			return
		}
		_, _ = w.Write([]byte(`{"major":"1","minor":"35","gitVersion":"v1.35.6+orb1","platform":"linux/arm64"}`))
	}))
	defer server.Close()
	client, err := kube.NewClient(server.URL, server.Client(), 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	status, object, err := fixedOfficialGET(context.Background(), client, "/version", 64<<10)
	if status != http.StatusOK || err != nil || object.Object["gitVersion"] != "v1.35.6+orb1" {
		t.Fatalf("native kindless /version rejected: status=%d error=%v", status, err)
	}
}
