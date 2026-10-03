package rca

import (
	"encoding/json"
	"errors"
	"ops-platform/internal/contract"
)

var ErrRecipe = errors.New("RECIPE_UNAVAILABLE")

type Requirement struct {
	Key           string `json:"key"`
	Predicate     string `json:"predicate"`
	MaxAgeSeconds int    `json:"maxAgeSeconds"`
	Required      bool   `json:"required"`
}
type Recipe struct {
	SchemaVersion    string        `json:"schemaVersion"`
	Name             string        `json:"name"`
	Version          string        `json:"version"`
	CandidateType    string        `json:"candidateType"`
	Entry            []string      `json:"entry"`
	GraphDepth       int           `json:"graphDepth"`
	GraphPlan        string        `json:"graphPlan"`
	RequiredEvidence []Requirement `json:"requiredEvidence"`
	OptionalEvidence []string      `json:"optionalEvidence"`
	Confirm          []string      `json:"confirm"`
	Exclude          []string      `json:"exclude"`
	ToolAllowlist    []string      `json:"toolAllowlist"`
	Budget           struct {
		MaxEvidence int `json:"maxEvidence"`
		MaxNodes    int `json:"maxNodes"`
		MaxEdges    int `json:"maxEdges"`
		TimeoutMs   int `json:"timeoutMs"`
	} `json:"budget"`
	PostCheck string `json:"postCheck"`
}

func DecodeRecipe(raw []byte) (Recipe, error) {
	var r Recipe
	if err := contract.Validate("https://ops.local/schemas/recipe-registry/v2", raw); err != nil {
		return r, ErrRecipe
	}
	if json.Unmarshal(raw, &r) != nil {
		return r, ErrRecipe
	}
	baseline, err := BuiltinVersion(r.Name, r.Version)
	if err != nil {
		return r, err
	}
	// This phase admits only these three reviewed executable predicate sets.
	// Arbitrary new predicates or tool names need a new reviewed adapter/version.
	if r.Version != baseline.Version || r.CandidateType != baseline.CandidateType || !sameJSON(r, baseline) {
		return r, ErrRecipe
	}
	return r, nil
}
func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func Builtin(name string) (Recipe, error) {
	version := "v1"
	if name == "node-failure" {
		version = "v2"
	}
	return BuiltinVersion(name, version)
}

// Historical executable predicate sets remain readable and replayable. A new
// signed activation is required before the Worker consumes Node v2.
func BuiltinVersion(name, version string) (Recipe, error) {
	if version != "v1" && !(name == "node-failure" && version == "v2") {
		return Recipe{}, ErrRecipe
	}
	candidate := map[string]string{"dimm-failure": "dimm_hardware_fault", "pvc-csi-failure": "csi_provisioner_failure", "node-failure": "node_runtime_failure"}[name]
	if candidate == "" {
		return Recipe{}, ErrRecipe
	}
	keys := map[string][]string{"dimm-failure": {"dimm-health", "ecc-uncorrectable", "hardware-path"}, "pvc-csi-failure": {"pvc-pending", "csi-error", "storage-path"}, "node-failure": {"node-not-ready", "kernel-fault", "node-path"}}[name]
	r := Recipe{SchemaVersion: "recipe-registry/v2", Name: name, Version: version, CandidateType: candidate, Entry: map[string][]string{"dimm-failure": {"DIMM", "Node"}, "pvc-csi-failure": {"PersistentVolumeClaim", "Pod"}, "node-failure": {"Node", "Pod"}}[name], GraphDepth: 2, GraphPlan: "single-query/v1", OptionalEvidence: []string{"metric-context", "recent-change"}, Confirm: append([]string(nil), keys...), Exclude: []string{"healthy-primary", "conflicting-required-facts", "invalid-path", "unreliable-clock"}, ToolAllowlist: []string{"read_evidence", "diagnostic_graph", "impact_scope"}, PostCheck: "fresh-required-evidence/v1"}
	if name == "dimm-failure" {
		r.GraphPlan = "dimm-hosted-pods/v1"
	}
	if name == "node-failure" && version == "v2" {
		keys = []string{"node-not-ready", "node-causal-evidence", "node-path"}
		r.Confirm = append([]string(nil), keys...)
	}
	for _, key := range keys {
		r.RequiredEvidence = append(r.RequiredEvidence, Requirement{Key: key, Predicate: key + "/v1", MaxAgeSeconds: 300, Required: true})
	}
	if name == "node-failure" && version == "v2" {
		r.RequiredEvidence[1].Predicate = "node-runtime-or-located-fatal-dimm/v2"
	}
	r.Budget.MaxEvidence = 64
	r.Budget.MaxNodes = 200
	r.Budget.MaxEdges = 400
	r.Budget.TimeoutMs = 3000
	return r, nil
}
