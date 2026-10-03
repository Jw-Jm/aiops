package kubernetes

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/finding"
	"ops-platform/internal/resource"
	"strconv"
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
					add("node", "NotReady", "Node", string(o.GetUID()), "", "resolved", start)
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
	if o.GetKind() == "Event" {
		reason, _, _ := unstructured.NestedString(o.Object, "reason")
		eventType, _, _ := unstructured.NestedString(o.Object, "type")
		if eventType == "Warning" && (reason == "FailedMount" || reason == "FailedAttachVolume" || reason == "ProvisioningFailed" || reason == "FailedScheduling") {
			kind, _, _ := unstructured.NestedString(o.Object, "involvedObject", "kind")
			uid, _, _ := unstructured.NestedString(o.Object, "involvedObject", "uid")
			ns, _, _ := unstructured.NestedString(o.Object, "involvedObject", "namespace")
			ts, _, _ := unstructured.NestedString(o.Object, "firstTimestamp")
			start, _ := time.Parse(time.RFC3339Nano, ts)
			if start.IsZero() {
				ts, _, _ = unstructured.NestedString(o.Object, "lastTimestamp")
				start, _ = time.Parse(time.RFC3339Nano, ts)
			}
			if uid != "" {
				family := "storage"
				if reason == "FailedScheduling" {
					family = "scheduling"
				}
				add(family, reason, kind, uid, ns, "firing", start)
			}
		}
	}
	return out
}
func (c Candidate) CanonicalID(tenant, cluster string) string {
	return resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: cluster, APIGroup: "core", Kind: c.ResourceKind, StableID: c.ResourceUID}.String()
}
func (c Candidate) SignalKey() string {
	return finding.Hash([]string{c.ResourceKind, c.ResourceUID, c.RuleID})
}
