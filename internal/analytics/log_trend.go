package analytics

import (
	"context"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"slices"
	"strings"
	"time"
)

// Rewrite only the compiler-owned absolute window in a frozen LogsQL template.
// Scope selectors and source qualification remain unchanged.
func rewindow(p VictoriaPlan, window *QueryTimeRange) VictoriaPlan {
	old := p.intent.TimeRange
	if p.path == "/select/logsql/stats_query" {
		oldText := "_time:[" + old.From.UTC().Format(time.RFC3339Nano) + "," + old.To.UTC().Format(time.RFC3339Nano) + ")"
		newText := "_time:[" + window.From.UTC().Format(time.RFC3339Nano) + "," + window.To.UTC().Format(time.RFC3339Nano) + ")"
		p.query = strings.Replace(p.query, oldText, newText, 1)
	}
	p.intent.TimeRange = copyQueryTimeRange(window)
	return p
}

// Logs use closed, disjoint half-open buckets, including the observation ending
// at the first grid point. No caller expressions or missing buckets become zero.
func (v *Victoria) executeLogTrend(ctx context.Context, p VictoriaPlan) (QueryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	single := p
	single.intent.Operation = "aggregate"
	single.intent.StepSeconds = 0
	single.dimension = ""
	var out QueryResult
	points := []Row{}
	exact := true
	warnings, degraded := []string{}, []string{}
	for at := p.intent.TimeRange.From; !at.After(p.intent.TimeRange.To); at = at.Add(time.Duration(p.intent.StepSeconds) * time.Second) {
		window := &QueryTimeRange{From: at.Add(-time.Duration(p.intent.StepSeconds) * time.Second), To: at, Timezone: p.intent.TimeRange.Timezone}
		single = rewindow(single, window)
		result, err := v.Execute(ctx, single)
		if err != nil {
			return QueryResult{}, err
		}
		out = result
		warnings = append(warnings, result.Warnings...)
		degraded = append(degraded, result.DegradedSources...)
		if len(result.Rows) == 1 {
			points = append(points, Row{at.UTC().Format(time.RFC3339), result.Rows[0].Value})
		}
		exact = exact && result.Exact && len(result.Rows) == 1
	}
	slices.Sort(warnings)
	slices.Sort(degraded)
	out.Warnings = slices.Compact(warnings)
	out.DegradedSources = slices.Compact(degraded)
	out.TimeRange = copyQueryTimeRange(p.intent.TimeRange)
	out.Dimension = "time"
	out.StepSeconds = p.intent.StepSeconds
	out.Rows = points
	out.Exact = exact
	out.Status = "complete"
	if !exact {
		out.Status = "partial"
		out.Warnings = append(out.Warnings, "missing_or_partial_log_buckets")
	}
	if len(points) == 0 {
		out.Status = "no_data"
		out.Exact = false
	}
	out.SourceManifest.TemplateVersion = p.intent.DatasetID + "-trend/v1"
	out.SourceManifest.QueryHash = graph.ScopeDigest(struct {
		Query  string
		Step   int
		Window *QueryTimeRange
	}{p.query, p.intent.StepSeconds, p.intent.TimeRange})
	out.SourceManifest.CoverageFrom = p.intent.TimeRange.From.Add(-time.Duration(p.intent.StepSeconds) * time.Second)
	if v.authorize(ctx, v.binding) != nil || !p.qualification.VerifiedUntil.After(time.Now()) {
		return QueryResult{}, ErrDenied
	}
	if contract.Validate("https://ops.local/schemas/query-result/v1", rawResult(out)) != nil {
		return QueryResult{}, ErrSource
	}
	return out, nil
}
