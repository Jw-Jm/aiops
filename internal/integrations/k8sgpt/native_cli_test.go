package k8sgpt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	kube "ops-platform/internal/integrations/kubernetes"
	"os"
	"testing"
	"time"
)

// This opt-in test executes the unchanged CLI, not a replacement fixture
// executable. The isolated native run must supply enforced kernel limits.
func TestNativeFixedCLIThroughScopedReadBroker(t *testing.T) {
	binary := os.Getenv("OPS_NATIVE_K8SGPT_BINARY")
	if binary == "" {
		t.Skip("native CLI material and isolated kernel limits required")
	}
	if !hardLimits() {
		t.Fatal("native CLI probe requires finite cgroups and read-only root")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var raw string
		switch r.URL.Path {
		case "/version":
			raw = `{"major":"1","minor":"36","gitVersion":"v1.36.3"}`
		case "/api/v1/nodes":
			raw = `{"apiVersion":"v1","kind":"NodeList","metadata":{},"items":[{"apiVersion":"v1","kind":"Node","metadata":{"name":"native-node","uid":"node-native","resourceVersion":"1","labels":{"team":"ops"}},"status":{"conditions":[{"type":"Ready","status":"False","reason":"KubeletNotReady","message":"native probe"}]}}]}`
		case "/api/v1/namespaces/apps/pods":
			raw = `{"apiVersion":"v1","kind":"PodList","metadata":{},"items":[{"apiVersion":"v1","kind":"Pod","metadata":{"name":"native-pod","namespace":"apps","uid":"pod-native","resourceVersion":"1","labels":{"team":"ops"},"ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"native-rs","uid":"rs-native"}]},"spec":{"containers":[{"name":"probe","image":"fixture.invalid/probe"}]},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable","message":"native probe insufficient CPU"}]}}]}`
		case "/api/v1/namespaces/apps/persistentvolumeclaims":
			raw = `{"apiVersion":"v1","kind":"PersistentVolumeClaimList","metadata":{},"items":[{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"native-pvc","namespace":"apps","uid":"pvc-native","resourceVersion":"1","labels":{"team":"ops"}},"status":{"phase":"Pending"}}]}`
		case "/api/v1/namespaces/apps/events":
			raw = `{"apiVersion":"v1","kind":"EventList","metadata":{},"items":[{"apiVersion":"v1","kind":"Event","metadata":{"name":"native-event","namespace":"apps","uid":"event-native"},"involvedObject":{"apiVersion":"v1","kind":"PersistentVolumeClaim","namespace":"apps","name":"native-pvc","uid":"pvc-native"},"reason":"ProvisioningFailed","message":"native fixture provisioning failed","lastTimestamp":"2026-10-10T00:00:00Z"}]}`
		case "/apis/apps/v1/namespaces/apps/replicasets/native-rs":
			raw = `{"apiVersion":"apps/v1","kind":"ReplicaSet","metadata":{"name":"native-rs","namespace":"apps","uid":"rs-native","labels":{"team":"ops"},"ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"native-deployment","uid":"deployment-native"}]}}`
		case "/apis/apps/v1/namespaces/apps/deployments/native-deployment":
			raw = `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"native-deployment","namespace":"apps","uid":"deployment-native","labels":{"team":"ops"}}}`
		default:
			t.Errorf("unexpected native upstream request %s", r.URL.Path)
			http.Error(w, "forbidden", 403)
			return
		}
		var data any
		if json.Unmarshal([]byte(raw), &data) != nil {
			t.Fatal("invalid native fixture")
		}
		json.NewEncoder(w).Encode(data)
	}))
	defer upstream.Close()
	client, err := kube.NewClient(upstream.URL, upstream.Client(), 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	broker := ReadBroker{Client: client, Namespace: "apps", RequiredLabels: map[string]string{"team": "ops"}, Authorize: func(context.Context) error { return nil }}
	path, cleanup, err := broker.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out, err := (Adapter{Binary: binary, Kubeconfig: path, SHA256: NativeLockedBinarySHA256(), Namespace: "apps"}).Run(context.Background())
	if err != nil {
		t.Logf("broker failures: %v", broker.failures)

		t.Fatal(err)
	}
	if !broker.Complete() {
		t.Fatalf("incomplete native read broker: %v", broker.failures)
	}
	kinds := map[string]bool{}
	for _, r := range out.Results {
		kinds[r.Kind] = true
	}
	for _, kind := range []string{"Pod", "Node", "PersistentVolumeClaim"} {
		if !kinds[kind] {
			t.Fatalf("native fixed filter did not detect %s: %+v", kind, out)
		}
	}
	if _, err := Candidates(out, broker.Objects(), "tenant-native", "cluster-native", time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Log("unchanged native CLI passed fixed Pod/Node/PVC filters, TLS read broker, native UID binding and candidate decoding under enforced cgroups/read-only root")
}
