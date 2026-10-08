package kubernetes

import (
	"encoding/json"
	"time"

	apiresource "k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// PodMetricObservation binds the name-based aggregated measurement to the
// current native Pod, Node and running container identities. Its caller must
// authorize the Source/namespace and fence native GETs before and after the
// metric read. A usage observation is context; it creates no symptom or cause.
// Restartable init and ephemeral containers remain unavailable until their
// distinct native status/measurement association is implemented and verified.
func PodMetricObservation(pod, node, metrics unstructured.Unstructured, observed time.Time) (json.RawMessage, error) {
	if pod.GetAPIVersion() != "v1" || pod.GetKind() != "Pod" || pod.GetUID() == "" || pod.GetName() == "" || pod.GetNamespace() == "" || pod.GetDeletionTimestamp() != nil || node.GetAPIVersion() != "v1" || node.GetKind() != "Node" || node.GetUID() == "" || node.GetName() == "" || node.GetNamespace() != "" || node.GetDeletionTimestamp() != nil || metrics.GetAPIVersion() != "metrics.k8s.io/v1beta1" || metrics.GetKind() != "PodMetrics" || metrics.GetName() != pod.GetName() || metrics.GetNamespace() != pod.GetNamespace() || metrics.GetUID() != "" && metrics.GetUID() != pod.GetUID() || observed.IsZero() {
		return nil, ErrObservation
	}
	nodeName, _, _ := unstructured.NestedString(pod.Object, "spec", "nodeName")
	if nodeName != node.GetName() {
		return nil, ErrObservation
	}
	timestamp, _, _ := unstructured.NestedString(metrics.Object, "timestamp")
	stamp, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil || stamp.After(observed) || stamp.Before(observed.Add(-5*time.Minute)) {
		return nil, ErrObservation
	}
	window, _, _ := unstructured.NestedString(metrics.Object, "window")
	duration, err := time.ParseDuration(window)
	if err != nil || duration <= 0 || duration > 5*time.Minute {
		return nil, ErrObservation
	}
	from := stamp.Add(-duration)
	for _, birth := range []time.Time{pod.GetCreationTimestamp().Time, node.GetCreationTimestamp().Time} {
		if birth.IsZero() || from.Before(birth) {
			return nil, ErrObservation
		}
	}
	capacity, _, _ := unstructured.NestedStringMap(node.Object, "status", "capacity")
	allocatable, _, _ := unstructured.NestedStringMap(node.Object, "status", "allocatable")
	for _, resource := range []string{"cpu", "memory"} {
		physical, e1 := apiresource.ParseQuantity(capacity[resource])
		available, e2 := apiresource.ParseQuantity(allocatable[resource])
		if e1 != nil || e2 != nil || physical.Sign() <= 0 || available.Sign() <= 0 || available.Cmp(physical) > 0 {
			return nil, ErrObservation
		}
	}
	// Only quantities needed to interpret this measurement enter its evidence.
	capacity = map[string]string{"cpu": capacity["cpu"], "memory": capacity["memory"]}
	allocatable = map[string]string{"cpu": allocatable["cpu"], "memory": allocatable["memory"]}
	ephemeral, _, _ := unstructured.NestedSlice(pod.Object, "spec", "ephemeralContainers")
	init, _, _ := unstructured.NestedSlice(pod.Object, "spec", "initContainers")
	if len(ephemeral) != 0 {
		return nil, ErrObservation
	}
	for _, item := range init {
		entry, ok := item.(map[string]any)
		if !ok || entry["restartPolicy"] == "Always" {
			return nil, ErrObservation
		}
	}
	declared, _, _ := unstructured.NestedSlice(pod.Object, "spec", "containers")
	statuses, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	samples, _, _ := unstructured.NestedSlice(metrics.Object, "containers")
	if len(declared) == 0 || len(declared) > 64 || len(declared) != len(statuses) || len(declared) != len(samples) {
		return nil, ErrObservation
	}
	names := map[string]bool{}
	for _, item := range declared {
		entry, ok := item.(map[string]any)
		name, valid := entry["name"].(string)
		if !ok || !valid || name == "" || names[name] {
			return nil, ErrObservation
		}
		names[name] = true
	}
	identities := map[string]map[string]any{}
	for _, item := range statuses {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, ErrObservation
		}
		name, _, _ := unstructured.NestedString(entry, "name")
		id, _, _ := unstructured.NestedString(entry, "containerID")
		started, _, _ := unstructured.NestedString(entry, "state", "running", "startedAt")
		clock, e := time.Parse(time.RFC3339Nano, started)
		restarts, exists, re := unstructured.NestedInt64(entry, "restartCount")
		if !names[name] || identities[name] != nil || id == "" || e != nil || from.Before(clock) || clock.Before(pod.GetCreationTimestamp().Time) || !exists || re != nil || restarts < 0 {
			return nil, ErrObservation
		}
		identities[name] = map[string]any{"containerID": id, "restartCount": restarts, "startedAt": clock.UTC().Format(time.RFC3339Nano)}
	}
	result := []map[string]any{}
	seen := map[string]bool{}
	for _, item := range samples {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, ErrObservation
		}
		name, _, _ := unstructured.NestedString(entry, "name")
		if identities[name] == nil || seen[name] {
			return nil, ErrObservation
		}
		seen[name] = true
		usage := map[string]string{}
		for _, resource := range []string{"cpu", "memory"} {
			value, exists, e := unstructured.NestedString(entry, "usage", resource)
			quantity, qe := apiresource.ParseQuantity(value)
			if !exists || e != nil || qe != nil || quantity.Sign() < 0 {
				return nil, ErrObservation
			}
			usage[resource] = value
		}
		result = append(result, map[string]any{"name": name, "identity": identities[name], "usage": usage})
	}
	return json.Marshal(map[string]any{"nativeKind": "PodMetrics", "nativeUID": string(pod.GetUID()), "name": pod.GetName(), "namespace": pod.GetNamespace(), "podResourceVersion": pod.GetResourceVersion(), "nodeName": node.GetName(), "nodeUID": string(node.GetUID()), "nodeResourceVersion": node.GetResourceVersion(), "nodeCapacity": capacity, "nodeAllocatable": allocatable, "metricAPI": "metrics.k8s.io/v1beta1", "timestamp": stamp.UTC().Format(time.RFC3339Nano), "window": window, "observedFrom": from.UTC().Format(time.RFC3339Nano), "containers": result, "causalConfirmation": false})
}
