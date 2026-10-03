package npd

import (
	"encoding/json"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/inspection/hardware"
	"ops-platform/internal/resource"
	"time"
)

// NativeKernelCandidates consumes scoped, versioned log Evidence only. Missing
// logs do not resolve a kernel fault. Archived ruleEvaluation is never trusted
// as the diagnosis; the locked NPD rules are replayed against each native line.
func NativeKernelCandidates(e evidence.Evidence) ([]finding.FindingCandidate, error) {
	id, err := resource.ParseCanonicalID(e.ResourceCanonicalID)
	if err != nil || id.Domain != "k8s" || id.Kind != "Node" || e.SourceSystem != "victorialogs" || e.QueryTemplateVersion != "node-kernel-logs/v1" || !e.TimeReliable || e.ContentDigest != evidence.Digest(e.Data) {
		return nil, evidence.ErrArgument
	}
	var rows []struct {
		Time      string `json:"_time"`
		Transport string `json:"_TRANSPORT"`
		Message   string `json:"_msg"`
	}
	if len(e.Data) > 64<<10 || json.Unmarshal(e.Data, &rows) != nil || len(rows) > 64 {
		return nil, evidence.ErrBudget
	}
	out := []finding.FindingCandidate{}
	for _, row := range rows {
		clock, err := time.Parse(time.RFC3339Nano, row.Time)
		if row.Transport != "kernel" || err != nil || clock.Before(e.ObservedFrom) || clock.After(e.ObservedTo) || len(row.Message) > 4096 {
			return nil, evidence.ErrArgument
		}
		results, err := hardware.MatchKernelRecord(row.Message)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, result := range results {
			if result.Degraded || result.Status != "degraded" || seen[result.RuleID] {
				continue
			}
			seen[result.RuleID] = true
			raw, _ := json.Marshal(map[string]any{"reason": result.RuleID, "ruleVersion": result.RuleVersion, "nativeEvidenceId": e.EvidenceID, "nativeEventAt": clock.UTC().Format(time.RFC3339Nano)})
			out = append(out, finding.FindingCandidate{ResourceCanonicalID: e.ResourceCanonicalID, RuleID: "npd/" + result.RuleID + "/v1", RuleFamily: "node", NormalizedSymptom: result.RuleID, State: "firing", NativeIdentity: finding.Hash([]string{e.ResourceCanonicalID, row.Time, row.Message, result.RuleID}), IndependenceGroup: e.ResourceCanonicalID + "/kernel", ObservedAt: clock, TimeReliable: true, QueryTemplateVersion: "sp05-npd/v1", Data: raw, EvidenceRefs: []string{e.EvidenceID}})
		}
	}
	return out, nil
}
