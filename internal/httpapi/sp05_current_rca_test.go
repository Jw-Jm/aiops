package httpapi

import (
	"ops-platform/internal/incident"
	"ops-platform/internal/rca"
	"testing"
)

func TestFrozenRCAIsNotCurrentWithoutRuntimeProof(t *testing.T) {
	out := currentRCAView(incident.Incident{Revision: 1}, rca.Revision{BaseIncidentRevision: 1}).(map[string]any)
	if out["currentEligible"] == true {
		t.Fatal("fresh-looking frozen revision claimed current eligibility without Graph/Recipe runtime proof")
	}
}
