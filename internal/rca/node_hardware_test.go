package rca

import (
	"encoding/json"
	"ops-platform/internal/evidence"
	"ops-platform/internal/resource"
	"regexp"
	"slices"
	"testing"
	"time"
)

// Expectations are specified before the alternative hardware predicate exists.
// Native CPER supplies a DIMM location in one atomic multiline record. A health
// alarm, a recent change, or the hardware path alone never proves Node causation.
func TestNodeNotReadySelectsHardwareUpstreamAndRejectsCompetingCauses(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	nodeEvidence, nodeGraph := frozenEvidence("node-failure", clock)
	dimmEvidence, dimmGraph := frozenEvidence("dimm-failure", clock)
	node := nodeEvidence[0].ResourceCanonicalID
	dimm := dimmEvidence[0].ResourceCanonicalID
	for i := range dimmGraph.Edges {
		if dimmGraph.Edges[i].Kind == "hosts" || dimmGraph.Edges[i].Kind == "scheduled_on" {
			if dimmGraph.Edges[i].To != dimm {
				dimmGraph.Edges[i].To = node
			}
		}
	}
	for i := range dimmGraph.Nodes {
		if dimmGraph.Nodes[i].Kind == "Node" {
			dimmGraph.Nodes[i].CanonicalID = node
		}
		if dimmGraph.Nodes[i].Kind == "DIMM" {
			dimmGraph.Nodes[i].Attributes = map[string]any{"DeviceLocator": "DIMM_A1"}
		}
	}
	for i := range dimmEvidence {
		dimmEvidence[i].EvidenceID = "hardware-" + dimmEvidence[i].EvidenceID
		dimmEvidence[i].IndependenceGroup = "hardware-" + dimmEvidence[i].IndependenceGroup
	}
	cper := nodeEvidence[1]
	cper.EvidenceID = "native-cper"
	cper.SourceSystem = "victorialogs"
	cper.Type = "log"
	cper.QueryTemplateVersion = "node-kernel-logs/v1"
	cper.IndependenceGroup = "native-cper"
	setCPER := func(message string) evidence.Evidence {
		e := cper
		e.Data, _ = json.Marshal([]map[string]string{{"_TRANSPORT": "kernel", "_time": clock.Format(time.RFC3339Nano), "_msg": message}})
		e.ContentDigest = evidence.Digest(e.Data)
		return e
	}
	cper = setCPER("[Hardware Error]: event severity: fatal\n[Hardware Error]: Error 0, type: fatal\n[Hardware Error]: section_type: memory error\n[Hardware Error]: DIMM location: BANK_0 DIMM_A1")
	input := Input{ResourceCanonicalID: node, Evidence: append([]evidence.Evidence{nodeEvidence[0], cper}, dimmEvidence...), Graph: dimmGraph, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock}
	recipe, _ := Builtin("node-failure")
	out, err := Evaluate(recipe, input)
	if err != nil || out.Status != "confirmed" || !slices.ContainsFunc(out.Candidates, func(c Candidate) bool { return c.Type == "dimm_hardware_fault" && c.ResourceCanonicalID == dimm }) {
		t.Fatalf("native hardware causal chain not selected: %+v %v graph=%+v", out, err, nodeGraph)
	}
	input.Evidence = append(input.Evidence, nodeEvidence[1])
	conflicting, err := Evaluate(recipe, input)
	if err != nil || conflicting.Status != "unresolved" || !conflicting.Partial || len(conflicting.Candidates) != 2 || len(conflicting.Bundle.Conflicts) == 0 {
		t.Fatalf("competing runtime/hardware causes silently confirmed: %+v %v", conflicting, err)
	}
	input.Evidence = input.Evidence[:len(input.Evidence)-1]
	for _, mode := range []string{"location-mismatch", "no-causal-record", "required-source-degraded", "missing-ecc", "disconnected"} {
		t.Run(mode, func(t *testing.T) {
			bad := input
			bad.Evidence = slices.Clone(input.Evidence)
			bad.Graph.Edges = slices.Clone(input.Graph.Edges)
			switch mode {
			case "location-mismatch":
				bad.Evidence[1] = setCPER("[Hardware Error]: DIMM location: BANK_0 DIMM_B2\n[Hardware Error]: event severity: fatal")
			case "no-causal-record":
				bad.Evidence[1] = setCPER("[Hardware Error]: event severity: fatal")
			case "required-source-degraded":
				bad.Graph.Partial = true
				bad.Graph.DegradedSources = []string{"redfish"}
			case "missing-ecc":
				bad.Evidence = bad.Evidence[:3]
			case "disconnected":
				bad.Graph.Edges = slices.DeleteFunc(bad.Graph.Edges, func(e resource.Relation) bool { return e.Kind == "hosts" })
			}
			got, err := Evaluate(recipe, bad)
			if err != nil || got.Status == "confirmed" {
				t.Fatalf("insufficient causal evidence confirmed: %+v %v", got, err)
			}
		})
	}
}

