package app

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"testing"
	"time"
)

func TestInitialInspectionWaitsForHealthWithoutRepublishingFacts(t *testing.T) {
	publications, inspections := 0, 0
	sink := withSP05SnapshotInspection(func(s kubernetes.Snapshot) error {
		if !s.ObservationOnly {
			publications++
		}
		return nil
	}, func(s kubernetes.Snapshot) error {
		// The actual inspector admits authoritative facts only once the initial
		// watch has completed, and cannot derive findings from an empty health
		// receipt. This exercises the production delivery boundary.
		if !s.ObservationOnly && s.State.LastError == "" && len(s.Objects) == 1 {
			inspections++
		}
		return nil
	})
	gvr := kubernetes.GVR{Version: "v1", Resource: "nodes"}
	state := kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, LastError: "watch_initializing"}
	if err := sink(kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{{Object: map[string]any{"kind": "Node", "metadata": map[string]any{"uid": "node-1"}}}}, State: state}); err != nil {
		t.Fatal(err)
	}
	if inspections != 0 {
		t.Fatal("initializing facts inspected")
	}
	state.WatchContinuous = true
	state.LastError = ""
	for range 3 {
		if err := sink(kubernetes.Snapshot{GVR: gvr, ObservationOnly: true, State: state}); err != nil {
			t.Fatal(err)
		}
	}
	if publications != 1 || inspections != 1 {
		t.Fatalf("initial fact delivery: publications=%d inspections=%d; expected one each", publications, inspections)
	}
}
