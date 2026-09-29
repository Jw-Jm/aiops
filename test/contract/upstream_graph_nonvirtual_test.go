package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aalpar/ariadne"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestAriadneNonVirtualGolden20K(t *testing.T) {
	root := filepath.Join("..", "..")
	fixturePath := filepath.Join(root, "test", "fixtures", "upstream-graph", "nonvirtual-golden-input.json")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var want struct {
		SchemaVersion int    `json:"schemaVersion"`
		ObjectCount   int    `json:"objectCount"`
		Generator     string `json:"generator"`
		ObjectSHA256  string `json:"objectSha256"`
	}
	if err := json.Unmarshal(fixture, &want); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	timing := time.Now()
	objects := nonVirtualGoldenObjects()
	t.Logf("build_objects=%s", time.Since(timing))
	encoded, err := json.Marshal(objects)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	gotDigest := hex.EncodeToString(digest[:])
	if want.SchemaVersion != 1 || want.ObjectCount != 20000 || want.Generator != "deterministic-k8s-owner-selector-pvc-csi-hardware-deepflow-no-virtualization" || want.ObjectSHA256 != gotDigest {
		t.Fatalf("nonvirtual 20k fixture mismatch: metadata=%+v actualCount=%d actualSHA256=%s", want, len(objects), gotDigest)
	}

	graph := ariadne.New(ariadne.WithResolver(ariadne.NewStructuralResolver()), ariadne.WithResolver(ariadne.NewSelectorResolver()), ariadne.WithResolver(ariadne.NewRuleResolver("nonvirtual-external/v1",
		ariadne.RefRule{FromGroup: "example.hardware", FromKind: "HardwareNode", ToKind: "Node", FieldPath: "spec.nodeName", ClusterScoped: true},
		ariadne.RefRule{FromGroup: "deepflow.example", FromKind: "FlowEndpoint", ToKind: "Pod", FieldPath: "spec.podName"},
	)))
	timing = time.Now()
	graph.Load(objects)
	t.Logf("graph_load=%s", time.Since(timing))
	if got := len(graph.Nodes()); got != 20000 {
		t.Fatalf("loaded graph objects=%d, want 20000", got)
	}

	pod := ariadne.ObjectRef{Kind: "Pod", Namespace: "apps", Name: "web-0"}
	if !hasNonVirtualAriadneEdge(graph.DependenciesOf(pod), "PersistentVolumeClaim", "web-data", "ref") || !hasNonVirtualAriadneEdge(graph.DependenciesOf(pod), "Node", "node-a", "ref") {
		t.Fatalf("Pod resource references missing: %#v", graph.DependenciesOf(pod))
	}
	if !hasAriadneSource(graph.DependentsOf(pod), "Service", "web", "label_selector") || !hasAriadneSource(graph.DependentsOf(pod), "FlowEndpoint", "web-flow", "ref") {
		t.Fatalf("selector or temporary external edge missing: %#v", graph.DependentsOf(pod))
	}
	pvc := ariadne.ObjectRef{Kind: "PersistentVolumeClaim", Namespace: "apps", Name: "web-data"}
	if !hasNonVirtualAriadneEdge(graph.DependenciesOf(pvc), "PersistentVolume", "pv-web-data", "ref") || !hasNonVirtualAriadneEdge(graph.DependenciesOf(pvc), "StorageClass", "fast", "ref") {
		t.Fatalf("PVC storage references missing: %#v", graph.DependenciesOf(pvc))
	}
	pv := ariadne.ObjectRef{Kind: "PersistentVolume", Name: "pv-web-data"}
	if !hasNonVirtualAriadneEdge(graph.DependenciesOf(pv), "CSIDriver", "fast-csi", "ref") {
		t.Fatal("PV to CSI driver reference missing")
	}
	hardware := ariadne.ObjectRef{Group: "example.hardware", Kind: "HardwareNode", Namespace: "apps", Name: "rack-1"}
	if !hasNonVirtualAriadneEdge(graph.DependenciesOf(hardware), "Node", "node-a", "ref") {
		t.Fatal("external hardware edge missing")
	}

	updated := graphObject(t, "v1", "Pod", "apps", "web-0", "pod-1", map[string]any{"volumes": []any{map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": "missing-data"}}}}, map[string]string{"app": "changed"}, nil)
	graph.Add(updated)
	if hasNonVirtualAriadneEdge(graph.DependenciesOf(pod), "PersistentVolumeClaim", "web-data", "ref") || hasAriadneSource(graph.DependentsOf(pod), "Service", "web", "label_selector") {
		t.Fatal("updated Pod retained a stale reference or selector edge")
	}
	graph.Remove(pod)
	if graph.Has(pod) || len(graph.DependenciesOf(pod)) != 0 || len(graph.DependentsOf(pod)) != 0 {
		t.Fatal("removed Pod retained graph nodes or edges")
	}
}

func nonVirtualGoldenObjects() []unstructured.Unstructured {
	objects := []unstructured.Unstructured{
		graphObject(nil, "v1", "ConfigMap", "apps", "web-config", "cm-1", nil, nil, nil),
		graphObject(nil, "apps/v1", "Deployment", "apps", "web", "deploy-1", nil, nil, nil),
		graphObject(nil, "apps/v1", "ReplicaSet", "apps", "web-rs", "rs-1", nil, nil, []map[string]string{{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web", "uid": "deploy-1"}}),
		graphObject(nil, "v1", "Pod", "apps", "web-0", "pod-1", map[string]any{"nodeName": "node-a", "volumes": []any{map[string]any{"name": "config", "configMap": map[string]any{"name": "web-config"}}, map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": "web-data"}}}}, map[string]string{"app": "web"}, []map[string]string{{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "web-rs", "uid": "rs-1"}}),
		graphObject(nil, "v1", "PersistentVolumeClaim", "apps", "web-data", "pvc-1", map[string]any{"storageClassName": "fast", "volumeName": "pv-web-data"}, nil, nil),
		graphObject(nil, "v1", "PersistentVolume", "", "pv-web-data", "pv-1", map[string]any{"storageClassName": "fast", "csi": map[string]any{"driver": "fast-csi"}}, nil, nil),
		graphObject(nil, "storage.k8s.io/v1", "StorageClass", "", "fast", "sc-1", nil, nil, nil),
		graphObject(nil, "storage.k8s.io/v1", "CSIDriver", "", "fast-csi", "csi-1", nil, nil, nil),
		graphObject(nil, "v1", "Service", "apps", "web", "svc-1", map[string]any{"selector": map[string]any{"app": "web"}}, nil, nil),
		graphObject(nil, "v1", "Node", "", "node-a", "node-1", nil, nil, nil),
		graphObject(nil, "example.hardware/v1", "HardwareNode", "apps", "rack-1", "hw-1", map[string]any{"nodeName": "node-a"}, nil, nil),
		graphObject(nil, "deepflow.example/v1", "FlowEndpoint", "apps", "web-flow", "flow-1", map[string]any{"podName": "web-0", "expiresAt": "2026-09-29T09:59:00Z"}, nil, nil),
	}
	for index := 1; index <= 19988; index++ {
		objects = append(objects, graphObject(nil, "v1", "Pod", "load", fmt.Sprintf("pod-%05d", index), fmt.Sprintf("bulk-%05d", index), nil, map[string]string{"class": "bulk"}, nil))
	}
	return objects
}

func graphObject(t *testing.T, apiVersion, kind, namespace, name, uid string, spec map[string]any, labels map[string]string, owners []map[string]string) unstructured.Unstructured {
	if t != nil {
		t.Helper()
	}
	metadata := map[string]any{"name": name, "uid": uid}
	if namespace != "" {
		metadata["namespace"] = namespace
	}
	if labels != nil {
		converted := map[string]any{}
		for key, value := range labels {
			converted[key] = value
		}
		metadata["labels"] = converted
	}
	if owners != nil {
		converted := make([]any, 0, len(owners))
		for _, owner := range owners {
			item := map[string]any{}
			for key, value := range owner {
				item[key] = value
			}
			converted = append(converted, item)
		}
		metadata["ownerReferences"] = converted
	}
	object := map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": metadata}
	if spec != nil {
		object["spec"] = spec
	}
	return unstructured.Unstructured{Object: object}
}

func hasAriadneSource(edges []ariadne.Edge, kind, name, edgeType string) bool {
	for _, edge := range edges {
		if edge.Type.String() == edgeType && edge.From.Kind == kind && edge.From.Name == name {
			return true
		}
	}
	return false
}

func hasNonVirtualAriadneEdge(edges []ariadne.Edge, kind, name, edgeType string) bool {
	for _, edge := range edges {
		if edge.To.Kind == kind && edge.To.Name == name && edge.Type.String() == edgeType {
			return true
		}
	}
	return false
}
