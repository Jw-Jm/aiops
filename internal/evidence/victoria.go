package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"math"
	"net/http"
	"net/url"
	"ops-platform/internal/inspection/hardware"
	"ops-platform/internal/resource"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrScopeUnverified = errors.New("SOURCE_SCOPE_UNVERIFIED")
var ErrBudget = errors.New("BUDGET_EXHAUSTED")
var ErrArgument = errors.New("INVALID_ARGUMENT")
var labelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var sensitive = regexp.MustCompile(`(?i)(bearer\s+)[^\s"\\]+|((?:password|passwd|token|secret|api[_-]?key)\s*[=:]\s*)[^\s,;"\\]+|-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*?-----END [^-]*PRIVATE KEY-----`)

var quotedSensitive = regexp.MustCompile(`(?i)(?:password|passwd|token|secret|api[_-]?key)["' ]*[:=]["' ]*[^\s,;"']+`)

func Redact(raw string) string {
	return quotedSensitive.ReplaceAllString(sensitive.ReplaceAllString(raw, "[REDACTED]"), "[REDACTED]")
}
func Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type Victoria struct {
	name      string
	binding   Binding
	client    *http.Client
	authorize Authorizer
	slots     chan struct{}
}

func NewVictoria(name string, b Binding, client *http.Client, authorize Authorizer) (*Victoria, error) {
	if b.SourceType != "" && b.SourceType != name {
		return nil, ErrArgument
	}
	b.SourceType = name
	u, err := url.Parse(b.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || client == nil || authorize == nil || b.Tenant == "" || b.SourceID == "" || b.Revision < 1 || b.BackendLogicalID == "" || (name != "victoriametrics" && name != "victorialogs") {
		return nil, ErrArgument
	}
	// Redirects must not forward backend credentials or scope to another source.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	b.ScopeMapping.RequiredLabels = cloneLabels(b.ScopeMapping.RequiredLabels)
	scopes := map[string][]string{}
	for k, v := range b.ScopeMapping.Scopes {
		scopes[k] = slices.Clone(v)
	}
	b.ScopeMapping.Scopes = scopes
	return &Victoria{name, b, &bounded, authorize, make(chan struct{}, 4)}, nil
}
func cloneLabels(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func (a *Victoria) Name() string { return a.name }
func (a *Victoria) Capabilities(ctx context.Context) (CapabilitySet, error) {
	if err := a.authorize(ctx, a.binding); err != nil {
		return CapabilitySet{"query": "disabled/unverified"}, err
	}
	return CapabilitySet{"query": "verified", "discovery": "disabled"}, nil
}
func (a *Victoria) Query(ctx context.Context, q Query) (Result, error) {
	out := Result{SchemaVersion: "evidence-result/v2", Evidence: []Evidence{}, Freshness: "fresh", DegradedSources: []string{}, Warnings: []string{}}
	id, err := resource.ParseCanonicalID(q.ResourceCanonicalID)
	if err != nil || id.Tenant != a.binding.Tenant || q.Scope.Tenant != a.binding.Tenant || id.Scope != q.Scope.Cluster || !q.Scope.Allows(q.ResourceCanonicalID, q.Namespace) {
		return out, ErrArgument
	}
	if q.From.IsZero() || q.To.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > time.Hour || q.To.After(time.Now().Add(time.Minute)) || q.Limit < 1 || q.Limit > 200 {
		return out, ErrBudget
	}
	if err := a.authorize(ctx, a.binding); err != nil {
		return out, ErrScopeUnverified
	}
	mapping := a.binding.ScopeMapping
	nodeLogs := q.Template == "node-kernel-logs/v1"
	if nodeLogs && (a.name != "victorialogs" || id.Domain != "k8s" || id.APIGroup != "core" || id.Kind != "Node" || q.Namespace != "" || !q.Scope.ClusterScoped) {
		return out, ErrScopeUnverified
	}
	if mapping.RequiredLabels["tenant"] != a.binding.Tenant || !slices.Contains(mapping.Scopes["cluster"], id.Scope) || (!nodeLogs && !slices.Contains(mapping.Scopes["namespace"], q.Namespace)) {
		return out, ErrScopeUnverified
	}
	// This adapter qualifies exact equality labels only. Native accounts or other
	// dimensions require a separately verified backend implementation.
	if mapping.NativeTenant != "" {
		return out, ErrScopeUnverified
	}
	for d := range mapping.Scopes {
		if d != "cluster" && d != "namespace" {
			return out, ErrScopeUnverified
		}
	}
	labels := cloneLabels(mapping.RequiredLabels)
	for k, v := range map[string]string{"cluster": id.Scope, "namespace": q.Namespace, "uid": id.StableID} {
		if prior, ok := labels[k]; ok && prior != v {
			return out, ErrScopeUnverified
		}
		labels[k] = v
	}
	if nodeLogs {
		if prior, ok := labels["_TRANSPORT"]; ok && prior != "kernel" {
			return out, ErrScopeUnverified
		}
		labels["_TRANSPORT"] = "kernel"
	}
	keys := []string{}
	for k := range labels {
		if !labelName.MatchString(k) {
			return out, ErrScopeUnverified
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	predicates := []string{}
	for _, key := range keys {
		predicates = append(predicates, key+"="+strconv.Quote(labels[key]))
	}
	selector := "{" + strings.Join(predicates, ",") + "}"
	path := ""
	query := ""
	switch a.name {
	case "victoriametrics":
		metric := ""
		switch q.Template {
		case "pod-phase/v1":
			metric = "kube_pod_status_phase"
		case "pod-restarts/v1":
			metric = "kube_pod_container_status_restarts_total"
		case "ipmi-sensors/v1":
			metric = "ipmi_temperature_celsius"
		case "smart-health/v1":
			metric = "smartctl_device_smart_status"
		default:
			return out, ErrArgument
		}
		query = metric + selector
		path = "/api/v1/query_range"
	case "victorialogs":
		if q.Template != "resource-logs/v1" && q.Template != "kernel-logs/v1" && !nodeLogs {
			return out, ErrArgument
		}
		query = selector
		path = "/select/logsql/query"
	}
	bound, _ := json.Marshal(struct {
		Binding  Binding
		Query    Query
		Selector string
		Scope    any
	}{a.binding, q, query, q.Scope})
	out.QueryHash = Digest(bound)
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		return out, ErrBudget
	}
	deadline, cancel := context.WithTimeout(ctx, q.TimeBudget())
	defer cancel()
	params := url.Values{"query": {query}, "start": {q.From.UTC().Format(time.RFC3339Nano)}, "end": {q.To.UTC().Format(time.RFC3339Nano)}, "limit": {strconv.Itoa(q.Limit)}, "step": {"30s"}, "timeout": {"3s"}}
	req, _ := http.NewRequestWithContext(deadline, "GET", strings.TrimSuffix(a.binding.Endpoint, "/")+path+"?"+params.Encode(), nil)
	degrade := func(reason string) (Result, error) {
		out.Partial = true
		out.Freshness = "unavailable"
		out.DegradedSources = append(out.DegradedSources, a.name)
		out.Warnings = append(out.Warnings, reason)
		return out, nil
	}
	res, err := a.client.Do(req)
	if err != nil {
		return degrade("source_unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return degrade("source_http_error")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (256<<10)+1))
	if err != nil || len(body) > 256<<10 {
		return degrade("source_byte_budget")
	}
	rows := []json.RawMessage{}
	if a.name == "victoriametrics" {
		var envelope struct {
			Status    string   `json:"status"`
			Warnings  []string `json:"warnings"`
			IsPartial bool     `json:"isPartial"`
			Data      struct {
				ResultType string            `json:"resultType"`
				Result     []json.RawMessage `json:"result"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Status != "success" || envelope.Data.ResultType != "matrix" {
			return degrade("source_contract_invalid")
		}
		rows = envelope.Data.Result
		if len(envelope.Warnings) > 0 || envelope.IsPartial {
			out.Partial = true
			out.DegradedSources = append(out.DegradedSources, a.name)
			out.Warnings = append(out.Warnings, "upstream_partial")
		}
		sampleCount := 0
		for index, row := range rows {
			var sample struct {
				Metric         map[string]string   `json:"metric"`
				Values         [][]json.RawMessage `json:"values"`
				RuleEvaluation any                 `json:"ruleEvaluation,omitempty"`
			}
			if json.Unmarshal(row, &sample) != nil {
				return degrade("source_contract_invalid")
			}
			for k, v := range labels {
				if sample.Metric[k] != v {
					return degrade("source_scope_violation")
				}
			}
			safeLabels := map[string]string{}
			for k, v := range sample.Metric {
				if _, bound := labels[k]; bound || slices.Contains([]string{"__name__", "phase", "container", "sensor", "device"}, k) {
					safeLabels[k] = Redact(v)
				}
			}
			sample.Metric = safeLabels
			if len(sample.Values) == 0 {
				return degrade("missing_metric_samples")
			}
			for _, pair := range sample.Values {
				sampleCount++
				if sampleCount > 4096 {
					return degrade("sample_budget_exhausted")
				}
				if len(pair) != 2 {
					return degrade("invalid_metric_sample")
				}
				var timestamp float64
				var value string
				if json.Unmarshal(pair[0], &timestamp) != nil || json.Unmarshal(pair[1], &value) != nil || math.IsNaN(timestamp) || math.IsInf(timestamp, 0) {
					return degrade("invalid_metric_sample")
				}
				numeric, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(numeric) || math.IsInf(numeric, 0) || timestamp < float64(q.From.UnixNano())/1e9-1 || timestamp > float64(q.To.UnixNano())/1e9+1 {
					return degrade("source_time_or_value_invalid")
				}
			}
			if q.Template == "ipmi-sensors/v1" || q.Template == "smart-health/v1" {
				var text string
				json.Unmarshal(sample.Values[len(sample.Values)-1][1], &text)
				value, _ := strconv.ParseFloat(text, 64)
				threshold := 85.0
				if q.Template == "smart-health/v1" {
					value = 1 - value
					threshold = 0
				}
				v32 := float32(value)
				sample.RuleEvaluation = hardware.CheckThreshold(q.Template, &v32, float32(threshold))
			}
			rows[index], _ = json.Marshal(sample)

		}
	} else {
		decoder := json.NewDecoder(bytes.NewReader(body))
		for {
			var row map[string]any
			err := decoder.Decode(&row)
			if err == io.EOF {
				break
			}
			if err != nil {
				return degrade("source_contract_invalid")
			}
			for k, v := range labels {
				if row[k] != v {
					return degrade("source_scope_violation")
				}
			}
			observed, parseErr := time.Parse(time.RFC3339Nano, stringValue(row["_time"]))
			if parseErr != nil || observed.Before(q.From) || observed.After(q.To) {
				return degrade("source_time_invalid")
			}
			message := Redact(stringValue(row["_msg"]))
			if len(message) > 4096 {
				message = string([]rune(message)[:min(len([]rune(message)), 1024)])
				out.Partial = true
				out.Warnings = append(out.Warnings, "message_budget_exhausted")
			}
			safe := map[string]any{"_time": observed.UTC().Format(time.RFC3339Nano), "_msg": message}
			if nodeLogs {
				safe["_TRANSPORT"] = "kernel"
			}
			if q.Template == "kernel-logs/v1" || nodeLogs {
				evaluation, err := hardware.MatchKernelLog(&message)
				if err != nil {
					return degrade("rule_mapping_unavailable")
				}
				safe["ruleEvaluation"] = evaluation
			}
			raw, _ := json.Marshal(safe)
			rows = append(rows, raw)
		}
	}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		out.Partial = true
		out.Warnings = append(out.Warnings, "row_budget_exhausted")
	}
	if err := a.authorize(ctx, a.binding); err != nil {
		return Result{}, ErrScopeUnverified
	}
	data, _ := json.Marshal(rows)
	if len(data) > q.ByteBudget() {
		return degrade("evidence_byte_budget")
	}
	if len(rows) == 0 {
		out.Warnings = append(out.Warnings, "no_evidence")
		return out, nil
	}
	kind := "metric"
	if a.name == "victorialogs" {
		kind = "log"
	}
	out.Evidence = append(out.Evidence, Evidence{SourceScopeDigest: BindingScopeDigest(a.binding), SchemaVersion: "evidence/v2", EvidenceID: uuid.Must(uuid.NewV7()).String(), TenantID: a.binding.Tenant, ResourceCanonicalID: q.ResourceCanonicalID, Type: kind, DataClass: "D1", SourceRegistrationID: a.binding.SourceID, SourceRevision: a.binding.Revision, SourceSystem: a.name, BackendLogicalID: a.binding.BackendLogicalID, QueryTemplateVersion: q.Template, QueryHash: out.QueryHash, EffectiveScope: q.Scope, EvaluatedAt: time.Now().UTC(), ObservedFrom: q.From, ObservedTo: q.To, SourceRetentionUntil: q.To.Add(time.Duration(a.binding.SourceRetentionSeconds) * time.Second), TimeReliable: true, ReplayState: "archive_pending", ContentDigest: Digest(data), DerivationEvidenceRefs: []string{}, IndependenceGroup: a.binding.SourceID, Data: data})
	return out, nil
}
func stringValue(v any) string { raw, _ := v.(string); return raw }
