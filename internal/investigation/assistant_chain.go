package investigation

import (
	"encoding/json"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

type ChainLink struct {
	From            string   `json:"from"`
	To              string   `json:"to"`
	Kind            string   `json:"kind"`
	RelationDigest  string   `json:"relationDigest"`
	Assessment      string   `json:"assessment"`
	Supports        []string `json:"supports"`
	Conflicts       []string `json:"conflicts"`
	MissingEvidence []string `json:"missingEvidence"`
	NextChecks      []string `json:"nextChecks"`
}

func committedAssistantRelations(tool string, data json.RawMessage) map[string]resource.Relation {
	out := map[string]resource.Relation{}
	if tool != "get_dependencies" && tool != "get_impact_scope" && tool != "get_resource_context" && tool != "resource_history" {
		return out
	}
	var result struct {
		Edges     []resource.Relation `json:"edges"`
		Relations []resource.Relation `json:"relations"`
	}
	if json.Unmarshal(data, &result) != nil {
		return out
	}
	for _, r := range append(result.Edges, result.Relations...) {
		if r.From == "" || r.To == "" || r.Kind == "" || r.ObservedAt.IsZero() || r.Provenance.SourceRegistrationID == "" {
			continue
		}
		out[graph.ScopeDigest(r)] = r
	}
	return out
}
func validateSupportConflict(supports, conflicts []string, available map[string]bool) error {
	seen := map[string]bool{}
	for _, list := range [][]string{supports, conflicts} {
		for _, ref := range list {
			if !available[ref] || seen[ref] {
				return ErrDenied
			}
			seen[ref] = true
		}
	}
	return nil
}
func validateFaultChain(a AssistantAnswer, relations map[string]resource.Relation, evidence map[string]bool) error {
	if len(a.FaultChain) > 0 && a.AnswerKind != "diagnosis" {
		return ErrInvalid
	}
	entities := map[string]bool{}
	for _, id := range a.EntityRefs {
		entities[id] = true
	}
	seen := map[string]bool{}
	for _, link := range a.FaultChain {
		r, ok := relations[link.RelationDigest]
		if !ok || seen[link.RelationDigest] || r.From != link.From || r.To != link.To || r.Kind != link.Kind || !entities[link.From] || !entities[link.To] || link.Assessment != "hypothesis" || len(link.MissingEvidence) == 0 {
			return ErrDenied
		}
		seen[link.RelationDigest] = true
		if validateSupportConflict(link.Supports, link.Conflicts, evidence) != nil {
			return ErrDenied
		}
	}
	return nil
}
