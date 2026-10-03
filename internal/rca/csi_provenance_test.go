package rca

import (
	"ops-platform/internal/evidence"
	"testing"
	"time"
)

func TestProvisioningFailureCannotConfirmUnidentifiedCSIController(t *testing.T) {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	recipe, _ := Builtin("pvc-csi-failure")
	es, g := frozenEvidence(recipe.Name, clock)
	for _, payload := range []string{`{"reason":"ProvisioningFailed"}`, `{"reason":"ProvisioningFailed","reportingController":"persistentvolume-controller"}`, `{"reason":"ProvisioningFailed","reportingController":"different.csi.example"}`} {
		copy := append([]evidence.Evidence(nil), es...)
		copy[1].Data = []byte(payload)
		copy[1].ContentDigest = evidence.Digest(copy[1].Data)
		out, err := Evaluate(recipe, Input{ResourceCanonicalID: es[0].ResourceCanonicalID, Evidence: copy, Graph: g, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock})
		if err != nil || out.Status == "confirmed" {
			t.Fatalf("unidentified/non-CSI controller confirmed: %+v %v", out, err)
		}
	}
}
