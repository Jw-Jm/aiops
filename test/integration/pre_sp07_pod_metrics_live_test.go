//go:build pre_sp07_live

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	inspection "ops-platform/internal/inspection/kubernetes"
)

// This gate reads the actual aggregated API and current native identities.
// It validates the admitted Inspector projection, not product delivery through
// SourceRegistration, retained Evidence, API and MCP; those gates are separate.
func TestCurrentNativeNodeMetricIdentity(t *testing.T) {
	name, uid, output := os.Getenv("PRE_SP07_METRIC_NODE_NAME"), os.Getenv("PRE_SP07_METRIC_NODE_UID"), os.Getenv("PRE_SP07_METRIC_NODE_RECEIPT")
	if len(validation.IsDNS1123Subdomain(name)) != 0 || uid == "" || output == "" {
		t.Fatal("explicit current native Node identity required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	get := func(path string) unstructured.Unstructured {
		t.Helper()
		raw, err := exec.CommandContext(ctx, "kubectl", "--context", "orbstack", "get", "--raw", path).Output()
		var item unstructured.Unstructured
		if err != nil || len(raw) > 512<<10 || json.Unmarshal(raw, &item) != nil {
			t.Fatal("bounded native Node/metric GET unavailable")
		}
		return item
	}
	path := "/api/v1/nodes/" + name
	node := get(path)
	metrics := get("/apis/metrics.k8s.io/v1beta1/nodes/" + name)
	after := get(path)
	beforeStatus, _, _ := unstructured.NestedMap(node.Object, "status")
	afterStatus, _, _ := unstructured.NestedMap(after.Object, "status")
	if string(node.GetUID()) != uid || after.GetUID() != node.GetUID() || !reflect.DeepEqual(beforeStatus["capacity"], afterStatus["capacity"]) || !reflect.DeepEqual(beforeStatus["allocatable"], afterStatus["allocatable"]) {
		t.Fatal("native Node identity or capacity changed during metric read")
	}
	now := time.Now().UTC()
	signals, err := inspection.MetricSignals(after, metrics, now)
	if err != nil || len(signals) != 2 {
		t.Fatalf("actual current Node metric rejected: %v", err)
	}
	facts := []json.RawMessage{signals[0].NativeData, signals[1].NativeData}
	receipt := map[string]any{"observedAt": now, "nativeNodeUID": uid, "name": name, "facts": facts, "nativeInspectorProjectionPassed": true, "formalSourceEvidenceAPIMCPPipelinePassed": false, "highUtilizationSymptomPassed": false, "fullR3Acceptance": false}
	raw, _ := json.MarshalIndent(receipt, "", "  ")
	if err := os.WriteFile(output, append(raw, '\n'), 0644); err != nil {
		t.Fatal("native Node metric receipt unavailable")
	}
}

func TestCurrentNativePodMetricIdentity(t *testing.T) {
	namespace, name, expectedUID := os.Getenv("PRE_SP07_METRIC_POD_NAMESPACE"), os.Getenv("PRE_SP07_METRIC_POD_NAME"), os.Getenv("PRE_SP07_METRIC_POD_UID")
	output := os.Getenv("PRE_SP07_METRIC_POD_RECEIPT")
	if len(validation.IsDNS1123Label(namespace)) != 0 || len(validation.IsDNS1123Subdomain(name)) != 0 || expectedUID == "" || output == "" {
		t.Fatal("explicit owned current Pod identity required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	get := func(path string) unstructured.Unstructured {
		t.Helper()
		c := exec.CommandContext(ctx, "kubectl", "--context", "orbstack", "get", "--raw", path)
		raw, err := c.Output()
		var item unstructured.Unstructured
		if err != nil || len(raw) > 512<<10 || json.Unmarshal(raw, &item) != nil {
			t.Fatal("bounded native identity/metric GET unavailable")
		}
		return item
	}
	podPath := "/api/v1/namespaces/" + namespace + "/pods/" + name
	pod := get(podPath)
	if string(pod.GetUID()) != expectedUID {
		t.Fatal("owned Pod has been replaced; new UID requires explicit preparation")
	}
	nodeName, _, _ := unstructured.NestedString(pod.Object, "spec", "nodeName")
	if len(validation.IsDNS1123Subdomain(nodeName)) != 0 {
		t.Fatal("current Pod node unavailable")
	}
	nodePath := "/api/v1/nodes/" + nodeName
	node := get(nodePath)
	metricPath := "/apis/metrics.k8s.io/v1beta1/namespaces/" + namespace + "/pods/" + name
	metrics := get(metricPath)
	afterPod, afterNode := get(podPath), get(nodePath)
	beforeContainers, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	afterContainers, _, _ := unstructured.NestedSlice(afterPod.Object, "status", "containerStatuses")
	beforeStatus, _, _ := unstructured.NestedMap(node.Object, "status")
	afterStatus, _, _ := unstructured.NestedMap(afterNode.Object, "status")
	if afterPod.GetUID() != pod.GetUID() || afterNode.GetUID() != node.GetUID() || !reflect.DeepEqual(beforeContainers, afterContainers) || !reflect.DeepEqual(beforeStatus["capacity"], afterStatus["capacity"]) || !reflect.DeepEqual(beforeStatus["allocatable"], afterStatus["allocatable"]) {
		t.Fatal("current Pod/container/Node identity or native capacity changed during metric read")
	}
	now := time.Now().UTC()
	facts, err := inspection.PodMetricObservation(afterPod, afterNode, metrics, now)
	if err != nil {
		t.Fatal("actual current Pod metric rejected by Inspector identity/freshness projection", err)
	}
	receipt := map[string]any{"observedAt": now, "nativePodUID": pod.GetUID(), "nativeNodeUID": node.GetUID(), "namespace": namespace, "name": name, "fixedNativeGETs": []string{podPath, nodePath, metricPath, podPath, nodePath}, "facts": json.RawMessage(facts), "nativeInspectorProjectionPassed": true, "formalSourceEvidenceAPIMCPPipelinePassed": false, "highUtilizationSymptomPassed": false, "performance": "USER_WAIVED", "fullR3Acceptance": false}
	raw, _ := json.MarshalIndent(receipt, "", "  ")
	if os.WriteFile(output, append(raw, '\n'), 0644) != nil {
		t.Fatal("native metric proof receipt unavailable")
	}
}
