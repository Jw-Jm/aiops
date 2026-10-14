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
	"slices"
	"sort"
	"strings"
	"time"
)

type Definition struct {
	Version  string `json:",omitempty"`
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
	version := d.Version
	if version == "" {
		version = "v2"
	}
	return contract.Validate("https://ops.local/schemas/"+d.Name+"/"+version, args)
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

func OutputSchema(versions ...string) (map[string]any, error) {
	version := "v2"
	if len(versions) > 0 {
		version = versions[0]
	}
	if version != "v2" && version != "v3" {
		return nil, investigation.ErrInvalid
	}
	b, err := api.Schemas.ReadFile("schemas/tool-response-" + version + ".schema.json")
	if err != nil {
		return nil, err
	}
	var s map[string]any
	err = json.Unmarshal(b, &s)
	return s, err
}
func ValidateOutput(b []byte) error {
	var envelope struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if json.Unmarshal(b, &envelope) != nil || (envelope.SchemaVersion != "tool-response/v2" && envelope.SchemaVersion != "tool-response/v3") {
		return investigation.ErrInvalid
	}
	return contract.Validate("https://ops.local/schemas/"+envelope.SchemaVersion, b)
}

// CatalogFor freezes the v2 catalog and restricts v3 tools by task and authorization domain.
func CatalogFor(schema, task, domain, version string) ([]Definition, error) {
	if schema == "investigation-job/v2" {
		return Catalog()
	}
	if schema != "investigation-job/v3" || version != "assistant-tools/v1" {
		return nil, investigation.ErrInvalid
	}
	names := []string{"get_assistant_context"}
	if task == "data_query" {
		names = append(names, "describe_analysis_dataset", "execute_analysis_query")
	} else if domain == "operational" && (task == "incident_followup" || task == "resource_troubleshooting") {
		names = append(names, "describe_analysis_dataset", "execute_analysis_query", "evaluate_fault_chain")
		names = append(names, "resolve_resources", "resource_findings")
		if task == "incident_followup" {
			names = append(names, "get_formal_references")
		}
		if task == "resource_troubleshooting" {
			names = append(names, "get_resource_investigation_context")
		}
	} else {
		return nil, investigation.ErrDenied
	}
	if domain != "operational" && domain != "audit" {
		return nil, investigation.ErrDenied
	}
	out := []Definition{}
	for _, name := range names {
		b, err := api.Schemas.ReadFile("assistant-tools/" + name + "-v3.schema.json")
		if err != nil {
			return nil, err
		}
		var s map[string]any
		if json.Unmarshal(b, &s) != nil {
			return nil, investigation.ErrInvalid
		}
		s, err = inlineAssistantSchema(s)
		if err != nil {
			return nil, err
		}
		out = append(out, Definition{Name: name, Schema: s, Version: "v3", RawQuery: name == "execute_analysis_query"})
	}
	if task != "data_query" {
		old, err := Catalog()
		if err != nil {
			return nil, err
		}
		for _, d := range old {
			if d.Disabled != "" {
				continue
			}
			if task == "resource_troubleshooting" && slices.Contains([]string{"get_incident_context", "get_findings", "get_ranked_evidence", "get_evidence_conflicts", "get_recent_changes", "get_root_cause_candidates"}, d.Name) {
				continue
			}
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func ForJob(j investigation.Job) ([]Definition, error) {
	if j.Assistant == nil {
		return Catalog()
	}
	return CatalogFor(j.SchemaVersion, j.Assistant.TaskKind, j.Assistant.AuthorizationDomain, j.Assistant.CatalogVersion)
}
