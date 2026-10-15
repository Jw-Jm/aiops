package investigation

import (
	"slices"

	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
)

// R5 uses the frozen baseline without an invented tolerance. Improvement alone
// remains a separate observation; source time and same-window health are gates.
func applyFaultChainRecovery(request FaultChainRequest, edge *FaultChainEdge, queries []StoredQueryResult, health []FaultChainResourceHealth) {
	if request.Recovery == nil {
		return
	}
	edge.MissingEvidence = append(edge.MissingEvidence, "recovery_baseline_and_health_unverified")
	if request.Baseline == nil {
		return
	}
	abnormal := false
	for _, ob := range edge.Observations {
		if (len(ob.RuleID) >= 3 && (ob.RuleID[:3] == "R1/" || ob.RuleID[:3] == "R2/")) && len(ob.Supports) > 0 {
			abnormal = true
		}
	}
	if !abnormal {
		return
	}
	matching := func(q StoredQueryResult, window, prior *analytics.QueryTimeRange) bool {
		r, n := q.Result, q.Result.NetworkContext
		return slices.Contains(edge.QueryResultRefs, q.QueryResultID) && validateStoredQueryResult(q) == nil && r.Status == "complete" && r.Exact && !r.Truncated && len(r.DegradedSources) == 0 && n != nil && n.CoverageComplete && n.TimeReliable && r.SourceManifest != nil && len(r.DerivationEvidenceRefs) > 0 && r.TimeRange != nil && r.ComparisonTimeRange != nil && *r.TimeRange == *window && *r.ComparisonTimeRange == *prior
	}
	supports := []string{}
	refs := []FactRef{}
	// Both qualified protocols are necessary for this finite recovery predicate.
	for _, dataset := range []string{"network.http-summary", "network.tcp-summary"} {
		found := false
		for _, fault := range queries {
			if fault.Result.DatasetID != dataset || !matching(fault, &request.Fault, request.Baseline) {
				continue
			}
			for _, recovery := range queries {
				if recovery.Result.DatasetID != dataset || !matching(recovery, request.Recovery, &request.Fault) {
					continue
				}
				f, r := fault.Result, recovery.Result
				if *f.NetworkContext != *r.NetworkContext || f.ScopeDigest != r.ScopeDigest || f.SourceManifest.SourceID != r.SourceManifest.SourceID || f.SourceManifest.Revision != r.SourceManifest.Revision || f.SourceManifest.BindingDigest != r.SourceManifest.BindingDigest || f.SourceManifest.QualificationDigest != r.SourceManifest.QualificationDigest || f.SourceManifest.TemplateVersion != r.SourceManifest.TemplateVersion {
					continue
				}
				values := func(q analytics.QueryResult) map[string]float64 {
					m := map[string]float64{}
					for _, v := range q.Statistics {
						m[v.Key] = v.Value
					}
					return m
				}
				base, current := values(f), values(r)
				keys := []string{"failure_observations", "retransmissions"}
				if dataset == "network.http-summary" {
					if base["baseline.responses"] <= 0 || current["current.responses"] <= 0 {
						continue
					}
					bErrors, bok := base["baseline.5xx"]
					cErrors, cok := current["current.5xx"]
					if !bok || !cok || cErrors/current["current.responses"] > bErrors/base["baseline.responses"] {
						continue
					}
					keys = []string{"mean_response_ms"}
				}
				acceptable := true
				for _, key := range keys {
					b, bok := base["baseline."+key]
					c, cok := current["current."+key]
					if !bok || !cok || c > b {
						acceptable = false
					}
				}
				if !acceptable {
					continue
				}
				found = true
				supports = append(supports, f.DerivationEvidenceRefs...)
				supports = append(supports, r.DerivationEvidenceRefs...)
				for _, q := range []StoredQueryResult{fault, recovery} {
					for _, fact := range q.Facts {
						refs = append(refs, FactRef{q.QueryResultID, fact.FactID})
					}
				}
				break
			}
			if found {
				break
			}
		}
		if !found {
			return
		}
	}
	required := []string{edge.Relation.From, edge.Relation.To}
	for _, h := range health {
		if h.ConditionStatus == "False" && !h.ObservedAt.Before(request.Fault.From) && h.ObservedAt.Before(request.Fault.To) {
			for _, other := range edge.Observations {
				if other.ResourceHealth != nil && other.ResourceHealth.CanonicalID == h.CanonicalID {
					required = append(required, h.CanonicalID)
				}
			}
		}
	}
	slices.Sort(required)
	required = slices.Compact(required)
	snapshots := []FaultChainResourceHealth{}
	for _, id := range required {
		var latest *FaultChainResourceHealth
		for i := range health {
			h := &health[i]
			if h.CanonicalID == id && h.ConditionType == "Ready" && !h.ObservedAt.Before(request.Recovery.From) && h.ObservedAt.Before(request.Recovery.To) && len(h.Sources) > 0 && (latest == nil || h.ObservedAt.After(latest.ObservedAt) || h.ObservedAt.Equal(latest.ObservedAt) && (h.ConditionStatus == "False" && latest.ConditionStatus != "False" || h.ConditionStatus == latest.ConditionStatus && graph.ScopeDigest(*h) < graph.ScopeDigest(*latest))) {
				latest = h
			}
		}
		if latest == nil || latest.ConditionStatus != "True" {
			return
		}
		snapshots = append(snapshots, *latest)
	}
	slices.Sort(supports)
	supports = slices.Compact(supports)
	// At most three resource snapshots and the existing per-edge Fact bound.
	if len(refs) > 100 {
		return
	}
	for _, h := range snapshots {
		edge.Observations = append(edge.Observations, ChainObservation{RuleID: "R5/recovery-consistency", Reason: "完整且时间合格的恢复窗口中，HTTP 错误率、耗时和传输异常观测均不高于冻结基线，相关资源 Ready=True；观察到恢复迹象，不自动关闭 Incident。", Supports: supports, Conflicts: []string{}, FactRefs: refs, ResourceHealth: &h})
	}
	edge.MissingEvidence = slices.DeleteFunc(edge.MissingEvidence, func(v string) bool { return v == "recovery_baseline_and_health_unverified" })
}
