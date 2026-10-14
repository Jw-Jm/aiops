package investigation

import (
	"slices"
	"strings"

	"github.com/google/uuid"
	"ops-platform/internal/evidence"
	"ops-platform/internal/resource"
)

// Inputs are loaded from successful same-Job tool references by the evaluator
// boundary. A change is background context, never a causal confirmation. Both
// source clocks must be qualified before comparing their event intervals.
func applyFaultChainChanges(request FaultChainRequest, edge *FaultChainEdge, queries []StoredQueryResult, changes []evidence.Evidence, out *FaultChainContext) {
	refs := []FactRef{}
	statEvidence := []string{}
	for _, q := range queries {
		if q.Result.NetworkContext == nil || !q.Result.NetworkContext.TimeReliable || !q.Result.Exact || q.Result.Truncated || !q.Result.NetworkContext.CoverageComplete || q.Result.TimeRange == nil || !q.Result.TimeRange.From.Equal(request.Fault.From) || !q.Result.TimeRange.To.Equal(request.Fault.To) {
			continue
		}
		for _, observation := range edge.Observations {
			if !(strings.HasPrefix(observation.RuleID, "R1/") || strings.HasPrefix(observation.RuleID, "R2/")) || len(observation.Supports) == 0 {
				continue
			}
			for _, f := range observation.FactRefs {
				if f.QueryResultID == q.QueryResultID {
					refs = append(refs, f)
					statEvidence = append(statEvidence, observation.Supports...)
				}
			}
		}
	}
	if len(refs) == 0 {
		return
	}
	supports := slices.Clone(statEvidence)
	matched := false
	known := map[string]bool{}
	for _, e := range changes {
		if known[e.EvidenceID] || (e.Type != "change" && e.Type != "change_event") || !e.TimeReliable || e.ObservedFrom.IsZero() || e.ObservedTo.Before(e.ObservedFrom) || !e.ObservedTo.Before(request.Fault.From) || e.SourceRevision < 1 || e.SourceScopeDigest == "" || e.ReplayState != "archived_verified" {
			continue
		}
		if _, err := uuid.Parse(e.EvidenceID); err != nil {
			continue
		}
		if source, err := uuid.Parse(e.SourceRegistrationID); err != nil || source == uuid.Nil || e.SourceSystem == "" || e.ArchiveRef == nil || e.ArchiveRef.Object.VersionID == "" || e.ArchiveRef.PlaintextDigest != e.ContentDigest {
			continue
		}
		identity, err := resource.ParseCanonicalID(e.ResourceCanonicalID)
		target, targetErr := resource.ParseCanonicalID(request.TargetCanonicalID)
		if err != nil || targetErr != nil || e.TenantID != identity.Tenant || identity.Tenant != target.Tenant || identity.Scope != target.Scope || (e.ResourceCanonicalID != edge.Relation.From && e.ResourceCanonicalID != edge.Relation.To) {
			continue
		}
		// Restrict background changes to the frozen baseline. Without a
		// baseline no additional look-back window is silently invented.
		if request.Baseline == nil || e.ObservedFrom.Before(request.Baseline.From) || e.ObservedTo.After(request.Baseline.To) {
			continue
		}
		known[e.EvidenceID] = true
		supports = append(supports, e.EvidenceID)
		out.EvidenceRefs = append(out.EvidenceRefs, e.EvidenceID)
		matched = true
	}
	if !matched {
		return
	}
	slices.Sort(supports)
	supports = slices.Compact(supports)
	slices.SortFunc(refs, func(a, b FactRef) int {
		if a.QueryResultID != b.QueryResultID {
			return slices.Compare(a.QueryResultID[:], b.QueryResultID[:])
		}
		return strings.Compare(a.FactID, b.FactID)
	})
	refs = slices.Compact(refs)
	edge.Observations = append(edge.Observations, ChainObservation{RuleID: "R4/change-before-observation-window", Reason: "同一资源身份的已归档变更观测早于故障统计窗口，仅作为关联背景；未证明变更导致异常。", Supports: supports, Conflicts: []string{}, FactRefs: refs})
	edge.MissingEvidence = slices.DeleteFunc(edge.MissingEvidence, func(v string) bool { return v == "change_source_not_checked" })
	edge.MissingEvidence = append(edge.MissingEvidence, "change_causal_mechanism_unverified")
}