func TestNodeRecipeCannotConfirmKernelCauseFromKubernetesEventReason(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	es, g := frozenEvidence("node-failure", clock)
	es[1].SourceSystem = "kubernetes"
	es[1].Type = "event"
	es[1].QueryTemplateVersion = "sp05-signal/v1"
	es[1].Data = []byte(`{"reason":"KernelOops"}`)
	es[1].ContentDigest = evidence.Digest(es[1].Data)
	recipe, _ := Builtin("node-failure")
	out, err := Evaluate(recipe, Input{ResourceCanonicalID: es[0].ResourceCanonicalID, Evidence: es, Graph: g, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock})
	if err != nil || out.Status == "confirmed" {
		t.Fatalf("caller-written native Event reason became causal proof: %+v %v", out, err)
	}
}

func TestNodeRecipeHistoricalV1RemainsExplicitlyExecutable(t *testing.T) {
	r, err := BuiltinVersion("node-failure", "v1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	decoded, err := DecodeRecipe(raw)
	if err != nil || decoded.Version != "v1" {
		t.Fatalf("historical version unavailable: %v", err)
	}
	if latest, _ := Builtin("node-failure"); latest.Version != "v2" {
		t.Fatal("latest executable version not advanced")
	}
}

func TestFatalRecordDoesNotConfirmCorrectedDIMMSection(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	es, g := frozenEvidence("node-failure", clock)
	e := es[1]
	e.Data, _ = json.Marshal([]map[string]string{{"_TRANSPORT": "kernel", "_time": clock.Format(time.RFC3339Nano), "_msg": "[Hardware Error]: Error 0, type: corrected\n[Hardware Error]: section_type: memory error\n[Hardware Error]: DIMM location: BANK_0 DIMM_A1\n[Hardware Error]: Error 1, type: fatal\n[Hardware Error]: section_type: PCIe error\n[Hardware Error]: event severity: fatal"}})
	e.ContentDigest = evidence.Digest(e.Data)
	input := Input{ResourceCanonicalID: es[0].ResourceCanonicalID, Graph: g, Evidence: []evidence.Evidence{e}, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock}
	if fatalDIMMRecord(e, input, "DIMM_A1") {
		t.Fatal("fatal PCIe event with corrected memory section confirmed DIMM causation")
	}
}

func TestMalformedCPERBoundaryCannotReusePreviousFatalMemorySection(t *testing.T) {
	location := regexp.MustCompile(`DIMM location: BANK_0 DIMM_A1$`)
	for _, boundary := range []string{"Error 1, type: unknown", "Error 1, type: fatal malformed", "section_type: PCIe error"} {
		message := "[Hardware Error]: Error 0, type: fatal\n[Hardware Error]: section_type: memory error\n[Hardware Error]: " + boundary + "\n[Hardware Error]: DIMM location: BANK_0 DIMM_A1"
		if locatedFatalMemorySection(message, location) {
			t.Fatalf("unrecognized section reused fatal memory state: %s", boundary)
		}
	}
}
