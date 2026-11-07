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
	// Admit only reviewed executable declarations, including native post-check sets.
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
	if name == "node-failure" || name == "pvc-csi-failure" {
		version = "v2"
	}
	return BuiltinVersion(name, version)
}

// Historical executable predicate sets remain readable and replayable. A new
// signed activation is required before the Worker consumes Node v2.
func BuiltinVersion(name, version string) (Recipe, error) {
	if version != "v1" && !((name == "node-failure" || name == "pvc-csi-failure") && version == "v2") {
		return Recipe{}, ErrRecipe
	}
	if version == "v1" {
		if entry := map[string][]string{"pod-health": {"Pod"}, "deployment-health": {"Deployment"}, "service-endpoints-health": {"Service", "EndpointSlice"}}[name]; entry != nil {
			r := Recipe{SchemaVersion: "recipe-registry/v2", Name: name, Version: version, CandidateType: "native_post_check", Entry: entry, GraphDepth: 1, GraphPlan: "single-query/v1", RequiredEvidence: []Requirement{{Key: "target-state", Predicate: "native-target/v1", MaxAgeSeconds: 300, Required: true}, {Key: "related-state", Predicate: "native-relations/v1", MaxAgeSeconds: 300, Required: true}, {Key: "recent-warnings", Predicate: "native-warnings/v1", MaxAgeSeconds: 300, Required: true}}, OptionalEvidence: []string{}, Confirm: []string{"target-state", "related-state", "recent-warnings"}, Exclude: []string{"incomplete-snapshot", "identity-changed", "unreliable-clock"}, ToolAllowlist: []string{"read_evidence"}, PostCheck: "native-related-health/v1"}
			r.Budget.MaxEvidence, r.Budget.MaxNodes, r.Budget.MaxEdges, r.Budget.TimeoutMs = 40, 40, 40, 3000
			return r, nil
		}
	}
	candidate := map[string]string{"dimm-failure": "dimm_hardware_fault", "pvc-csi-failure": "csi_provisioner_failure", "node-failure": "node_runtime_failure", "pod-image-pull-failure": "pod_image_pull_failure", "service-no-endpoints": "service_no_ready_endpoints", "pod-crashloop-failure": "pod_crash_loop", "pod-oom-failure": "pod_oom_killed", "pod-probe-failure": "pod_probe_failure", "deployment-rollout-stuck": "deployment_rollout_stuck", "pod-scheduling-failure": "pod_scheduling_failure"}[name]
	if candidate == "" {
		return Recipe{}, ErrRecipe
	}
	keys := map[string][]string{"dimm-failure": {"dimm-health", "ecc-uncorrectable", "hardware-path"}, "pvc-csi-failure": {"pvc-pending", "csi-error", "storage-path"}, "node-failure": {"node-not-ready", "kernel-fault", "node-path"}, "pod-image-pull-failure": {"pod-pull-failing", "image-pull-error", "pod-path"}, "service-no-endpoints": {"service-no-ready-endpoints", "backend-unready", "service-selection"}, "pod-crashloop-failure": {"pod-crash-failing", "container-backoff-error", "pod-path"}, "pod-oom-failure": {"oom-killed", "oom-exit-observed", "pod-path"}, "pod-probe-failure": {"probe-failing", "probe-kind-observed", "pod-path"}, "deployment-rollout-stuck": {"rollout-stuck", "blocking-pod", "rollout-path"}, "pod-scheduling-failure": {"pod-unschedulable", "scheduling-blocked", "scheduling-cause-detail"}}[name]
	r := Recipe{SchemaVersion: "recipe-registry/v2", Name: name, Version: version, CandidateType: candidate, Entry: map[string][]string{"dimm-failure": {"DIMM", "Node"}, "pvc-csi-failure": {"PersistentVolumeClaim", "Pod"}, "node-failure": {"Node", "Pod"}, "pod-image-pull-failure": {"Pod", "Event"}, "service-no-endpoints": {"Service", "EndpointSlice"}, "pod-crashloop-failure": {"Pod", "Event"}, "pod-oom-failure": {"Pod"}, "pod-probe-failure": {"Pod", "Event"}, "deployment-rollout-stuck": {"Deployment", "Pod"}, "pod-scheduling-failure": {"Pod", "Event"}}[name], GraphDepth: 2, GraphPlan: "single-query/v1", OptionalEvidence: []string{"metric-context", "recent-change"}, Confirm: append([]string(nil), keys...), Exclude: []string{"healthy-primary", "conflicting-required-facts", "invalid-path", "unreliable-clock"}, ToolAllowlist: []string{"read_evidence", "diagnostic_graph", "impact_scope"}, PostCheck: "fresh-required-evidence/v1"}
	if name == "pvc-csi-failure" && version == "v2" {
		r.PostCheck = "native-related-health/v1"
	}
	if name == "pod-image-pull-failure" || name == "service-no-endpoints" || name == "pod-crashloop-failure" || name == "pod-oom-failure" || name == "pod-probe-failure" || name == "deployment-rollout-stuck" || name == "pod-scheduling-failure" {
		r.PostCheck = "native-related-health/v1"
	}
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
