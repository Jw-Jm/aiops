package rca

import (
	"encoding/json"
	"fmt"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/inspection/hardware"
	"ops-platform/internal/resource"
	api "ops-platform/internal/upstream/ontology/api"
	"ops-platform/internal/upstream/ontology/service/diagnostic"
	"slices"
	"strings"
	"time"
)

type Input struct {
	SchemaVersion       string                  `json:"schemaVersion"`
	FindingRevisions    []FindingInput          `json:"findingRevisions"`
	GraphSources        []graph.SourceAuthority `json:"graphSources"`
	ResourceCanonicalID string                  `json:"resourceCanonicalId"`
	Evidence            []evidence.Evidence     `json:"evidence"`
	Graph               graph.Result            `json:"graph"`
	From                time.Time               `json:"from"`
	To                  time.Time               `json:"to"`
	EvaluatedAt         time.Time               `json:"evaluatedAt"`
}
type Bundle struct {
	Supporting      []string                 `json:"supporting"`
	Contradicting   []string                 `json:"contradicting"`
	Missing         []string                 `json:"missing"`
	DegradedSources []string                 `json:"degradedSources"`
	Ranked          []api.RankedEvidence     `json:"ranked"`
	Conflicts       []api.DiagnosticConflict `json:"conflicts"`
}
type Candidate struct {
	Key                 string   `json:"candidateKey"`
	Type                string   `json:"candidateType"`
	ResourceCanonicalID string   `json:"resourceCanonicalId"`
	Source              string   `json:"source"`
	EvidenceRefs        []string `json:"evidenceRefs"`
	Score               float64  `json:"score"`
	Provenance          []string `json:"provenance"`
}
type Result struct {
	SchemaVersion string       `json:"schemaVersion"`
	Status        string       `json:"status"`
	Partial       bool         `json:"partial"`
	Candidates    []Candidate  `json:"candidates"`
	Bundle        Bundle       `json:"evidenceBundle"`
	RecipeVersion string       `json:"recipeVersion"`
	RecipeDigest  string       `json:"recipeDigest"`
	Impact        graph.Result `json:"impact"`
	GraphPlan     string       `json:"graphPlan"`
}

