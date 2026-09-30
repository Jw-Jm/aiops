package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aalpar/ariadne"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestAriadneResolvesReferencesAndSelectorsFromGoldenInput(t *testing.T) {
	root := filepath.Join("..", "..")
	fixturePath := filepath.Join(root, "test", "fixtures", "upstream-graph", "golden-input.json")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read upstream graph fixture: %v", err)
	}
	var golden struct {
		SchemaVersion int    `json:"schemaVersion"`
		ObjectCount   int    `json:"objectCount"`
		Generator     string `json:"generator"`
		ObjectSHA256  string `json:"objectSha256"`
	}
	if err := json.Unmarshal(fixture, &golden); err != nil {
		t.Fatalf("decode upstream graph fixture: %v", err)
	}
	if golden.SchemaVersion != 1 || golden.ObjectCount != 20000 || golden.Generator != "deterministic-k8s-owner-selector-pvc-csi-vmi-external-overlay" {
		t.Fatalf("unexpected upstream graph fixture metadata: %+v", golden)
	}

	objects := goldenGraphObjects()
	if len(objects) != golden.ObjectCount {
		t.Fatalf("golden fixture objects = %d, want %d", len(objects), golden.ObjectCount)
	}
	encodedObjects, err := json.Marshal(objects)
	if err != nil {
		t.Fatalf("encode golden graph objects: %v", err)
	}
	objectHash := sha256.Sum256(encodedObjects)
	if got := hex.EncodeToString(objectHash[:]); got != golden.ObjectSHA256 {
		t.Fatalf("golden object SHA-256 = %s, want %s", got, golden.ObjectSHA256)
	}
	graph := ariadne.New(ariadne.WithResolver(ariadne.NewRuleResolver("platform-conformance-v1",
		ariadne.RefRule{FromKind: "Pod", ToKind: "ConfigMap", FieldPath: "spec.volumes[*].configMap.name"},
		ariadne.RefRule{FromKind: "Pod", ToKind: "Secret", FieldPath: "spec.volumes[*].secret.secretName"},
		ariadne.RefRule{FromGroup: "kubevirt.io", FromKind: "VirtualMachineInstance", ToKind: "PersistentVolumeClaim", FieldPath: "spec.volumes[*].persistentVolumeClaim.claimName"},
		ariadne.RefRule{FromKind: "PersistentVolumeClaim", ToKind: "PersistentVolume", FieldPath: "spec.volumeName", ClusterScoped: true},
		ariadne.RefRule{FromKind: "PersistentVolumeClaim", ToGroup: "storage.k8s.io", ToKind: "StorageClass", FieldPath: "spec.storageClassName", ClusterScoped: true},
		ariadne.RefRule{FromKind: "PersistentVolume", ToGroup: "storage.k8s.io", ToKind: "CSIDriver", FieldPath: "spec.csi.driver", ClusterScoped: true},
		ariadne.LabelSelectorRule{FromKind: "Service", ToKind: "Pod", SelectorFieldPath: "spec.selector"},
		ariadne.RefRule{FromGroup: "example.hardware", FromKind: "HardwareNode", ToKind: "Node", FieldPath: "spec.nodeName", ClusterScoped: true},
		ariadne.RefRule{FromGroup: "deepflow.example", FromKind: "FlowEndpoint", ToKind: "Pod", FieldPath: "spec.podName"},
	)))
	graph.Load(objects)

	dependent := ariadne.ObjectRef{Kind: "Pod", Namespace: "golden", Name: "pod-00001"}
	dependencies := graph.DependenciesOf(dependent)
	if !hasAriadneEdge(dependencies, "ConfigMap", "config-shared", "ref") {
		t.Fatalf("Pod dependency edge missing: %#v", dependencies)
	}
	if !hasAriadneEdge(dependencies, "Secret", "secret-00001", "ref") {
		t.Fatalf("Pod secret edge missing: %#v", dependencies)
	}
	service := ariadne.ObjectRef{Kind: "Service", Namespace: "golden", Name: "all-pods"}
	if got := graph.DependenciesOf(service); len(got) != 1 || !hasAriadneEdge(got, "Pod", "pod-00001", "label_selector") {
		t.Fatalf("Service selector edges = %#v, want the one matching Pod", got)
	}
	vm := ariadne.ObjectRef{Group: "kubevirt.io", Kind: "VirtualMachineInstance", Namespace: "golden", Name: "vm-00001"}
	if got := graph.DependenciesOf(vm); !hasAriadneEdge(got, "PersistentVolumeClaim", "vm-disk-00001", "ref") {
		t.Fatalf("KubeVirt VMI PVC edge missing: %#v", got)
	}
	pvc := ariadne.ObjectRef{Kind: "PersistentVolumeClaim", Namespace: "golden", Name: "vm-disk-00001"}
	if got := graph.DependenciesOf(pvc); !hasAriadneEdge(got, "PersistentVolume", "pv-vm-disk-00001", "ref") || !hasAriadneEdge(got, "StorageClass", "fast", "ref") {
		t.Fatalf("PVC/PV/StorageClass dependency edges missing: %#v", got)
	}
	pv := ariadne.ObjectRef{Kind: "PersistentVolume", Name: "pv-vm-disk-00001"}
	if got := graph.DependenciesOf(pv); !hasAriadneEdge(got, "CSIDriver", "example.csi", "ref") {
		t.Fatalf("PV/CSI dependency edge missing: %#v", got)
	}
	hardware := ariadne.ObjectRef{Group: "example.hardware", Kind: "HardwareNode", Namespace: "golden", Name: "bmc-1"}
	if got := graph.DependenciesOf(hardware); !hasAriadneEdge(got, "Node", "orbstack", "ref") {
		t.Fatalf("bounded hardware overlay edge missing: %#v", got)
	}
	flow := ariadne.ObjectRef{Group: "deepflow.example", Kind: "FlowEndpoint", Namespace: "golden", Name: "flow-1"}
	if got := graph.DependenciesOf(flow); !hasAriadneEdge(got, "Pod", "pod-00001", "ref") {
		t.Fatalf("temporary DeepFlow overlay edge missing: %#v", got)
	}
	ownerGraph := ariadne.New(ariadne.WithResolver(ariadne.NewStructuralResolver()))
	ownerGraph.Load(selectOwnerChain(objects))
	if got := ownerGraph.DependenciesOf(dependent); !hasAriadneEdge(got, "ReplicaSet", "rs-1", "ref") {
		t.Fatalf("Ariadne ownerReference resolver edge missing: %#v", got)
	}
	rs := ariadne.ObjectRef{Group: "apps", Kind: "ReplicaSet", Namespace: "golden", Name: "rs-1"}
	if got := ownerGraph.DependenciesOf(rs); !hasAriadneEdge(got, "Deployment", "deployment-1", "ref") {
		t.Fatalf("Ariadne recursive ownerReference edge missing: %#v", got)
	}
	updatedPod := objects[1].DeepCopy()
	updatedPod.Object["spec"].(map[string]any)["volumes"] = []any{map[string]any{
		"name": "settings", "configMap": map[string]any{"name": "config-updated"},
	}}
	graph.Add(*updatedPod)
	if got := graph.DependenciesOf(dependent); hasAriadneEdge(got, "ConfigMap", "config-shared", "ref") {
		t.Fatalf("incremental update retained stale dependency edge: %#v", got)
	}
	graph.Remove(dependent)
	if graph.Has(dependent) || len(graph.DependenciesOf(dependent)) != 0 {
		t.Fatalf("incremental removal retained the Pod or its dependencies: %#v", graph.DependenciesOf(dependent))
	}
}

