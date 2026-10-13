package analytics

import (
	"context"
	"slices"
	"sort"
	"time"

	"ops-platform/internal/graph"
)

type Executor struct {
	Authorization ScopeReader
	Graph         ResourceReader
}

func CheckGrant(ctx context.Context, reader ScopeReader, subject string, scope graph.Scope, digest string) error {
	if reader == nil {
		return ErrDenied
	}
	current, err := reader.Effective(ctx, scope.Tenant, subject, scope.Cluster)
	if err != nil {
		return ErrDenied
	}
	if graph.ScopeDigest(current) != digest {
		return ErrStale
	}
	return nil
}

// Execute returns an internal result. Final Message/SSE disclosure must still
// recheck current authority and validate the frozen provenance in the Job runtime.
func (e Executor) Execute(ctx context.Context, plan QueryPlan) (QueryResult, error) {
	if plan.digest == "" || e.Graph == nil {
		return QueryResult{}, ErrInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := CheckGrant(bounded, e.Authorization, plan.subject, plan.query.Scope, plan.grantDigest); err != nil {
		return QueryResult{}, err
	}
	q := plan.query
	q.Scope = CloneScope(q.Scope)
	out, err := e.Graph.Query(bounded, q)
	if err != nil {
		return QueryResult{}, err
	}
	if err := CheckGrant(bounded, e.Authorization, plan.subject, q.Scope, plan.grantDigest); err != nil {
		return QueryResult{}, err
	}
	if err := bounded.Err(); err != nil {
		return QueryResult{}, err
	}
	if out.SchemaVersion != "resource-graph/v2" || out.GraphRevision.OwnerEpoch != q.ExpectedOwnerEpoch ||
		out.GraphRevision.GraphGeneration < 1 || out.CollectedAt.IsZero() ||
		!slices.Contains([]string{"fresh", "stale", "unavailable"}, out.Freshness) {
		return QueryResult{}, ErrSource
	}
	result := QueryResult{SchemaVersion: "query-result/v1", DatasetID: "resources.current", PlanDigest: plan.digest,
		CatalogDigest: plan.catalogDigest, ScopeDigest: graph.ScopeDigest(q.Scope), Dimension: plan.dimension,
		Unit: "resources", Rows: []Row{}, SnapshotReadAt: out.CollectedAt, GraphRevision: out.GraphRevision,
		Freshness: out.Freshness, Warnings: append([]string{}, out.Warnings...), DegradedSources: append([]string{}, out.DegradedSources...)}
	if out.Freshness == "unavailable" {
		result.Status = "degraded"
		return result, nil
	}
	if out.Summary == nil {
		result.Status = "no_data"
		return result, nil
	}
	if !validSummary(out.Summary, q.Kind) {
		return QueryResult{}, ErrSource
	}
	result.Exact = CompleteResourceList(out)
	result.Status = "complete"
	if !result.Exact {
		result.Status = "partial"
	}
	// A stale/partial empty snapshot does not establish a count of zero.
	if out.Summary.Total == 0 && !result.Exact {
		result.Status = "no_data"
		return result, nil
	}
	if plan.dimension == "" {
		result.Rows = append(result.Rows, Row{Value: float64(out.Summary.Total)})
	} else {
		groups := out.Summary.ByKind
		if plan.dimension == "health" {
			groups = out.Summary.ByHealth
		}
		for dimension, count := range groups {
			result.Rows = append(result.Rows, Row{dimension, float64(count)})
		}
		sort.Slice(result.Rows, func(i, j int) bool {
			a, b := result.Rows[i], result.Rows[j]
			if plan.sort.Field == "resource_count" && a.Value != b.Value {
				if plan.sort.Direction == "asc" {
					return a.Value < b.Value
				}
				return a.Value > b.Value
			}
			if plan.sort.Field == plan.dimension && plan.sort.Direction == "desc" {
				return a.Dimension > b.Dimension
			}
			return a.Dimension < b.Dimension
		})
		if len(result.Rows) > plan.limit {
			result.Truncated = true
			result.Rows = result.Rows[:plan.limit]
		}
	}
	return result, nil
}

func validSummary(summary *graph.ResourceSummary, kind string) bool {
	if summary.Total < 0 || summary.ByKind == nil || summary.ByHealth == nil {
		return false
	}
	kinds, health := 0, 0
	for key, count := range summary.ByKind {
		if key != kind || count < 0 || count > summary.Total-kinds {
			return false
		}
		kinds += count
	}
	for key, count := range summary.ByHealth {
		if !slices.Contains([]string{"normal", "abnormal", "unknown", "degraded"}, key) || count < 0 || count > summary.Total-health {
			return false
		}
		health += count
	}
	return kinds == summary.Total && health == summary.Total
}
