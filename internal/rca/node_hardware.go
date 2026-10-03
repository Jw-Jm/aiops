package rca

import (
	"encoding/json"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/inspection/hardware"
	api "ops-platform/internal/upstream/ontology/api"
	"ops-platform/internal/upstream/ontology/service/diagnostic"
	"regexp"
	"slices"
	"strings"
	"time"
)

func admissibleNativeEvidence(e evidence.Evidence, in Input) bool {
	return e.TimeReliable && !e.ObservedTo.After(in.EvaluatedAt) && !e.ObservedTo.Before(in.EvaluatedAt.Add(-300*time.Second)) && !e.ObservedFrom.After(in.To) && !e.ObservedTo.Before(in.From) && e.ReplayState == "archived_verified" && e.ContentDigest == evidence.Digest(e.Data) && e.IndependenceGroup != "" && len(e.DerivationEvidenceRefs) == 0
}

// A topology neighbor alone is a hypothesis. The v2 predicate requires a native
// atomic CPER record naming the same unique DeviceLocator, the locked NPD fatal
// rule, current uncorrectable ECC and DIMM health, and the reviewed hosted path.
// Separate log rows are never concatenated into a purported causal record.
func fatalDIMMRecord(e evidence.Evidence, in Input, locator string) bool {
	if locator == "" || len(locator) > 128 || strings.ContainsAny(locator, "\n\r\t") || !admissibleNativeEvidence(e, in) || e.ResourceCanonicalID != in.ResourceCanonicalID || e.SourceSystem != "victorialogs" || e.Type != "log" || e.QueryTemplateVersion != "node-kernel-logs/v1" || len(e.Data) > 64<<10 {
		return false
	}
	var rows []struct {
		Clock     string `json:"_time"`
		Transport string `json:"_TRANSPORT"`
		Message   string `json:"_msg"`
	}
	if json.Unmarshal(e.Data, &rows) != nil || len(rows) > 64 {
		return false
	}
	location := regexp.MustCompile(`(?m)^.*\[Hardware Error\]: DIMM location: [^\s]+ ` + regexp.QuoteMeta(locator) + `[ \t]*$`)
	for _, row := range rows {
		clock, err := time.Parse(time.RFC3339Nano, row.Clock)
		if row.Transport != "kernel" || err != nil || clock.Before(e.ObservedFrom) || clock.After(e.ObservedTo) || len(row.Message) > 4096 || !locatedFatalMemorySection(row.Message, location) {
			continue
		}
		results, err := hardware.MatchKernelRecord(row.Message)
		if err == nil && slices.ContainsFunc(results, func(r hardware.CheckResult) bool { return !r.Degraded && r.RuleID == "CperHardwareErrorFatal" }) {
			return true
		}
	}
	return false
}

