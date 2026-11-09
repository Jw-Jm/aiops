package investigation

import (
	"bytes"
	"encoding/json"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/rca"
	"strings"
	"testing"
)

func TestFormalRCASummaryDoesNotEmbedSnapshotPayloads(t *testing.T) {
	rev := rca.Revision{IncidentID: "incident", Revision: 811, InputDigest: "frozen-input",
		InputManifest: rca.Input{ResourceCanonicalID: "target", Evidence: []evidence.Evidence{{EvidenceID: "evidence", Data: json.RawMessage(`{"payload":"` + strings.Repeat("x", 200000) + `"}`)}}},
		Result:        rca.Result{Status: "inconclusive", Partial: true, RecipeVersion: "v1", RecipeDigest: "recipe", Bundle: rca.Bundle{Supporting: []string{"evidence"}, Missing: []string{"pod-state"}}}}
	raw, _ := json.Marshal(rev)
	digest := graph.ScopeDigest(raw)
	summary, err := formalRCASummary(rev, []string{"evidence"})
	if err != nil || len(summary) > 4096 || bytes.Contains(summary, []byte("payload")) {
		t.Fatalf("large snapshot leaked into model context: bytes=%d err=%v", len(summary), err)
	}
	var decoded struct {
		Schema   string `json:"schemaVersion"`
		ID       string `json:"incidentId"`
		Revision int64  `json:"revision"`
		Result   struct {
			Status   string
			Partial  bool
			Evidence struct{ Missing []string } `json:"evidenceBundle"`
		}
		Input struct {
			Target string   `json:"resourceCanonicalId"`
			Refs   []string `json:"evidenceRefs"`
		} `json:"inputManifest"`
	}
	if json.Unmarshal(summary, &decoded) != nil || decoded.Schema != "formal-rca-summary/v1" || decoded.ID != "incident" || decoded.Revision != 811 || decoded.Input.Target != "target" || len(decoded.Input.Refs) != 1 || decoded.Input.Refs[0] != "evidence" || decoded.Result.Status != "inconclusive" || !decoded.Result.Partial || len(decoded.Result.Evidence.Missing) != 1 {
		t.Fatalf("lost fixed identity, evidence or qualification: %s", summary)
	}
	after, _ := json.Marshal(rev)
	if graph.ScopeDigest(after) != digest {
		t.Fatal("summary changed the immutable reference content")
	}
}
