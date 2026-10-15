package investigation

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

// A narrow immutable reference to a committed native health observation. The
// complete original Step remains the authority, rather than model-owned JSON.
type FaultChainResourceHealth struct {
	StepID          uuid.UUID               `json:"stepId"`
	CanonicalID     string                  `json:"canonicalId"`
	Namespace       string                  `json:"namespace"`
	ObservedAt      time.Time               `json:"observedAt"`
	ConditionType   string                  `json:"conditionType"`
	ConditionStatus string                  `json:"conditionStatus"`
	Sources         []graph.SourceAuthority `json:"sources"`
}

func CommittedFaultChainResourceHealth(step uuid.UUID, snapshot graph.Result) []FaultChainResourceHealth {
	out := []FaultChainResourceHealth{}
	if step == uuid.Nil || snapshot.Partial || snapshot.Freshness != "fresh" || len(snapshot.DegradedSources) > 0 || snapshot.CollectedAt.IsZero() {
		return out
	}
	for _, node := range snapshot.Nodes {
		id, err := resource.ParseCanonicalID(node.CanonicalID)
		if err != nil || id.Domain != "k8s" || id.APIGroup != "core" || (id.Kind != "Pod" && id.Kind != "Node") || node.UpdatedAt.IsZero() || node.UpdatedAt.After(snapshot.CollectedAt) || len(node.SourceRefs) == 0 {
			continue
		}
		status, _ := node.Attributes["status"].(map[string]any)
		conditions, _ := status["conditions"].([]any)
		readyStatus := ""
		for _, raw := range conditions {
			condition, _ := raw.(map[string]any)
			if condition["type"] != "Ready" {
				continue
			}
			status, _ := condition["status"].(string)
			if readyStatus != "" || !slices.Contains([]string{"False", "True", "Unknown"}, status) {
				readyStatus = ""
				break
			}
			readyStatus = status
		}
		if readyStatus == "" {
			continue
		}
		sources := []graph.SourceAuthority{}
		for _, ref := range node.SourceRefs {
			for _, source := range snapshot.ObservationSources {
				if source.SourceRegistrationID == ref && source.Revision > 0 && source.ScopeDigest != "" {
					sources = append(sources, source)
				}
			}
		}
		if len(sources) != len(node.SourceRefs) {
			continue
		}
		slices.SortFunc(sources, func(a, b graph.SourceAuthority) int {
			return strings.Compare(a.SourceRegistrationID, b.SourceRegistrationID)
		})
		out = append(out, FaultChainResourceHealth{step, node.CanonicalID, node.Namespace, node.UpdatedAt, "Ready", readyStatus, sources})
	}
	return out
}

