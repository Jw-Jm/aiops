package kubernetes

import (
	"encoding/json"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func currentPodMetricInputs() (unstructured.Unstructured, unstructured.Unstructured, unstructured.Unstructured, time.Time) {
	now := time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC)
	birth := now.Add(-time.Hour).Format(time.RFC3339Nano)
	pod := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "owned", "namespace": "owned-ns", "uid": "pod-current", "resourceVersion": "15", "creationTimestamp": birth}, "spec": map[string]any{"nodeName": "current-node", "containers": []any{map[string]any{"name": "application"}}}, "status": map[string]any{"containerStatuses": []any{map[string]any{"name": "application", "containerID": "containerd://current", "restartCount": int64(0), "state": map[string]any{"running": map[string]any{"startedAt": birth}}}}}}}
	node := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": "current-node", "uid": "node-current", "resourceVersion": "20", "creationTimestamp": birth}, "status": map[string]any{"capacity": map[string]any{"cpu": "4", "memory": "8Gi"}, "allocatable": map[string]any{"cpu": "3900m", "memory": "7Gi"}}}}
	metrics := unstructured.Unstructured{Object: map[string]any{"apiVersion": "metrics.k8s.io/v1beta1", "kind": "PodMetrics", "metadata": map[string]any{"name": "owned", "namespace": "owned-ns"}, "timestamp": now.Add(-time.Second).Format(time.RFC3339Nano), "window": "20s", "containers": []any{map[string]any{"name": "application", "usage": map[string]any{"cpu": "1536123640n", "memory": "32768Ki"}}}}}
	return pod, node, metrics, now
}

func TestPodMetricObservationRejectsTerminatingNode(t *testing.T) {
	pod, node, metrics, now := currentPodMetricInputs()
	stamp := metav1.NewTime(now.Add(-time.Second))
	node.SetDeletionTimestamp(&stamp)
	if raw, err := PodMetricObservation(pod, node, metrics, now); err == nil || len(raw) != 0 {
		t.Fatalf("Pod usage bound to terminating Node: bytes=%d err=%v", len(raw), err)
	}
}

func TestPodMetricObservationCurrentNativeIdentity(t *testing.T) {
	pod, node, metrics, now := currentPodMetricInputs()
	raw, err := PodMetricObservation(pod, node, metrics, now)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data["nativeUID"] != "pod-current" || data["nodeUID"] != "node-current" || data["namespace"] != "owned-ns" || data["causalConfirmation"] != false {
		t.Fatal("native metric identity/context lost or converted to causal confirmation")
	}
}

func TestPodMetricObservationRejectsUnverifiableSamples(t *testing.T) {
	for _, name := range []string{"wrong UID", "wrong namespace", "wrong node", "stale", "future", "before pod recreation", "before container restart", "unknown container", "duplicate container", "negative quantity", "missing usage", "invalid window", "missing container identity", "invalid allocatable"} {
		t.Run(name, func(t *testing.T) {
			pod, node, metrics, now := currentPodMetricInputs()
			containers, _, _ := unstructured.NestedSlice(metrics.Object, "containers")
			switch name {
			case "wrong UID":
				metrics.SetUID("pod-old")
			case "wrong namespace":
				metrics.SetNamespace("foreign")
			case "wrong node":
				node.SetName("other-node")
			case "stale":
				metrics.Object["timestamp"] = now.Add(-6 * time.Minute).Format(time.RFC3339Nano)
			case "future":
				metrics.Object["timestamp"] = now.Add(time.Second).Format(time.RFC3339Nano)
			case "before pod recreation":
				_ = unstructured.SetNestedField(pod.Object, now.Add(-5*time.Second).Format(time.RFC3339Nano), "metadata", "creationTimestamp")
			case "before container restart", "missing container identity":
				statuses, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
				if name == "before container restart" {
					statuses[0].(map[string]any)["state"] = map[string]any{"running": map[string]any{"startedAt": now.Add(-5 * time.Second).Format(time.RFC3339Nano)}}
				} else {
					delete(statuses[0].(map[string]any), "containerID")
				}
				_ = unstructured.SetNestedSlice(pod.Object, statuses, "status", "containerStatuses")
			case "unknown container":
				containers[0].(map[string]any)["name"] = "foreign"
			case "duplicate container":
				containers = append(containers, containers[0])
			case "negative quantity":
				containers[0].(map[string]any)["usage"] = map[string]any{"cpu": "-1m", "memory": "1Mi"}
			case "missing usage":
				delete(containers[0].(map[string]any), "usage")
			case "invalid window":
				metrics.Object["window"] = "0s"
			case "invalid allocatable":
				_ = unstructured.SetNestedField(node.Object, "9Gi", "status", "allocatable", "memory")
			}
			_ = unstructured.SetNestedSlice(metrics.Object, containers, "containers")
			if _, err := PodMetricObservation(pod, node, metrics, now); err == nil {
				t.Fatal("unverifiable native metric accepted")
			}
		})
	}
}