func selectOwnerChain(objects []unstructured.Unstructured) []unstructured.Unstructured {
	selected := make([]unstructured.Unstructured, 0, 3)
	for _, object := range objects {
		kind := object.GetKind()
		if (kind == "Pod" && object.GetName() == "pod-00001") || (kind == "ReplicaSet" && object.GetName() == "rs-1") || (kind == "Deployment" && object.GetName() == "deployment-1") {
			selected = append(selected, object)
		}
	}
	return selected
}

func TestAriadneInstancesAreIndependentAndRebuildReplacesSnapshot(t *testing.T) {
	facts := selectOwnerChain(goldenGraphObjects())
	newGraph := func(objects []unstructured.Unstructured) *ariadne.Graph {
		graph := ariadne.New(ariadne.WithResolver(ariadne.NewStructuralResolver()))
		graph.Load(objects)
		return graph
	}
	// Identical Kubernetes IDs in distinct instances must not share mutable
	// nodes or edges. Production tenant+cluster routing is still unverified.
	first, second := newGraph(facts), newGraph(facts)
	pod := ariadne.ObjectRef{Kind: "Pod", Namespace: "golden", Name: "pod-00001"}
	first.Remove(pod)
	if first.Has(pod) || !second.Has(pod) || !hasAriadneEdge(second.DependenciesOf(pod), "ReplicaSet", "rs-1", "ref") {
		t.Fatal("mutating one Ariadne instance changed another instance")
	}
	// Load is additive. Rebuilding a replacement snapshot must create a
	// fresh upstream instance, rather than loading into the previous graph.
	replacement := []unstructured.Unstructured{}
	for _, object := range facts {
		if object.GetKind() != "Pod" {
			replacement = append(replacement, object)
		}
	}
	rebuilt := newGraph(replacement)
	if rebuilt.Has(pod) || len(rebuilt.Nodes()) != len(replacement) || len(rebuilt.DependenciesOf(pod)) != 0 {
		t.Fatal("fresh snapshot reconstruction retained a removed fact")
	}
	if !second.Has(pod) {
		t.Fatal("reconstruction mutated the original snapshot")
	}
}

