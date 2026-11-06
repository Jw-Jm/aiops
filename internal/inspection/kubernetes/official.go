package kubernetes

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/finding"
	"ops-platform/internal/resource"
	"strconv"
	"strings"
	"time"
)

// Candidate is a projection of an official status/Condition/Event, not a
// separately inferred root cause. Worker alone binds source and tenant identity.
type Candidate struct {
	NativeData        json.RawMessage           `json:"-"`
	RuleID            string                    `json:"ruleId"`
	RuleFamily        string                    `json:"ruleFamily"`
	NormalizedSymptom string                    `json:"normalizedSymptom"`
	ResourceKind      string                    `json:"resourceKind"`
	ResourceUID       string                    `json:"resourceUid"`
	Namespace         string                    `json:"namespace"`
	State             string                    `json:"lifecycleState"`
	StartsAt          time.Time                 `json:"startsAt"`
	ObservedAt        time.Time                 `json:"observedAt"`
	SourceSequence    int64                     `json:"sourceSequence"`
	TimeReliable      bool                      `json:"timeReliable"`
	Object            unstructured.Unstructured `json:"-"`
}

func Inspect(o unstructured.Unstructured) []Candidate {
	out := []Candidate{}
	for _, c := range Signals(o) {
		if c.State == "firing" {
			out = append(out, c)
		}
	}
	return out
}
func Signals(o unstructured.Unstructured) []Candidate {
	out := []Candidate{}
	sequence, _ := strconv.ParseInt(o.GetResourceVersion(), 10, 64)
	observed, _ := time.Parse(time.RFC3339Nano, o.GetAnnotations()["ops.internal/observed-at"])
	add := func(family, symptom, kind, uid, ns, state string, start time.Time) {
		if start.IsZero() {
			start = o.GetCreationTimestamp().Time
		}
		out = append(out, Candidate{RuleID: "kubernetes/" + symptom + "/v1", RuleFamily: family, NormalizedSymptom: symptom, ResourceKind: kind, ResourceUID: uid, Namespace: ns, State: state, StartsAt: start, ObservedAt: observed, SourceSequence: sequence, TimeReliable: !start.IsZero(), Object: o})
	}
	conditions, _, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
	for _, item := range conditions {
		c, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := c["type"].(string)
		status, _ := c["status"].(string)
		reason, _ := c["reason"].(string)
		ts, _ := c["lastTransitionTime"].(string)
		start, _ := time.Parse(time.RFC3339Nano, ts)
		if o.GetKind() == "Node" {
			switch kind {
			case "Ready":
				if status == "False" {
					add("node", "NotReady", "Node", string(o.GetUID()), "", "firing", start)
				} else if status == "True" {
					// A confirmed healthy node closes both the NotReady and the
					// NodeReadyUnknown occurrence of the same node. The unknown
					// occurrence keeps its own rule identity, so it is settled by
					// an explicit resolution of that rule and not by the absence of
					// a new unknown sample. An unknown node is never healthy.
					add("node", "NotReady", "Node", string(o.GetUID()), "", "resolved", start)
					add("node", "NodeReadyUnknown", "Node", string(o.GetUID()), "", "resolved", start)
				} else if status == "Unknown" {
					add("node", "NodeReadyUnknown", "Node", string(o.GetUID()), "", "firing", start)
				}
			case "MemoryPressure", "DiskPressure", "PIDPressure":
				if status == "True" {
					add("node", kind, "Node", string(o.GetUID()), "", "firing", start)
				} else if status == "False" {
					add("node", kind, "Node", string(o.GetUID()), "", "resolved", start)
				}
			}
		}
		if o.GetKind() == "Pod" && kind == "PodScheduled" {
			if status == "False" && reason == "Unschedulable" {
				add("scheduling", "Unschedulable", "Pod", string(o.GetUID()), o.GetNamespace(), "firing", start)
			} else if status == "True" {
				add("scheduling", "Unschedulable", "Pod", string(o.GetUID()), o.GetNamespace(), "resolved", start)
			}
		}
	}
	if o.GetKind() == "PersistentVolumeClaim" {
		phase, _, _ := unstructured.NestedString(o.Object, "status", "phase")
		if phase == "Pending" {
			add("storage", "PVCUnbound", o.GetKind(), string(o.GetUID()), o.GetNamespace(), "firing", o.GetCreationTimestamp().Time)
		} else if phase == "Bound" {
			add("storage", "PVCUnbound", o.GetKind(), string(o.GetUID()), o.GetNamespace(), "resolved", o.GetCreationTimestamp().Time)
		}
	}
	if o.GetKind() == "Pod" {
		pulling, running := false, false
		for _, raw := range containerStates(o) {
			state, _ := raw["state"].(map[string]any)
			if wait, _ := state["waiting"].(map[string]any); wait != nil {
				reason, _ := wait["reason"].(string)
				if reason == "ErrImagePull" || reason == "ImagePullBackOff" || reason == "ErrImageNeverPull" {
					pulling = true
				}
			}
			if _, ok := state["running"].(map[string]any); ok && raw["ready"] == true {
				running = true
			}
		}
		if pulling {
			add("image", "ImagePullFailure", "Pod", string(o.GetUID()), o.GetNamespace(), "firing", o.GetCreationTimestamp().Time)
		} else if running {
			// The running container also settles the kubelet pull-error
			// occurrence: both symptoms describe the same pull condition.
			add("image", "ImagePullFailure", "Pod", string(o.GetUID()), o.GetNamespace(), "resolved", o.GetCreationTimestamp().Time)
			add("image", "ImagePullError", "Pod", string(o.GetUID()), o.GetNamespace(), "resolved", o.GetCreationTimestamp().Time)
		}
	}
	if o.GetKind() == "EndpointSlice" {
		if service := owningServiceUID(o); service != "" {
			ready := 0
			endpoints, _, _ := unstructured.NestedSlice(o.Object, "endpoints")
			for _, raw := range endpoints {
				endpoint, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				conditions, _ := endpoint["conditions"].(map[string]any)
				if unready, ok := conditions["ready"].(bool); ok && !unready {
					continue
				}
				ready++
			}
			state := "firing"
			if ready > 0 {
				state = "resolved"
			}
			// shortcut: one occurrence per Service; multi-slice flapping
			// (one full, one empty slice) is not aggregated, upgrade when
			// services with >100 or topology-partitioned endpoints appear.
			add("network", "ServiceNoEndpoints", "Service", service, o.GetNamespace(), state, o.GetCreationTimestamp().Time)
		}
	}
	if o.GetKind() == "Event" {
		reason, _, _ := unstructured.NestedString(o.Object, "reason")
		eventType, _, _ := unstructured.NestedString(o.Object, "type")
		kind, _, _ := unstructured.NestedString(o.Object, "involvedObject", "kind")
		uid, _, _ := unstructured.NestedString(o.Object, "involvedObject", "uid")
		ns, _, _ := unstructured.NestedString(o.Object, "involvedObject", "namespace")
		ts, _, _ := unstructured.NestedString(o.Object, "firstTimestamp")
		start, _ := time.Parse(time.RFC3339Nano, ts)
		if start.IsZero() {
			ts, _, _ = unstructured.NestedString(o.Object, "lastTimestamp")
			start, _ = time.Parse(time.RFC3339Nano, ts)
		}
		if eventType == "Warning" && (reason == "FailedMount" || reason == "FailedAttachVolume" || reason == "ProvisioningFailed" || reason == "FailedScheduling") {
			if uid != "" {
				family := "storage"
				if reason == "FailedScheduling" {
					family = "scheduling"
				}
				add(family, reason, kind, uid, ns, "firing", start)
			}
		}
		// Kubelet reports pull failures as generic Failed/BackOff events; the
		// bounded message is the only pull-specific discriminator.
		if eventType == "Warning" && kind == "Pod" && uid != "" {
			reporter, _, _ := unstructured.NestedString(o.Object, "reportingComponent")
			if reporter == "" {
				reporter, _, _ = unstructured.NestedString(o.Object, "source", "component")
			}
			message, _, _ := unstructured.NestedString(o.Object, "message")
			if reporter == "kubelet" && (strings.Contains(message, "pull image") || strings.Contains(message, "ErrImagePull") || strings.Contains(message, "ImagePullBackOff") || strings.Contains(message, "pulling image")) {
				add("image", "ImagePullError", "Pod", uid, ns, "firing", start)
			}
		}
	}
	return out
}
func containerStates(o unstructured.Unstructured) []map[string]any {
	states := []map[string]any{}
	for _, field := range []string{"containerStatuses", "initContainerStatuses"} {
		statuses, _, _ := unstructured.NestedSlice(o.Object, "status", field)
		for _, raw := range statuses {
			if m, ok := raw.(map[string]any); ok {
				states = append(states, m)
			}
		}
	}
	return states
}
func owningServiceUID(o unstructured.Unstructured) string {
	for _, owner := range o.GetOwnerReferences() {
		if owner.Kind == "Service" && owner.UID != "" {
			return string(owner.UID)
		}
	}
	return ""
}
func (c Candidate) CanonicalID(tenant, cluster string) string {
	return resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: cluster, APIGroup: "core", Kind: c.ResourceKind, StableID: c.ResourceUID}.String()
}
func (c Candidate) SignalKey() string {
	return finding.Hash([]string{c.ResourceKind, c.ResourceUID, c.RuleID})
}
