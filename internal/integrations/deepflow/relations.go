package deepflow

import (
	"encoding/json"
	"ops-platform/internal/evidence"
	"ops-platform/internal/resource"
	"time"
)

// NetworkRelations maps only already-scoped source observations. Observation
// time is preserved from Querier rows; executing or replaying a query never
// renews an edge. The graph adapter owns the bounded TTL overlay.
func NetworkRelations(result evidence.Result) ([]resource.Relation, error) {
	edges := []resource.Relation{}
	for _, fact := range result.Evidence {
		var rows []struct {
			From       string    `json:"from"`
			To         string    `json:"to"`
			ObservedAt time.Time `json:"observedAt"`
		}
		if json.Unmarshal(fact.Data, &rows) != nil {
			return nil, evidence.ErrArgument
		}
		for _, row := range rows {
			if row.ObservedAt.IsZero() {
				return nil, evidence.ErrArgument
			}
			edges = append(edges, resource.Relation{From: row.From, To: row.To, Kind: "communicates_with", TTLSeconds: 300, ObservedAt: row.ObservedAt, ValidFrom: row.ObservedAt, Confidence: 1, Provenance: resource.Provenance{SourceRegistrationID: fact.SourceRegistrationID, RuleVersion: fact.QueryTemplateVersion, ObservedAt: row.ObservedAt}})
		}
	}
	return edges, nil
}