// R3 requires two distinct affected callers, matching committed shared paths,
// and the common resource's own qualified same-window NotReady observation.
func EvaluateFaultChainWithResourceHealth(request FaultChainRequest, relations []resource.Relation, queries []StoredQueryResult, now time.Time, health []FaultChainResourceHealth, changes []evidence.Evidence) (FaultChainContext, error) {
	out, err := EvaluateFaultChain(request, relations, queries, now, changes)
	if err != nil {
		return out, err
	}
	if len(health) > 40 {
		return out, ErrInvalid
	}
	health = slices.Clone(health)
	slices.SortFunc(health, func(a, b FaultChainResourceHealth) int {
		return strings.Compare(graph.ScopeDigest(a), graph.ScopeDigest(b))
	})
	for index := range out.Edges {
		edge := &out.Edges[index]
		known := map[string]bool{}
		for _, h := range health {
			if known[h.CanonicalID] || h.StepID == uuid.Nil || h.ConditionType != "Ready" || h.ConditionStatus != "False" || h.ObservedAt.Before(request.Fault.From) || !h.ObservedAt.Before(request.Fault.To) || len(h.Sources) == 0 {
				continue
			}
			candidate, err := resource.ParseCanonicalID(h.CanonicalID)
			target, targetErr := resource.ParseCanonicalID(request.TargetCanonicalID)
			if err != nil || targetErr != nil || candidate.Tenant != target.Tenant || candidate.Scope != target.Scope {
				continue
			}
			affected := map[string]bool{}
			paths := []resource.Relation{}
			parents := []StoredQueryResult{}
			refs := []FactRef{}
			supports := []string{}
			for _, q := range queries {
				n := q.Result.NetworkContext
				if n == nil || !n.TimeReliable || !n.CoverageComplete || !q.Result.Exact || q.Result.Truncated || q.Result.Status != "complete" || q.Result.TimeRange == nil || !q.Result.TimeRange.From.Equal(request.Fault.From) || !q.Result.TimeRange.To.Equal(request.Fault.To) || validateStoredQueryResult(q) != nil {
					continue
				}
				if !slices.Contains([]string{"network.http-summary", "network.http-5xx-rate", "network.http-5xx"}, q.Result.DatasetID) || n.Protocol != "http" {
					continue
				}
				counts := map[string]float64{}
				for _, s := range q.Result.Statistics {
					counts[s.Key] = s.Value
				}
				if counts["current.responses"] <= 0 || counts["current.5xx"] <= 0 {
					continue
				}
				if len(q.Result.DerivationEvidenceRefs) == 0 || (q.Result.ComparisonTimeRange != nil && !request.Allows(q.Result.ComparisonTimeRange)) {
					continue
				}
				caller, server := n.Selection.TargetCanonicalID, n.Selection.PeerCanonicalID
				if n.Selection.Direction == "incoming" {
					caller, server = server, caller
				} else if n.Selection.Direction != "outgoing" {
					continue
				}
				left, leftErr := resource.ParseCanonicalID(caller)
				right, rightErr := resource.ParseCanonicalID(server)
				if leftErr != nil || rightErr != nil || left.Kind != "Pod" || right.Kind != "Pod" || left.Tenant != candidate.Tenant || right.Tenant != candidate.Tenant || left.Scope != candidate.Scope || right.Scope != candidate.Scope {
					continue
				}
				var matched *resource.Relation
				for _, path := range relations {
					if path.From != caller || path.To != h.CanonicalID || path.Provenance.SourceRegistrationID == "" || path.ObservedAt.Before(request.Fault.From) || !path.ObservedAt.Before(request.Fault.To) {
						continue
					}
					nativeSource := false
					for _, s := range h.Sources {
						if s.SourceRegistrationID == path.Provenance.SourceRegistrationID {
							nativeSource = true
						}
					}
					if (candidate.Kind == "Node" && path.Kind == "scheduled_on" && nativeSource) || (candidate.Kind == "Pod" && path.Kind == "communicates_with" && server == h.CanonicalID && q.Result.SourceManifest != nil && path.Provenance.SourceRegistrationID == q.Result.SourceManifest.SourceID) {
						p := path
						matched = &p
						break
					}
				}
				if matched == nil || affected[caller] {
					continue
				}
				affected[caller] = true
				paths = append(paths, *matched)
				parents = append(parents, q)
				supports = append(supports, q.Result.DerivationEvidenceRefs...)
				for _, f := range q.Facts {
					if f.Column == "value" {
						refs = append(refs, FactRef{q.QueryResultID, f.FactID})
					}
				}
			}
			if len(affected) < 2 || len(supports) == 0 || (!affected[edge.Relation.From] && !affected[edge.Relation.To]) {
				continue
			}
			known[h.CanonicalID] = true
			slices.Sort(supports)
			supports = slices.Compact(supports)
			slices.SortFunc(paths, func(a, b resource.Relation) int { return strings.Compare(graph.ScopeDigest(a), graph.ScopeDigest(b)) })
			slices.SortFunc(refs, func(a, b FactRef) int { return strings.Compare(graph.ScopeDigest(a), graph.ScopeDigest(b)) })
			edge.Observations = append(edge.Observations, ChainObservation{RuleID: "R3/shared-resource-observation", Reason: "多个授权调用方在同一合格窗口观察到 HTTP 异常，并存在共享资源关系及该资源自身 NotReady 快照；支持检查共同资源，未确认共同根因。", Supports: supports, Conflicts: []string{}, FactRefs: refs, ResourceHealth: &h, RelatedRelations: paths})
			edge.MissingEvidence = slices.DeleteFunc(edge.MissingEvidence, func(v string) bool { return v == "shared_resource_cause_not_checked" })
			edge.MissingEvidence = append(edge.MissingEvidence, "shared_resource_causal_mechanism_unverified")
			for _, q := range parents {
				edge.QueryResultRefs = append(edge.QueryResultRefs, q.QueryResultID)
				out.QueryResultRefs = append(out.QueryResultRefs, q.QueryResultID)
				out.EvidenceRefs = append(out.EvidenceRefs, q.Result.DerivationEvidenceRefs...)
			}
		}
		applyFaultChainRecovery(request, edge, queries, health)
		slices.SortFunc(edge.QueryResultRefs, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		edge.QueryResultRefs = slices.Compact(edge.QueryResultRefs)
	}
	slices.SortFunc(out.QueryResultRefs, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	out.QueryResultRefs = slices.Compact(out.QueryResultRefs)
	slices.Sort(out.EvidenceRefs)
	out.EvidenceRefs = slices.Compact(out.EvidenceRefs)
	return out, nil
}

func decodeFaultChainHealthSnapshot(raw json.RawMessage) (graph.Result, error) {
	var snapshot graph.Result
	err := json.Unmarshal(raw, &snapshot)
	return snapshot, err
}
