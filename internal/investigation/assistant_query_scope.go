package investigation

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
)

// AssistantQueryScopeFilterV1 is the public projection of one persisted intent
// filter. It carries only the field, operator and value the product already
// accepts for intent filters; no grant, plan or model data is included.
type AssistantQueryScopeFilterV1 struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// AssistantQueryScopeV1 describes the scope a stored query result was produced
// under. It is derived only from the already authorized persisted scope of that
// query result and from the committed execute_analysis_query step of the same
// turn, so a drill-down can reproduce the submitted query instead of a broader
// list.
type AssistantQueryScopeV1 struct {
	ClusterUID    string                        `json:"clusterUid"`
	Namespaces    []string                      `json:"namespaces"`
	CanonicalIDs  []string                      `json:"canonicalIds"`
	ClusterScoped bool                          `json:"clusterScoped"`
	Filters       []AssistantQueryScopeFilterV1 `json:"filters"`
	// FiltersKnown tells the reader whether Filters is the real filter set of
	// the submitted query or only the absence of a recoverable descriptor. An
	// empty filter list of an older record is not the same as a submitted
	// query without filters, so the two are never published as one value.
	FiltersKnown bool `json:"filtersKnown"`
}

// ReadAssistantQueryResultWithScopeTx returns the authorized query result and
// the public scope descriptor for the same row. The descriptor is produced by
// the same authorized transaction that authorizes the read, never by a second
// unguarded query. A missing tool step yields a descriptor whose filters are
// unknown; a real read error fails the read instead of publishing a descriptor
// that pretends the submitted query had no filters.
func ReadAssistantQueryResultWithScopeTx(ctx context.Context, tx pgx.Tx, j Job, id uuid.UUID) (StoredQueryResult, AssistantQueryScopeV1, error) {
	out, scope, err := loadQueryResultScopeTx(ctx, tx, j, id)
	if err != nil {
		return out, AssistantQueryScopeV1{}, err
	}
	descriptor := buildAssistantQueryScope(scope, nil, false)
	filters, known, ferr := assistantQueryScopeFilters(ctx, tx, j, id)
	if ferr != nil {
		return out, AssistantQueryScopeV1{}, ferr
	}
	descriptor.Filters = filters
	descriptor.FiltersKnown = known
	return out, descriptor, nil
}

// buildAssistantQueryScope projects the persisted query scope onto the public
// descriptor. The tenant and the authorization revision of the scope stay
// private; only the cluster, the namespace list, the canonical ids and the
// cluster scoped flag are published, in persisted order and deduplicated.
func buildAssistantQueryScope(scope graph.Scope, filters []AssistantQueryScopeFilterV1, known bool) AssistantQueryScopeV1 {
	if filters == nil {
		filters = []AssistantQueryScopeFilterV1{}
	}
	return AssistantQueryScopeV1{
		ClusterUID:    scope.Cluster,
		Namespaces:    uniqueScopeValues(scope.Namespaces),
		CanonicalIDs:  uniqueScopeValues(scope.Resources),
		ClusterScoped: scope.ClusterScoped,
		Filters:       filters,
		FiltersKnown:  known,
	}
}

// uniqueScopeValues keeps the persisted order and drops repeated values.
func uniqueScopeValues(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// assistantQueryScopeFilters reads the intent filters of the committed
// execute_analysis_query step that produced the query result. It reports
// whether such a step exists at all, so a record without a recoverable
// descriptor is never published as a query that had no filters. It never
// guesses or synthesizes filters.
func assistantQueryScopeFilters(ctx context.Context, tx pgx.Tx, j Job, id uuid.UUID) ([]AssistantQueryScopeFilterV1, bool, error) {
	if j.Assistant == nil {
		return []AssistantQueryScopeFilterV1{}, false, nil
	}
	rows, err := tx.Query(ctx, `SELECT result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND state='succeeded' AND tool_name='execute_analysis_query' ORDER BY started_at,step_id`, j.TenantID, j.JobID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []AssistantQueryScopeFilterV1{}
	found := false
	for rows.Next() {
		var result []byte
		if rows.Scan(&result) != nil {
			return nil, false, ErrInvalid
		}
		var wire struct {
			Data struct {
				Stored *struct {
					QueryResultID uuid.UUID `json:"queryResultId"`
				} `json:"storedQueryResult"`
				QueryResultRefs []uuid.UUID     `json:"queryResultRefs"`
				Filters         json.RawMessage `json:"filters"`
			} `json:"data"`
		}
		if json.Unmarshal(result, &wire) != nil {
			continue
		}
		matched := wire.Data.Stored != nil && wire.Data.Stored.QueryResultID == id
		if !matched {
			for _, ref := range wire.Data.QueryResultRefs {
				if ref == id {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}
		if len(wire.Data.Filters) == 0 {
			break
		}
		var filters []analytics.Filter
		if json.Unmarshal(wire.Data.Filters, &filters) != nil {
			return nil, false, ErrInvalid
		}
		found = true
		for _, filter := range filters {
			if published, ok := publishAssistantQueryScopeFilter(filter); ok {
				out = append(out, published)
			} else {
				return nil, false, ErrInvalid
			}
		}
		break
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, found, nil
}

// publishAssistantQueryScopeFilter keeps only printable safe intent filter
// values, using the same limits the product applies to intent filters.
func publishAssistantQueryScopeFilter(filter analytics.Filter) (AssistantQueryScopeFilterV1, bool) {
	if !analytics.ValidText(filter.Field, 128) || !analytics.ValidText(filter.Operator, 32) || !analytics.ValidText(filter.Value, 253) {
		return AssistantQueryScopeFilterV1{}, false
	}
	return AssistantQueryScopeFilterV1{Field: filter.Field, Operator: filter.Operator, Value: filter.Value}, true
}
