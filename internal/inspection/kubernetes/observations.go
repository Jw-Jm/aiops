package kubernetes

import (
	"encoding/json"
	"errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"time"
)

var ErrObservation = errors.New("official_inspection_observation_unavailable_or_invalid")

// Metrics are symptoms, never confirmation of causation. The fixed v1 policy
// reports usage strictly above 90% of the current Node's allocatable resource.
// NodeMetrics has no dependable Node UID: the caller must fence its native Node
// GET against the current identity before binding this name-based measurement.
func MetricSignals(node, metrics unstructured.Unstructured, observed time.Time) ([]Candidate, error) {
	if node.GetKind() != "Node" || node.GetAPIVersion() != "v1" || node.GetUID() == "" || node.GetName() == "" || metrics.GetKind() != "NodeMetrics" || metrics.GetAPIVersion() != "metrics.k8s.io/v1beta1" || metrics.GetName() != node.GetName() || metrics.GetNamespace() != "" || observed.IsZero() {
		return nil, ErrObservation
	}
	stamp, _, _ := unstructured.NestedString(metrics.Object, "timestamp")
	clock, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || !node.GetCreationTimestamp().Time.IsZero() && clock.Before(node.GetCreationTimestamp().Time) || clock.After(observed) || clock.Before(observed.Add(-5*time.Minute)) {
		return nil, ErrObservation
	}
	window, _, _ := unstructured.NestedString(metrics.Object, "window")
	duration, err := time.ParseDuration(window)
	if err != nil || duration <= 0 || duration > 5*time.Minute {
		return nil, ErrObservation
	}
	out := []Candidate{}
	for _, name := range []string{"cpu", "memory"} {
		usage, _, _ := unstructured.NestedString(metrics.Object, "usage", name)
		capacity, _, _ := unstructured.NestedString(node.Object, "status", "allocatable", name)
		used, err := apiresource.ParseQuantity(usage)
		if err != nil || used.Sign() < 0 {
			return nil, ErrObservation
		}
		cap, err := apiresource.ParseQuantity(capacity)
		if err != nil || cap.Sign() <= 0 {
			return nil, ErrObservation
		}
		// Exact decimal comparison avoids rounding a nanocore boundary upward.
		lhs, rhs := used.AsDec(), cap.AsDec()
		lhs.Mul(lhs, apiresource.NewQuantity(10, apiresource.DecimalSI).AsDec())
		rhs.Mul(rhs, apiresource.NewQuantity(9, apiresource.DecimalSI).AsDec())
		state := "resolved"
		if lhs.Cmp(rhs) > 0 {
			state = "firing"
		}
		symptom := "CPUUtilizationHigh"
		if name == "memory" {
			symptom = "MemoryUtilizationHigh"
		}
		data, _ := json.Marshal(map[string]any{"reason": symptom, "nativeKind": "NodeMetrics", "nativeUID": string(node.GetUID()), "nodeResourceVersion": node.GetResourceVersion(), "metricAPI": "metrics.k8s.io/v1beta1", "timestamp": clock.UTC().Format(time.RFC3339Nano), "window": window, "resource": name, "usage": usage, "allocatable": capacity, "policy": "utilization-over-90-percent/v1", "causalConfirmation": false})
		out = append(out, Candidate{RuleID: "kubernetes/" + symptom + "/v1", RuleFamily: "node", NormalizedSymptom: symptom, ResourceKind: "Node", ResourceUID: string(node.GetUID()), State: state, ObservedAt: clock, StartsAt: clock.Add(-duration), TimeReliable: true, Object: metrics, NativeData: data})
	}
	return out, nil
}

// This observation is explicitly relative to the configured collector. It does
// not claim a global control-plane outage. Auth, TLS, DNS and local rate-budget
// failures cannot create or resolve this symptom.
func ControlPlaneSignals(node unstructured.Unstructured, status int, errorClass string, observed time.Time) ([]Candidate, error) {
	if node.GetKind() != "Node" || node.GetUID() == "" || node.GetName() == "" || observed.IsZero() {
		return nil, ErrObservation
	}
	state := ""
	if errorClass == "" && status == 200 {
		state = "resolved"
	} else if errorClass == "" && (status == 503 || status == 504) || status == 0 && (errorClass == "dial_refused" || errorClass == "request_timeout") {
		state = "firing"
	} else {
		return nil, ErrObservation
	}
	data, _ := json.Marshal(map[string]any{"reason": "ControlPlaneUnreachable", "probe": "GET /version", "observer": "bound-collector", "httpStatus": status, "errorClass": errorClass, "causalConfirmation": false})
	return []Candidate{{RuleID: "kubernetes/ControlPlaneUnreachable/v1", RuleFamily: "control-plane", NormalizedSymptom: "ControlPlaneUnreachable", ResourceKind: "Node", ResourceUID: string(node.GetUID()), State: state, ObservedAt: observed, StartsAt: observed, TimeReliable: true, Object: node, NativeData: data}}, nil
}
