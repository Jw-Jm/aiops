package analytics

import "ops-platform/internal/graph"

// CompleteResourceList interprets completeness relative to the authorized
// query scope. Graph marks scope_limited when it omits invisible nodes; those
// nodes are outside this dataset, not missing samples within it. All other
// partial reasons remain incomplete. This rule is only for list summaries,
// never for dependency/impact graphs where invisible links break a chain.
func CompleteResourceList(out graph.Result) bool {
	if out.Summary == nil || out.Freshness != "fresh" || len(out.DegradedSources) > 0 {
		return false
	}
	for _, warning := range out.Warnings {
		if warning != "scope_limited" {
			return false
		}
	}
	return !out.Partial || len(out.Warnings) > 0
}
