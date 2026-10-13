package analytics

import (
	"context"
	"math"
	"slices"
	"time"

	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
)

// A comparison evaluates two fixed equal windows: gauge endpoints, CPU means,
// counter increases or log counts. All derived differences and percentages are
// server computed; missing/partial inputs never produce a delta.
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
	single = rewindow(single, p.intent.ComparisonTimeRange)
	baseline, err := v.Execute(ctx, single)
	if err != nil {
		return QueryResult{}, err
	}
	single = rewindow(single, p.intent.TimeRange)
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
	if p.intent.Operation == "compare_percent" {
		out.Unit = "percent"
		out.Rows = []Row{}
		if out.Exact && baseline.Rows[0].Value > 0 {
			delta := current.Rows[0].Value - baseline.Rows[0].Value
			direction := "unchanged"
			if delta > 0 {
				direction = "increase"
			} else if delta < 0 {
				direction = "decrease"
			}
			percent := math.Abs(delta) / baseline.Rows[0].Value * 100
			if math.IsNaN(percent) || math.IsInf(percent, 0) || percent > 1<<53 {
				return QueryResult{}, ErrSource
			}
			out.Rows = append(out.Rows, Row{direction, percent})
		} else {
			out.Status, out.Exact = "no_data", false
			out.Warnings = append(out.Warnings, "percentage_requires_complete_nonzero_baseline")
		}
	}
	out.SourceManifest.CoverageFrom = p.intent.ComparisonTimeRange.From
	out.SourceManifest.CoverageTo = p.intent.TimeRange.To
	out.SourceManifest.QueryHash = graph.ScopeDigest(struct {
		Query             string
		Baseline, Current *QueryTimeRange
	}{p.query, p.intent.ComparisonTimeRange, p.intent.TimeRange})
	out.SourceManifest.TemplateVersion = p.intent.DatasetID + "-" + p.intent.Operation + "/v1"
	if v.authorize(ctx, v.binding) != nil || !p.qualification.VerifiedUntil.After(time.Now()) {
		return QueryResult{}, ErrDenied
	}
	if contract.Validate("https://ops.local/schemas/query-result/v1", rawResult(out)) != nil {
		return QueryResult{}, ErrSource
	}
	return out, nil
}
