// Package tools defines the closed semantic investigation tool catalog.
package tools

import (
	"context"
	"encoding/json"
	"ops-platform/api"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/resource"
	"sort"
	"strings"
	"time"
)

type Definition struct {
	Name     string
	Schema   map[string]any
	RawQuery bool
	Disabled string
}

func Catalog() ([]Definition, error) {
	names := []string{"get_incident_context", "get_resource_context", "get_resource_health", "get_dependencies", "get_impact_scope", "get_findings", "get_ranked_evidence", "get_evidence_conflicts", "get_recent_changes", "get_root_cause_candidates", "query_metrics", "query_logs", "query_deepflow", "query_kubernetes", "query_kubevirt", "query_hardware"}
	out := []Definition{}
	for _, n := range names {
		b, err := api.Schemas.ReadFile("mcp/tools/" + n + "-v2.schema.json")
		if err != nil {
			return nil, err
		}
		s := map[string]any{}
		if err = json.Unmarshal(b, &s); err != nil {
			return nil, err
		}
		d := Definition{Name: n, Schema: s, RawQuery: strings.HasPrefix(n, "query_")}
		if n == "query_kubevirt" {
			d.Disabled = "development_deferred; disabled/unverified (ADR0008)"
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func Digest(defs []Definition) string { return graph.ScopeDigest(defs) }
func Names(defs []Definition) []string {
	out := []string{}
	for _, d := range defs {
		if d.Disabled == "" {
			out = append(out, d.Name)
		}
	}
	return out
}
func (d Definition) Validate(args []byte) error {
	return contract.Validate("https://ops.local/schemas/"+d.Name+"/v2", args)
}

type Result struct {
	SchemaVersion   string   `json:"schemaVersion"`
	Tool            string   `json:"tool"`
	State           string   `json:"state"`
	Data            any      `json:"data"`
	EvidenceRefs    []string `json:"evidenceRefs"`
	Partial         bool     `json:"partial"`
	DegradedSources []string `json:"degradedSources"`
	ErrorCode       string   `json:"errorCode,omitempty"`
	Budget          struct {
		Consumed  investigation.Usage `json:"consumed"`
		Remaining investigation.Usage `json:"remaining"`
	} `json:"budget"`
}
type SemanticAPI interface {
	CheckScope(context.Context, investigation.Job, string, json.RawMessage) error
	Invoke(context.Context, investigation.Job, string, json.RawMessage) (Result, error)
}

// CheckArguments runs after schema validation and before any nonce or budget
// reservation. Source authorization is repeated by the existing domain APIs.
func CheckArguments(j investigation.Job, name string, args json.RawMessage) error {
	var p struct {
		IncidentID  string `json:"incidentId"`
		CanonicalID string `json:"canonicalId"`
		ResourceID  string `json:"resourceCanonicalId"`
		TimeRange   *struct {
			From time.Time `json:"from"`
			To   time.Time `json:"to"`
		} `json:"timeRange"`
	}
	if json.Unmarshal(args, &p) != nil {
		return investigation.ErrInvalid
	}
	if p.IncidentID != "" && p.IncidentID != j.IncidentID.String() {
		return investigation.ErrDenied
	}
	id := p.CanonicalID
	if id == "" {
		id = p.ResourceID
	}
	if id != "" {
		parsed, err := resource.ParseCanonicalID(id)
		if err != nil || parsed.Tenant != j.TenantID.String() {
			return investigation.ErrDenied
		}
		if name == "query_kubernetes" && parsed.Domain != "k8s" || name == "query_hardware" && parsed.Domain != "hardware" {
			return investigation.ErrInvalid
		}
	}
	if p.TimeRange != nil && (p.TimeRange.From.IsZero() || !p.TimeRange.To.After(p.TimeRange.From) || p.TimeRange.To.Sub(p.TimeRange.From) > time.Hour || p.TimeRange.To.After(time.Now().Add(time.Minute))) {
		return investigation.ErrInvalid
	}
	return nil
}

func OutputSchema() (map[string]any, error) {
	b, err := api.Schemas.ReadFile("schemas/tool-response-v2.schema.json")
	if err != nil {
		return nil, err
	}
	var s map[string]any
	err = json.Unmarshal(b, &s)
	return s, err
}
func ValidateOutput(b []byte) error {
	return contract.Validate("https://ops.local/schemas/tool-response/v2", b)
}
