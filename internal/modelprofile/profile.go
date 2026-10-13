// Package modelprofile freezes the independently admitted assistant model material.
package modelprofile

import (
	"encoding/json"
	"net"
	"net/url"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"strings"
)

type Profile struct {
	SchemaVersion           string   `json:"schemaVersion"`
	Version                 string   `json:"version"`
	ModelID                 string   `json:"modelID"`
	ModelVersion            string   `json:"modelVersion"`
	BaseURL                 string   `json:"baseURL"`
	APIKeyRef               string   `json:"apiKeyRef"`
	ContextWindow           int      `json:"contextWindow"`
	OutputTokenLimit        int      `json:"outputTokenLimit"`
	TokenAccounting         string   `json:"tokenAccounting"`
	CostPolicy              string   `json:"costPolicy"`
	ToolCalling             bool     `json:"toolCalling"`
	StructuredOutput        bool     `json:"structuredOutput"`
	TaskKinds               []string `json:"taskKinds"`
	TimeoutSeconds          int      `json:"timeoutSeconds"`
	Retries                 int      `json:"retries"`
	LicenseSPDX             string   `json:"licenseSPDX"`
	WeightsDigest           string   `json:"weightsDigest"`
	EvaluationVersion       string   `json:"evaluationVersion"`
	EvaluationDigest        string   `json:"evaluationDigest"`
	AdmissionEvidenceDigest string   `json:"admissionEvidenceDigest"`
	AdmissionState          string   `json:"admissionState"`
}

// Reference includes every field, including endpoint, cost and evaluation proof.
// Canonical map encoding is shared with the Python dispatcher validation.
func (p Profile) Reference() string {
	b, _ := json.Marshal(p)
	var canonical map[string]any
	json.Unmarshal(b, &canonical)
	return p.Version + "@" + graph.ScopeDigest(canonical)
}
func (p Profile) Valid(listen string) bool {
	b, _ := json.Marshal(p)
	if contract.Validate("https://ops.local/schemas/assistant-model-profile/v1", b) != nil || p.OutputTokenLimit >= p.ContextWindow {
		return false
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/v1" || strings.ContainsAny(p.BaseURL, "?#") {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	if (ip == nil || (!ip.IsPrivate() && !ip.IsLoopback())) && !strings.HasSuffix(u.Hostname(), ".svc.cluster.local") {
		return false
	}
	if p.AdmissionState == "admitted" {
		return true
	}
	// An explicitly labelled fixture can run only in the isolated loopback smoke
	// harness. Helm never admits it. Pending material cannot run anywhere.
	host, _, err := net.SplitHostPort(listen)
	address := net.ParseIP(host)
	return p.AdmissionState == "fixture_only" && err == nil && address != nil && address.IsLoopback() && ip != nil && ip.IsLoopback()
}