func goldenGraphObjects() []unstructured.Unstructured {
	objects := make([]unstructured.Unstructured, 0, 20000)
	objects = append(objects, unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "config-shared", "namespace": "golden"},
	}})
	for index := 1; index <= 19978; index++ {
		name := fmt.Sprintf("%05d", index)
		pod := unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{
				"name": "pod-" + name, "namespace": "golden",
				"labels": map[string]any{"app": "pod-" + name},
			},
			"spec": map[string]any{"volumes": []any{
				map[string]any{"name": "settings", "configMap": map[string]any{"name": "config-shared"}},
			}},
		}}
		if index == 1 {
			pod.Object["metadata"].(map[string]any)["ownerReferences"] = []any{map[string]any{
				"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "rs-1", "uid": "rs-1",
			}}
			pod.Object["spec"].(map[string]any)["volumes"] = append(
				pod.Object["spec"].(map[string]any)["volumes"].([]any),
				map[string]any{"name": "credentials", "secret": map[string]any{"secretName": "secret-00001"}},
			)
		}
		objects = append(objects, pod)
	}
	for index := 1; index <= 4; index++ {
		name := fmt.Sprintf("%05d", index)
		objects = append(objects,
			unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "PersistentVolumeClaim",
				"metadata": map[string]any{"name": "vm-disk-" + name, "namespace": "golden"},
				"spec":     map[string]any{"storageClassName": "fast", "volumeName": "pv-vm-disk-" + name},
			}},
			unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "PersistentVolume",
				"metadata": map[string]any{"name": "pv-vm-disk-" + name},
				"spec":     map[string]any{"storageClassName": "fast", "csi": map[string]any{"driver": "example.csi"}},
			}},
			unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance",
				"metadata": map[string]any{"name": "vm-" + name, "namespace": "golden"},
				"spec": map[string]any{"volumes": []any{map[string]any{
					"name": "root", "persistentVolumeClaim": map[string]any{"claimName": "vm-disk-" + name},
				}}},
			}},
		)
	}
	objects = append(objects,
		unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]any{"name": "orbstack"},
		}},
		unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Service",
			"metadata": map[string]any{"name": "all-pods", "namespace": "golden"},
			"spec":     map[string]any{"selector": map[string]any{"app": "pod-00001"}},
		}},
		unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "storage.k8s.io/v1", "kind": "StorageClass",
			"metadata": map[string]any{"name": "fast"},
		}},
		unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "storage.k8s.io/v1", "kind": "CSIDriver",
			"metadata": map[string]any{"name": "example.csi"},
		}},
		unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "example.hardware/v1", "kind": "HardwareNode",
			"metadata": map[string]any{"name": "bmc-1", "namespace": "golden"},
			"spec":     map[string]any{"nodeName": "orbstack"},
		}},
		unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "deepflow.example/v1", "kind": "FlowEndpoint",
			"metadata": map[string]any{"name": "flow-1", "namespace": "golden"},
			"spec":     map[string]any{"podName": "pod-00001"},
		}},
		unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Secret",
			"metadata": map[string]any{"name": "secret-00001", "namespace": "golden"},
		}},
		unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1", "kind": "ReplicaSet",
			"metadata": map[string]any{"name": "rs-1", "namespace": "golden", "uid": "rs-1", "ownerReferences": []any{map[string]any{
				"apiVersion": "apps/v1", "kind": "Deployment", "name": "deployment-1", "uid": "deployment-1",
			}}},
		}},
		unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "deployment-1", "namespace": "golden", "uid": "deployment-1"},
		}},
	)
	return objects
}

func hasAriadneEdge(edges []ariadne.Edge, kind, name, edgeType string) bool {
	for _, edge := range edges {
		if edge.To.Kind == kind && edge.To.Name == name && edge.Type.String() == edgeType {
			return true
		}
	}
	return false
}
