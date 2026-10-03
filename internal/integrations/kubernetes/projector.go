package kubernetes

import (
	"encoding/json"
	"errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"time"
)

// Project excludes Secret payloads, container env/commands, managedFields and
// arbitrary annotations. Only fields used by locked upstream relations and
// health/identity mapping are copied.
func Project(obj unstructured.Unstructured, observed time.Time) (unstructured.Unstructured, error) {
	if obj.GetUID() == "" || obj.GetName() == "" || obj.GetKind() == "" || obj.GetAPIVersion() == "" || observed.IsZero() {
		return unstructured.Unstructured{}, errors.New("incomplete resource identity")
	}
	out := unstructured.Unstructured{Object: map[string]any{"apiVersion": obj.GetAPIVersion(), "kind": obj.GetKind(), "metadata": map[string]any{"name": obj.GetName(), "uid": string(obj.GetUID()), "resourceVersion": obj.GetResourceVersion()}}}
	out.SetAnnotations(map[string]string{"ops.internal/observed-at": observed.UTC().Format(time.RFC3339Nano)})
	out.SetNamespace(obj.GetNamespace())
	labels := map[string]string{}
	for key, value := range obj.GetLabels() {
		lower := strings.ToLower(key)
		if !strings.Contains(lower, "password") && !strings.Contains(lower, "token") && !strings.Contains(lower, "secret") && !strings.Contains(lower, "credential") && len(value) <= 256 {
			labels[key] = value
		}
	}
	out.SetLabels(labels)
	out.SetOwnerReferences(obj.GetOwnerReferences())
	out.SetCreationTimestamp(obj.GetCreationTimestamp())
	out.SetDeletionTimestamp(obj.GetDeletionTimestamp())
	paths := [][]string{{"spec", "nodeName"}, {"spec", "providerID"}, {"spec", "selector"}, {"spec", "volumeName"}, {"spec", "storageClassName"}, {"spec", "csi", "driver"}, {"spec", "claimRef"}, {"spec", "serviceAccountName"}, {"spec", "scaleTargetRef"}, {"status", "phase"}, {"status", "nodeInfo", "systemUUID"}, {"status", "nodeInfo", "machineID"}, {"status", "addresses"}, {"involvedObject"}, {"regarding"}, {"reason"}, {"reportingController"}, {"type"}, {"lastTimestamp"}, {"firstTimestamp"}, {"eventTime"}, {"count"}, {"provisioner"}, {"subjects"}, {"roleRef"}, {"endpoints"}, {"addressType"}}
	for _, path := range paths {
		value, found, err := unstructured.NestedFieldCopy(obj.Object, path...)
		if err != nil {
			return out, err
		}
		if found {
			value = projectNested(path, value)
			if value == nil {
				continue
			}
			if err := unstructured.SetNestedField(out.Object, value, path...); err != nil {
				return out, err
			}
		}
	}
	volumes, _, _ := unstructured.NestedSlice(obj.Object, "spec", "volumes")
	safe := []any{}
	for _, v := range volumes {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		p := map[string]any{}
		if name, ok := m["name"].(string); ok {
			p["name"] = name
		}
		for source, key := range map[string]string{"persistentVolumeClaim": "claimName", "configMap": "name", "secret": "secretName"} {
			if nested, ok := m[source].(map[string]any); ok {
				if name, ok := nested[key].(string); ok {
					p[source] = map[string]any{key: name}
				}
			}
		}
		safe = append(safe, p)
	}
	if len(safe) > 0 {
		unstructured.SetNestedSlice(out.Object, safe, "spec", "volumes")
	}
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	projected := []any{}
	for _, c := range conditions {
		if m, ok := c.(map[string]any); ok {
			item := map[string]any{}
			for _, k := range []string{"type", "status", "reason", "lastTransitionTime"} {
				if value, ok := m[k].(string); ok && len(value) <= 128 {
					item[k] = value
				}
			}
			projected = append(projected, item)
		}
	}
	if len(projected) > 0 {
		unstructured.SetNestedSlice(out.Object, projected, "status", "conditions")
	}
	raw, err := json.Marshal(out.Object)
	if err != nil || len(raw) > 64<<10 {
		return unstructured.Unstructured{}, errors.New("projection byte budget exhausted")
	}
	return out, nil
}