func applyNodeHardwareHypotheses(r Recipe, in Input, keys map[string][]string, out *Result) {
	if len(keys["node-not-ready"]) == 0 {
		return
	}
	runtimeCause := len(keys["node-causal-evidence"]) > 0
	locators := map[string]int{}
	for _, n := range in.Graph.Nodes {
		if n.Kind == "DIMM" {
			if location, _ := n.Attributes["DeviceLocator"].(string); location != "" {
				locators[location]++
			}
		}
	}
	hardwareCandidates := []Candidate{}
	hardwareSupport := []string{}
	causalRefs := []string{}
	for _, n := range in.Graph.Nodes {
		if n.Kind != "DIMM" {
			continue
		}
		locator, _ := n.Attributes["DeviceLocator"].(string)
		if locators[locator] != 1 {
			continue
		}
		refs := []string{}
		for _, e := range in.Evidence {
			if fatalDIMMRecord(e, in, locator) {
				refs = append(refs, e.EvidenceID)
			}
		}
		if len(refs) == 0 {
			continue
		}
		dimmRecipe, _ := Builtin("dimm-failure")
		hardwareInput := in
		hardwareInput.ResourceCanonicalID = n.CanonicalID
		result, err := Evaluate(dimmRecipe, hardwareInput)
		if err != nil || result.Status != "confirmed" {
			continue
		}
		// The generic DIMM Recipe may reach a different Node. Fence the exact
		// component_of -> hosts endpoint to this NotReady subject as well.
		reaches := false
		for _, component := range in.Graph.Edges {
			if component.From == n.CanonicalID && component.Kind == "component_of" {
				for _, host := range in.Graph.Edges {
					if host.From == component.To && host.To == in.ResourceCanonicalID && host.Kind == "hosts" {
						reaches = true
					}
				}
			}
		}
		if !reaches {
			continue
		}
		refs = append(refs, result.Bundle.Supporting...)
		refs = append(refs, keys["node-not-ready"]...)
		slices.Sort(refs)
		refs = slices.Compact(refs)
		hardwareCandidates = append(hardwareCandidates, Candidate{Key: finding.Hash([]string{r.Name, r.Version, "dimm_hardware_fault", n.CanonicalID}), Type: "dimm_hardware_fault", ResourceCanonicalID: n.CanonicalID, Source: "deterministic", EvidenceRefs: refs, Provenance: []string{r.Name + "/" + r.Version, "node-runtime-or-located-fatal-dimm/v2", "ontology/346236312685a2e2e824c46b9ecdb3e8e8a10c76"}})
		hardwareSupport = append(hardwareSupport, result.Bundle.Supporting...)
		causalRefs = append(causalRefs, refs...)
	}
	if len(hardwareCandidates) == 0 {
		return
	}
	out.Candidates = append(out.Candidates, hardwareCandidates...)
	out.Bundle.Supporting = append(out.Bundle.Supporting, hardwareSupport...)
	out.Bundle.Supporting = append(out.Bundle.Supporting, causalRefs...)
	slices.Sort(out.Bundle.Supporting)
	out.Bundle.Supporting = slices.Compact(out.Bundle.Supporting)
	out.Bundle.Missing = slices.DeleteFunc(out.Bundle.Missing, func(k string) bool { return k == "node-causal-evidence" })
	nodes := []api.DiagnosticNode{}
	sortedEvidence := slices.Clone(in.Evidence)
	slices.SortFunc(sortedEvidence, func(a, b evidence.Evidence) int { return strings.Compare(a.EvidenceID, b.EvidenceID) })
	seenRankIDs := map[string]bool{}
	seenRankGroups := map[string]bool{}
	for _, e := range sortedEvidence {
		if slices.Contains(out.Bundle.Supporting, e.EvidenceID) && !seenRankIDs[e.EvidenceID] && !seenRankGroups[e.IndependenceGroup] {
			seenRankIDs[e.EvidenceID] = true
			seenRankGroups[e.IndependenceGroup] = true
			nodes = append(nodes, api.DiagnosticNode{CanonicalID: e.EvidenceID, Kind: api.NodeKindEvent, Attributes: map[string]any{"reason": "FailureEvidence", "message": "node-causal-evidence", "lastTimestamp": e.ObservedTo.Format(time.RFC3339Nano)}})
		}
	}
	out.Bundle.Ranked = diagnostic.RankEvidence(nodes, nil)
	for i := range out.Candidates {
		for _, ranked := range out.Bundle.Ranked {
			if slices.Contains(out.Candidates[i].EvidenceRefs, ranked.NodeID) {
				out.Candidates[i].Score = ranked.Score
				break
			}
		}
	}
	out.Partial = in.Graph.Partial || len(out.Bundle.Missing) > 0 || len(out.Bundle.DegradedSources) > 0 || len(out.Bundle.Conflicts) > 0 || len(out.Bundle.Contradicting) > 0
	if len(hardwareCandidates) > 1 || runtimeCause {
		out.Status = "unresolved"
		out.Partial = true
		out.Bundle.Conflicts = append(out.Bundle.Conflicts, api.DiagnosticConflict{Code: "competing_node_causes", Message: "independent native runtime or located hardware causes require resolution", NodeIDs: slices.Clone(out.Bundle.Supporting), Confidence: "conflicting"})
	} else if !out.Partial {
		out.Status = "confirmed"
	} else {
		out.Status = "unresolved"
	}
	slices.SortFunc(out.Candidates, func(a, b Candidate) int { return strings.Compare(a.Key, b.Key) })
}

// CPER's event severity can be fatal because of another section. Require the
// located memory section itself to be fatal; section boundaries are native
// Error N,type markers from the Linux CPER wire format.
func locatedFatalMemorySection(message string, location *regexp.Regexp) bool {
	marker := regexp.MustCompile(`\[Hardware Error\]:[ \t]+Error [0-9]+, type: (fatal|recoverable|corrected|info)[ \t]*$`)
	fatal, memory := false, false
	for _, line := range strings.Split(message, "\n") {
		if strings.Contains(line, "[Hardware Error]:") && strings.Contains(line, "Error ") {
			matched := marker.FindStringSubmatch(line)
			fatal = matched != nil && matched[1] == "fatal"
			memory = false
			continue
		}
		if strings.Contains(line, "[Hardware Error]:") && strings.Contains(line, "section_type:") {
			memory = strings.HasSuffix(strings.TrimSpace(line), "section_type: memory error")
			continue
		}
		if fatal && memory && location.MatchString(line) {
			return true
		}
	}
	return false
}
