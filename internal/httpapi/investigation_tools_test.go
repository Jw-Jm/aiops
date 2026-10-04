package httpapi

import (
	"encoding/json"
	"ops-platform/internal/evidence"
	"ops-platform/internal/investigation/tools"
	"testing"
)

func TestHardwareToolProjectsRequestedComponentWithoutChangingEvidence(t *testing.T) {
	fact := json.RawMessage(`{"health":"critical","entities":[{"kind":"DIMM","name":"dimm-a"},{"kind":"CPU","name":"cpu-a"}]}`)
	out := tools.Result{Data: map[string]any{}}
	hardwareProjection(&out, "memory", []evidence.Evidence{{EvidenceID: "source-ref", FactSlice: fact}})
	b, _ := json.Marshal(out.Data)
	if string(b) != `{"component":"memory","facts":[{"kind":"DIMM","name":"dimm-a"}]}` {
		t.Fatalf("requested hardware projection: %s", b)
	}
	if string(fact) != `{"health":"critical","entities":[{"kind":"DIMM","name":"dimm-a"},{"kind":"CPU","name":"cpu-a"}]}` {
		t.Fatal("immutable source facts changed")
	}
	out = tools.Result{}
	hardwareProjection(&out, "fan", []evidence.Evidence{{FactSlice: fact}})
	if !out.Partial || out.State != "partial" {
		t.Fatal("missing requested component claimed complete")
	}
}
