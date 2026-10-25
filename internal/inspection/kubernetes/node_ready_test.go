package kubernetes

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// firingFor reports whether the signals contain an open fault for the symptom.
func firingFor(candidates []Candidate, symptom string) bool {
	for _, s := range candidates {
		if s.NormalizedSymptom == symptom && s.State == "firing" {
			return true
		}
	}
	return false
}

// nodeWithReady builds an official Node with one Ready condition.
func nodeWithReady(uid, status string, observed time.Time, resourceVersion string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]any{
			"uid": uid, "name": "node-a", "resourceVersion": resourceVersion,
			"creationTimestamp": "2026-10-01T00:00:00Z",
			"annotations":       map[string]any{"ops.internal/observed-at": observed.Format(time.RFC3339Nano)},
		},
		"status": map[string]any{
			"conditions": []any{map[string]any{
				"type": "Ready", "status": status, "reason": "KubeletReady",
				"lastTransitionTime": observed.Format(time.RFC3339Nano),
			}},
		},
	}}
}

func signalFor(t *testing.T, candidates []Candidate, symptom string) Candidate {
	t.Helper()
	for _, s := range candidates {
		if s.NormalizedSymptom == symptom {
			return s
		}
	}
	t.Fatalf("no %s signal in %+v", symptom, candidates)
	return Candidate{}
}

// A node whose readiness is unknown is not healthy. When the same node is
// observed ready again, both the NotReady and the NodeReadyUnknown occurrence of
// that node are explicitly resolved, so the unknown fault can settle instead of
// staying firing forever.
func TestNodeReadyUnknownSettlesWhenTheNodeBecomesReady(t *testing.T) {
	observed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	unknown := Signals(nodeWithReady("uid-1", "Unknown", observed, "10"))
	unknownSignal := signalFor(t, unknown, "NodeReadyUnknown")
	if unknownSignal.State != "firing" {
		t.Fatalf("unknown readiness state = %s", unknownSignal.State)
	}
	if firingFor(unknown, "NotReady") {
		t.Fatal("an unknown node must not be reported as a not-ready fault")
	}

	ready := Signals(nodeWithReady("uid-1", "True", observed.Add(time.Minute), "11"))
	readyUnknown := signalFor(t, ready, "NodeReadyUnknown")
	if readyUnknown.State != "resolved" {
		t.Fatalf("the unknown occurrence was not settled: %s", readyUnknown.State)
	}
	if readyUnknown.SignalKey() != unknownSignal.SignalKey() {
		t.Fatalf("the resolution does not keep the occurrence identity: %s vs %s", readyUnknown.SignalKey(), unknownSignal.SignalKey())
	}
	if signalFor(t, ready, "NotReady").State != "resolved" {
		t.Fatal("the not-ready occurrence was not settled")
	}
	// A repeated healthy observation stays a resolution and does not create a new
	// fault.
	again := Signals(nodeWithReady("uid-1", "True", observed.Add(2*time.Minute), "12"))
	if signalFor(t, again, "NodeReadyUnknown").State != "resolved" {
		t.Fatal("a repeated healthy sample changed the settlement")
	}
}

// False -> Unknown -> True keeps both rule identities: the not-ready fault is
// settled by the healthy sample, and the unknown fault that replaced it is
// settled as well.
func TestNodeReadyFalseUnknownTrueKeepsBothOccurrences(t *testing.T) {
	observed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	notReady := Signals(nodeWithReady("uid-2", "False", observed, "20"))
	if signalFor(t, notReady, "NotReady").State != "firing" {
		t.Fatal("a not-ready node must raise the not-ready fault")
	}
	if firingFor(notReady, "NodeReadyUnknown") {
		t.Fatal("a not-ready node must not report an unknown readiness fault")
	}
	unknown := Signals(nodeWithReady("uid-2", "Unknown", observed.Add(time.Minute), "21"))
	if signalFor(t, unknown, "NodeReadyUnknown").State != "firing" {
		t.Fatal("the unknown readiness fault was not raised")
	}
	ready := Signals(nodeWithReady("uid-2", "True", observed.Add(2*time.Minute), "22"))
	if signalFor(t, ready, "NodeReadyUnknown").State != "resolved" {
		t.Fatal("the unknown readiness fault was not settled by the healthy sample")
	}
	if signalFor(t, ready, "NotReady").State != "resolved" {
		t.Fatal("the not-ready fault was not settled by the healthy sample")
	}
}

// A replaced node is a different resource: its occurrence identity changes, so a
// healthy sample of the new node can never settle the old node's unknown fault.
func TestNodeReadyUnknownIdentityFollowsTheNodeUID(t *testing.T) {
	observed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	old := signalFor(t, Signals(nodeWithReady("uid-old", "Unknown", observed, "30")), "NodeReadyUnknown")
	replaced := signalFor(t, Signals(nodeWithReady("uid-new", "Unknown", observed, "31")), "NodeReadyUnknown")
	if old.SignalKey() == replaced.SignalKey() {
		t.Fatal("a replaced node kept the occurrence identity of the old node")
	}
	healthyReplacement := signalFor(t, Signals(nodeWithReady("uid-new", "True", observed.Add(time.Minute), "32")), "NodeReadyUnknown")
	if healthyReplacement.SignalKey() != replaced.SignalKey() {
		t.Fatal("the healthy sample does not address the current node")
	}
	if healthyReplacement.SignalKey() == old.SignalKey() {
		t.Fatal("the healthy sample of the replacement settled the old node")
	}
}