func Evaluate(r Recipe, input Input) (Result, error) {
	baseline, err := BuiltinVersion(r.Name, r.Version)
	if err != nil || !sameJSON(baseline, r) {
		return Result{}, ErrRecipe
	}
	out := Result{SchemaVersion: "rca-evaluation/v2", Status: "unresolved", Partial: input.Graph.Partial, RecipeVersion: r.Version, RecipeDigest: finding.Hash(r), Impact: input.Graph, GraphPlan: r.GraphPlan, Bundle: Bundle{Supporting: []string{}, Contradicting: []string{}, Missing: []string{}, DegradedSources: append([]string{}, input.Graph.DegradedSources...), Ranked: []api.RankedEvidence{}, Conflicts: []api.DiagnosticConflict{}}}
	if len(input.Evidence) > r.Budget.MaxEvidence {
		return out, fmt.Errorf("BUDGET_EXHAUSTED")
	}
	if input.From.IsZero() || input.To.Before(input.From) || input.EvaluatedAt.IsZero() {
		return out, fmt.Errorf("INVALID_ARGUMENT")
	}
	keys := map[string][]string{}
	nodes := []api.DiagnosticNode{}
	seen := map[string]string{}
	independent := map[string]bool{}
	es := append([]evidence.Evidence(nil), input.Evidence...)
	slices.SortFunc(es, func(a, b evidence.Evidence) int {
		if a.EvidenceID < b.EvidenceID {
			return -1
		}
		if a.EvidenceID > b.EvidenceID {
			return 1
		}
		return 0
	})
	for _, e := range es {
		if digest, ok := seen[e.EvidenceID]; ok {
			if digest != e.ContentDigest {
				out.Bundle.DegradedSources = append(out.Bundle.DegradedSources, "evidence-digest-conflict")
			}
			continue
		}
		seen[e.EvidenceID] = e.ContentDigest
		if !e.TimeReliable || e.ObservedTo.After(input.EvaluatedAt) || e.ObservedTo.Before(input.EvaluatedAt.Add(-300*time.Second)) || e.ObservedFrom.After(input.To) || e.ObservedTo.Before(input.From) || e.ReplayState != "archived_verified" || e.ContentDigest != evidence.Digest(e.Data) || e.IndependenceGroup == "" {
			out.Bundle.DegradedSources = append(out.Bundle.DegradedSources, "evidence/"+e.EvidenceID)
			continue
		}
		// All three recipes have concrete predicates. No expression language,
		// correlation-to-causation shortcut or general scoring engine exists here.
		matched, contradicts := recipeFacts(r.Name, e, input.ResourceCanonicalID, input.EvaluatedAt)
		if r.Name == "pvc-csi-failure" && slices.Contains(matched, "csi-error") && !csiSourceMatches(input, e) {
			matched = nil
		}
		if contradicts {
			out.Bundle.Contradicting = append(out.Bundle.Contradicting, e.EvidenceID)
			continue
		}
		if len(matched) == 0 {
			continue
		}
		if len(e.DerivationEvidenceRefs) > 0 {
			// Derived Candidate archives are useful provenance, but never another
			// independent confirmation vote. If the native parent is absent, the
			// required fact remains Missing; its present duplicate is not an outage.
			continue
		}
		group := e.IndependenceGroup
		if independent[group] {
			continue
		}
		independent[group] = true
		for _, key := range matched {
			if r.Name == "node-failure" && r.Version == "v2" && key == "kernel-fault" {
				key = "node-causal-evidence"
			}
			keys[key] = append(keys[key], e.EvidenceID)
		}
		out.Bundle.Supporting = append(out.Bundle.Supporting, e.EvidenceID)
		nodes = append(nodes, api.DiagnosticNode{CanonicalID: e.EvidenceID, Kind: api.NodeKindEvent, Attributes: map[string]any{"reason": "FailureEvidence", "message": r.CandidateType, "lastTimestamp": e.ObservedTo.Format(time.RFC3339Nano)}})
	}
	pathKey := map[string]string{"dimm-failure": "hardware-path", "pvc-csi-failure": "storage-path", "node-failure": "node-path"}[r.Name]
	if recipePath(r.Name, input) {
		keys[pathKey] = append([]string{}, out.Bundle.Supporting...)
	}
	for _, req := range r.RequiredEvidence {
		if len(keys[req.Key]) == 0 {
			out.Bundle.Missing = append(out.Bundle.Missing, req.Key)
		}
	}
	out.Bundle.Ranked = append([]api.RankedEvidence{}, diagnostic.RankEvidence(nodes, nil)...)
	out.Bundle.Conflicts = append([]api.DiagnosticConflict{}, diagnostic.EvidenceConflicts(nodes, nil)...)
	if len(out.Bundle.Contradicting) > 0 {
		out.Bundle.Conflicts = append(out.Bundle.Conflicts, api.DiagnosticConflict{Code: "recipe_condition_conflict", Message: "required recipe facts contradicted", NodeIDs: append([]string(nil), out.Bundle.Contradicting...), Confidence: "conflicting"})
	}
	slices.Sort(out.Bundle.DegradedSources)
	out.Bundle.DegradedSources = slices.Compact(out.Bundle.DegradedSources)
	out.Partial = out.Partial || len(out.Bundle.Missing) > 0 || len(out.Bundle.DegradedSources) > 0 || len(out.Bundle.Conflicts) > 0
	if len(out.Bundle.Supporting) > 0 && !(r.Name == "node-failure" && r.Version == "v2" && len(keys["node-causal-evidence"]) == 0) {
		candidate := Candidate{Key: finding.Hash([]string{r.Name, r.Version, r.CandidateType, input.ResourceCanonicalID}), Type: r.CandidateType, ResourceCanonicalID: input.ResourceCanonicalID, Source: "deterministic", EvidenceRefs: append([]string{}, out.Bundle.Supporting...), Provenance: []string{r.Name + "/" + r.Version, "ontology/346236312685a2e2e824c46b9ecdb3e8e8a10c76"}}
		if len(out.Bundle.Ranked) > 0 {
			candidate.Score = out.Bundle.Ranked[0].Score
		}
		out.Candidates = []Candidate{candidate}
	}
	if !out.Partial && len(out.Bundle.Contradicting) == 0 && len(out.Bundle.Conflicts) == 0 && len(out.Bundle.Missing) == 0 && len(out.Bundle.Supporting) >= 2 {
		out.Status = "confirmed"
	} else if !out.Partial && len(out.Bundle.Contradicting) == 0 && len(out.Bundle.Supporting) > 0 {
		out.Status = "probable"
	}
	if out.Candidates == nil {
		out.Candidates = []Candidate{}
	}
	if r.Name == "node-failure" && r.Version == "v2" {
		applyNodeHardwareHypotheses(r, input, keys, &out)
	}
	return out, nil
}
func recipeFacts(name string, e evidence.Evidence, target string, evaluatedAt time.Time) ([]string, bool) {
	if e.ResourceCanonicalID != target {
		return nil, false
	}
	if name == "node-failure" && e.SourceSystem == "victorialogs" && e.Type == "log" && e.QueryTemplateVersion == "node-kernel-logs/v1" {
		var rows []struct {
			Time      string `json:"_time"`
			Transport string `json:"_TRANSPORT"`
			Message   string `json:"_msg"`
		}
		if len(e.Data) > 64<<10 || json.Unmarshal(e.Data, &rows) != nil || len(rows) > 64 {
			return nil, false
		}
		for _, row := range rows {
			clock, err := time.Parse(time.RFC3339Nano, row.Time)
			if row.Transport != "kernel" || err != nil || clock.Before(e.ObservedFrom) || clock.After(e.ObservedTo) || clock.Before(evaluatedAt.Add(-5*time.Minute)) || clock.After(evaluatedAt) {
				continue
			}
			matched, err := hardware.MatchKernelRecord(row.Message)
			if err != nil {
				continue
			}
			for _, rule := range matched {
				if !rule.Degraded && (rule.RuleID == "KernelOops" || rule.RuleID == "KernelDeadlock") {
					return []string{"kernel-fault"}, false
				}
			}
		}
		return nil, false
	}
	var raw map[string]any
	if json.Unmarshal(e.Data, &raw) != nil {
		return nil, false
	}
	// The source adapter publishes these bounded typed facts from actual native
	// objects. Only versioned SP05 templates and native projection are consumed.
	if e.ResourceCanonicalID != target {
		return nil, false
	}
	allowed := e.QueryTemplateVersion == "sp05-signal/v1" || e.QueryTemplateVersion == "sp05-hardware/v1" || e.QueryTemplateVersion == "sp05-npd/v1"
	if !allowed {
		return nil, false
	}
	if name == "dimm-failure" && e.SourceSystem != "redfish" {
		return nil, false
	}
	if name != "dimm-failure" && e.SourceSystem != "kubernetes" && !(name == "node-failure" && e.SourceSystem == "victorialogs" && e.QueryTemplateVersion == "sp05-npd/v1") {
		return nil, false
	}
	switch name {
	case "dimm-failure":
		if raw["health"] == "OK" {
			return nil, true
		}
		if raw["health"] == "Critical" {
			return []string{"dimm-health"}, false
		}
		if raw["uncorrectableECC"] == true {
			return []string{"ecc-uncorrectable"}, false
		}
	case "pvc-csi-failure":
		if raw["phase"] == "Bound" {
			return nil, true
		}
		if raw["phase"] == "Pending" {
			return []string{"pvc-pending"}, false
		}
		if raw["reason"] == "ProvisioningFailed" || raw["reason"] == "FailedMount" || raw["reason"] == "FailedAttachVolume" {
			return []string{"csi-error"}, false
		}
	case "node-failure":
		if raw["ready"] == true {
			return nil, true
		}
		if raw["ready"] == false {
			return []string{"node-not-ready"}, false
		}
	}
	return nil, false
}

