package analytics

import (
	"encoding/json"
	"slices"

	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
)

// DecodeIntent rejects unknown identity, expression, network and privilege fields
// before Go decoding can silently drop them. Supported semantics are checked by Compile.
func DecodeIntent(raw []byte) (QueryIntent, error) {
	var intent QueryIntent
	if len(raw) > 16<<10 || contract.Validate("https://ops.local/schemas/query-intent/v1", raw) != nil {
		return intent, ErrInvalid
	}
	if json.Unmarshal(raw, &intent) != nil {
		return intent, ErrInvalid
	}
	return intent, nil
}

func Compile(intent QueryIntent, binding Binding) (QueryPlan, error) {
	if intent.NetworkSelection != nil {
		return QueryPlan{}, ErrUnsupported
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		return QueryPlan{}, ErrInvalid
	}
	if _, err := DecodeIntent(raw); err != nil {
		return QueryPlan{}, err
	}
	if !ValidText(binding.Subject, 512) || binding.OwnerEpoch < 1 {
		return QueryPlan{}, ErrInvalid
	}
	catalog, err := Catalog()
	if err != nil {
		return QueryPlan{}, err
	}
	dataset := catalog.Datasets[0]
	if intent.ComparisonTimeRange != nil || intent.StepSeconds != 0 || intent.TimeRange != nil || intent.DatasetID != dataset.ID || intent.Operation != "aggregate" ||
		len(intent.Metrics) != 1 || intent.Metrics[0] != dataset.Metric ||
		len(intent.Dimensions) > 1 {
		return QueryPlan{}, ErrUnsupported
	}
	dimension := ""
	if len(intent.Dimensions) == 1 {
		dimension = intent.Dimensions[0]
		if !slices.Contains(dataset.Dimensions, dimension) {
			return QueryPlan{}, ErrUnsupported
		}
	}
	scope, err := RestrictScope(binding.Grant, binding.Selection)
	if err != nil {
		return QueryPlan{}, err
	}
	q := graph.Query{QueryKind: "list", Scope: scope, ExpectedOwnerEpoch: binding.OwnerEpoch,
		RelationKinds: []string{}, MaxDepth: 1, MaxNodes: 1, MaxEdges: 1, Sort: "canonicalId", Order: "asc"}
	seen := map[string]bool{}
	for _, filter := range intent.Filters {
		if seen[filter.Field] {
			return QueryPlan{}, ErrInvalid
		}
		seen[filter.Field] = true
		if !slices.Contains(dataset.Filters, filter.Field) || filter.Operator != "eq" {
			return QueryPlan{}, ErrUnsupported
		}
		if !ValidText(filter.Value, 253) {
			return QueryPlan{}, ErrInvalid
		}
		switch filter.Field {
		case "kind":
			if !SupportedKind(filter.Value) {
				return QueryPlan{}, ErrUnsupported
			}
			q.Kind = filter.Value
		case "namespace":
			if !slices.Contains(scope.Namespaces, filter.Value) {
				return QueryPlan{}, ErrDenied
			}
			q.Namespace = filter.Value
			q.Scope.Namespaces = []string{filter.Value}
			q.Scope.ClusterScoped = false
		case "name":
			q.Name = filter.Value
		case "health":
			if !slices.Contains(dataset.HealthValues, filter.Value) {
				return QueryPlan{}, ErrUnsupported
			}
			q.Health = filter.Value
		}
	}
	// Requiring an explicit nonvirtual kind prevents general summaries from
	// silently enabling deferred VM/VMI/DataVolume analysis.
	if q.Kind == "" {
		return QueryPlan{}, ErrUnsupported
	}
	if ClusterScopedKind(q.Kind) {
		if q.Namespace != "" {
			return QueryPlan{}, ErrUnsupported
		}
		if !q.Scope.ClusterScoped {
			return QueryPlan{}, ErrDenied
		}
	} else if len(q.Scope.Namespaces) == 0 {
		return QueryPlan{}, ErrDenied
	}
	sort := Sort{Field: dataset.Metric, Direction: "desc"}
	if len(intent.Sort) > 1 {
		return QueryPlan{}, ErrUnsupported
	}
	if len(intent.Sort) == 1 {
		sort = intent.Sort[0]
		if sort.Field != dataset.Metric && (dimension == "" || sort.Field != dimension) {
			return QueryPlan{}, ErrUnsupported
		}
	}
	if intent.Limit > dataset.MaxGroups {
		return QueryPlan{}, ErrInvalid
	}
	plan := QueryPlan{query: q, subject: binding.Subject, grantDigest: graph.ScopeDigest(binding.Grant),
		catalogDigest: catalogDigest(catalog), dimension: dimension, sort: sort, limit: intent.Limit}
	plan.digest = graph.ScopeDigest(struct {
		Query                                          graph.Query
		Subject, GrantDigest, CatalogDigest, Dimension string
		Sort                                           Sort
		Limit                                          int
	}{q, plan.subject, plan.grantDigest, plan.catalogDigest, dimension, sort, plan.limit})
	return plan, nil
}
