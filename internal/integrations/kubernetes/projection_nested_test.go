package kubernetes

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
	"time"
)

func TestProjectionRejectsNestedSecretPayloadAndOversizedMetadata(t *testing.T) {
	o := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "PersistentVolume", "metadata": map[string]any{"name": "pv", "uid": "uid", "resourceVersion": "1"}, "spec": map[string]any{"claimRef": map[string]any{"name": "claim", "namespace": "apps", "uid": "pvc", "credential": "nested-secret-value"}}, "status": map[string]any{"addresses": []any{map[string]any{"type": "InternalIP", "address": "10.0.0.1", "password": "nested-secret-value"}}}}}
	p, err := Project(o, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p.Object)
	if strings.Contains(string(raw), "nested-secret-value") {
		t.Fatal("nested payload escaped projection")
	}
	labels := map[string]string{}
	for i := 0; i < 1000; i++ {
		labels[strings.Repeat("a", 100)+string(rune(1000+i))] = strings.Repeat("b", 256)
	}
	o.SetLabels(labels)
	if _, err := Project(o, time.Now()); err == nil {
		t.Fatal("unbounded projection accepted")
	}
}
