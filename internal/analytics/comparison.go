package analytics

import (
	"context"
	"math"
	"slices"
	"time"

	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
)

// A comparison is two fixed gauge endpoint evaluations under one compiled
// scope and qualification, with a shared deadline. It is not a window mean or
// a model-derived percentage. Missing/partial input never produces a delta.
func (v *Victoria) executeComparison(ctx context.Context, p VictoriaPlan) (QueryResult, error) {
	if p.intent.ComparisonTimeRange == nil {
		return QueryResult{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	single := p
	single.intent.Operation = "aggregate"
	single.intent.ComparisonTimeRange = nil
	single.dimension = ""
	single.intent.TimeRange = copyQueryTimeRange(p.intent.ComparisonTimeRange)
	baseline, err := v.Execute(ctx, single)
	if err != nil {
		return QueryResult{}, err
	}
	single.intent.TimeRange = copyQueryTimeRange(p.intent.TimeRange)
	current, err := v.Execute(ctx, single)
	if err != nil {
		return QueryResult{}, err
	}
	out := current
	out.Dimension = "comparison"
	out.ComparisonTimeRange = copyQueryTimeRange(p.intent.ComparisonTimeRange)
	out.Rows = []Row{}
	out.Warnings = append(slices.Clone(baseline.Warnings), current.Warnings...)
	out.DegradedSources = []string{}
	for _, source := range append(slices.Clone(baseline.DegradedSources), current.DegradedSources...) {
		if !slices.Contains(out.DegradedSources, source) {
			out.DegradedSources = append(out.DegradedSources, source)
		}
	}
	if len(baseline.Rows) == 1 {
		out.Rows = append(out.Rows, Row{"baseline", baseline.Rows[0].Value})
	} else {
		out.Warnings = append(out.Warnings, "comparison_baseline_unavailable")
	}
	if len(current.Rows) == 1 {
		out.Rows = append(out.Rows, Row{"current", current.Rows[0].Value})
	} else {
		out.Warnings = append(out.Warnings, "comparison_current_unavailable")
	}
	out.Exact = baseline.Exact && current.Exact && len(out.Rows) == 2
	out.Status = "partial"
	if len(out.Rows) == 0 {
		out.Status = "no_data"
	}
	if out.Exact {
		out.Status = "complete"
		delta := current.Rows[0].Value - baseline.Rows[0].Value
		direction := "unchanged"
		if delta > 0 {
			direction = "increase"
		} else if delta < 0 {
			direction = "decrease"
		}
		out.Rows = append(out.Rows, Row{direction, math.Abs(delta)})
	}
	out.SourceManifest.CoverageFrom = p.intent.ComparisonTimeRange.From
	out.SourceManifest.CoverageTo = p.intent.TimeRange.To
	out.SourceManifest.QueryHash = graph.ScopeDigest(struct {
		Query             string
		Baseline, Current *QueryTimeRange
	}{p.query, p.intent.ComparisonTimeRange, p.intent.TimeRange})
	out.SourceManifest.TemplateVersion = p.intent.DatasetID + "-compare/v1"
	if v.authorize(ctx, v.binding) != nil || !p.qualification.VerifiedUntil.After(time.Now()) {
		return QueryResult{}, ErrDenied
	}
	if contract.Validate("https://ops.local/schemas/query-result/v1", rawResult(out)) != nil {
		return QueryResult{}, ErrSource
	}
	return out, nil
}
