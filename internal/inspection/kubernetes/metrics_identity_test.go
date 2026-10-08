package kubernetes

import (
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
	"time"
)

func TestMetricRejectsConflictingUIDAndPreviousNodeWindow(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	node := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": "node-a", "uid": "current-node", "creationTimestamp": now.Add(-time.Minute).Format(time.RFC3339)}, "status": map[string]any{"allocatable": map[string]any{"cpu": "2", "memory": "4Gi"}, "capacity": map[string]any{"cpu": "4", "memory": "8Gi"}}}}
	metric := unstructured.Unstructured{Object: map[string]any{"apiVersion": "metrics.k8s.io/v1beta1", "kind": "NodeMetrics", "metadata": map[string]any{"name": "node-a"}, "timestamp": now.Format(time.RFC3339), "window": "30s", "usage": map[string]any{"cpu": "1900m", "memory": "1Gi"}}}
	t.Run("explicit_wrong_uid", func(t *testing.T) {
		m := metric.DeepCopy()
		m.SetUID("previous-node")
		if _, err := MetricSignals(node, *m, now); err == nil {
			t.Fatal("conflicting metric identity accepted")
		}
	})
	t.Run("window_crosses_recreation", func(t *testing.T) {
		m := metric.DeepCopy()
		m.Object["window"] = "2m"
		if _, err := MetricSignals(node, *m, now); err == nil {
			t.Fatal("window includes previous Node identity")
		}
	})
	t.Run("allocatable_exceeds_capacity", func(t *testing.T) {
		n := node.DeepCopy()
		_ = unstructured.SetNestedField(n.Object, "1", "status", "capacity", "cpu")
		if _, err := MetricSignals(*n, metric, now); err == nil {
			t.Fatal("invalid native capacity accepted")
		}
	})
	t.Run("positive_capacity_binding", func(t *testing.T) {
		c, err := MetricSignals(node, metric, now)
		if err != nil || len(c) != 2 {
			t.Fatalf("valid sample rejected: %v", err)
		}
		var data map[string]any
		if json.Unmarshal(c[0].NativeData, &data) != nil || data["capacity"] != "4" || data["nativeUID"] != "current-node" {
			t.Fatal("native capacity/UID missing from retained fact")
		}
		if c[0].State != "firing" || c[1].State != "resolved" {
			t.Fatal("formal threshold changed")
		}
	})
	t.Run("missing_native_capacity", func(t *testing.T) {
		n := node.DeepCopy()
		unstructured.RemoveNestedField(n.Object, "status", "capacity")
		if _, err := MetricSignals(*n, metric, now); err == nil {
			t.Fatal("metric symptom accepted without current Node capacity")
		}
	})
	for _, name := range []string{"terminating_node", "missing_creation_time", "namespaced_node"} {
		t.Run(name, func(t *testing.T) {
			n := node.DeepCopy()
			switch name {
			case "terminating_node":
				stamp := metav1.NewTime(now.Add(-time.Second))
				n.SetDeletionTimestamp(&stamp)
			case "missing_creation_time":
				unstructured.RemoveNestedField(n.Object, "metadata", "creationTimestamp")
			case "namespaced_node":
				n.SetNamespace("foreign")
			}
			// Low usage must not resolve a symptom without a current Node.
			m := metric.DeepCopy()
			_ = unstructured.SetNestedField(m.Object, "100m", "usage", "cpu")
			if signals, err := MetricSignals(*n, *m, now); err == nil || len(signals) != 0 {
				t.Fatalf("invalid Node emitted recovery signals: signals=%d err=%v", len(signals), err)
			}
		})
	}
}