// These checks validate only fixed Recipe path shapes returned by the bounded
// Ariadne/ontology query; graph discovery and traversal stay in that kernel.
func recipePath(name string, input Input) bool {
	if input.Graph.Partial || input.Graph.Freshness != "fresh" {
		return false
	}
	kinds := map[string]string{}
	for _, n := range input.Graph.Nodes {
		kinds[n.CanonicalID] = n.Kind
	}
	edges := []resource.Relation{}
	for _, e := range input.Graph.Edges {
		if e.ValidFrom.IsZero() || e.ObservedAt.IsZero() || e.Provenance.SourceRegistrationID == "" || e.Provenance.RuleVersion == "" || e.Provenance.ObservedAt.IsZero() || e.Confidence < 1 || e.ValidFrom.After(input.To) || (e.ValidTo != nil && !e.ValidTo.After(input.From)) {
			continue
		}
		if kinds[e.From] == "" || kinds[e.To] == "" {
			continue
		}
		edges = append(edges, e)
	}
	target := input.ResourceCanonicalID
	switch name {
	case "node-failure":
		if kinds[target] != "Node" {
			return false
		}
		for _, e := range edges {
			if e.Kind == "scheduled_on" && e.To == target && kinds[e.From] == "Pod" {
				return true
			}
		}
	case "pvc-csi-failure":
		if kinds[target] != "PersistentVolumeClaim" {
			return false
		}
		mounted, storage, driver := false, false, false
		for _, e := range edges {
			if e.Kind == "mounts_pvc" && e.To == target && kinds[e.From] == "Pod" {
				mounted = true
			}
			if e.Kind == "uses_storage_class" && e.From == target && kinds[e.To] == "StorageClass" {
				storage = true
				for _, related := range edges {
					if related.From == e.To && related.Kind == "provisioned_by_csi_driver" && kinds[related.To] == "CSIDriver" {
						driver = true
					}
				}
			}
		}
		return mounted && storage && driver
	case "dimm-failure":
		if kinds[target] != "DIMM" {
			return false
		}
		for _, component := range edges {
			if component.Kind != "component_of" || component.From != target || kinds[component.To] != "PhysicalServer" {
				continue
			}
			for _, host := range edges {
				if host.Kind != "hosts" || host.From != component.To || kinds[host.To] != "Node" {
					continue
				}
				for _, pod := range edges {
					if pod.Kind == "scheduled_on" && pod.To == host.To && kinds[pod.From] == "Pod" {
						return true
					}
				}
			}
		}
	}
	return false
}

// A generic provisioning Event proves a storage symptom, not a CSI cause.
// Match its native controller identity to the native SC -> CSIDriver reference
// returned by Ariadne. External-provisioner suffixes preserve the driver name.
func csiSourceMatches(input Input, e evidence.Evidence) bool {
	var fact struct {
		Reporter string `json:"reportingController"`
	}
	if json.Unmarshal(e.Data, &fact) != nil || fact.Reporter == "" {
		return false
	}
	classes := []string{}
	for _, edge := range input.Graph.Edges {
		if edge.From == input.ResourceCanonicalID && edge.Kind == "uses_storage_class" {
			classes = append(classes, edge.To)
		}
	}
	for _, edge := range input.Graph.Edges {
		if edge.Kind != "provisioned_by_csi_driver" || !slices.Contains(classes, edge.From) {
			continue
		}
		for _, node := range input.Graph.Nodes {
			if node.CanonicalID == edge.To && node.Kind == "CSIDriver" && node.Name != "" && (fact.Reporter == node.Name || strings.HasPrefix(fact.Reporter, node.Name+"_")) {
				return true
			}
		}
	}
	return false
}
