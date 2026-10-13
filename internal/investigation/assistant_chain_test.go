package investigation

import (
	"encoding/json"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestFaultChainUsesOnlyCommittedRelationsAndDisjointEvidence(t *testing.T) {
	r := resource.Relation{From: "a", To: "b", Kind: "depends_on", ObservedAt: time.Now().UTC(), Provenance: resource.Provenance{SourceRegistrationID: "source"}}
	data, _ := json.Marshal(map[string]any{"edges": []resource.Relation{r}, "attributes": map[string]any{"edges": []resource.Relation{r}}})
	relations := committedAssistantRelations("get_dependencies", data)
	if len(relations) != 1 || len(committedAssistantRelations("query_logs", data)) != 0 {
		t.Fatal("untrusted data became relation")
	}
	link := ChainLink{From: "a", To: "b", Kind: r.Kind, RelationDigest: graph.ScopeDigest(r), Assessment: "hypothesis", Supports: []string{"e"}, Conflicts: []string{}, MissingEvidence: []string{"因果仍需验证"}, NextChecks: []string{}}
	answer := AssistantAnswer{AnswerKind: "diagnosis", EntityRefs: []string{"a", "b"}, FaultChain: []ChainLink{link}}
	if validateFaultChain(answer, relations, map[string]bool{"e": true}) != nil {
		t.Fatal("valid observation rejected")
	}
	for _, change := range []string{"invented_edge", "confirmed", "self_conflict", "missing"} {
		bad := answer
		bad.FaultChain = append([]ChainLink{}, answer.FaultChain...)
		switch change {
		case "invented_edge":
			bad.FaultChain[0].To = "c"
		case "confirmed":
			bad.FaultChain[0].Assessment = "confirmed"
		case "self_conflict":
			bad.FaultChain[0].Conflicts = []string{"e"}
		case "missing":
			bad.FaultChain[0].MissingEvidence = nil
		}
		if validateFaultChain(bad, relations, map[string]bool{"e": true}) == nil {
			t.Fatal(change)
		}
	}
}

func TestHistoryReferencesDoNotBecomeFreshByLoadingContext(t *testing.T) {
	old, now := time.Now().Add(-time.Hour).UTC(), time.Now().UTC()
	a := AssistantAnswer{EntityRefs: []string{"observed"}}
	if !assistantObservationTime(a, map[string]bool{}, old, now).Equal(old) {
		t.Fatal("history became fresh")
	}
	if !assistantObservationTime(a, map[string]bool{"observed": true}, old, now).Equal(now) {
		t.Fatal("fresh tool read ignored")
	}
}
