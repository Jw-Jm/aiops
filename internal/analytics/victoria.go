package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

// Qualification is an independently verified collector/template/coverage proof,
// supplied by trusted deployment configuration, never by a QueryIntent. Legacy
// evidence scope proofs do not qualify a new aggregate template by themselves.
type Qualification struct {
	ObservationPoint         string `json:"observationPoint,omitempty"`
	CoverageComplete         bool   `json:"coverageComplete,omitempty"`
	Operation                string `json:"operation,omitempty"`
	DatasetID                string
	SourceRevision           int64
	BindingDigest            string
	EvidenceDigest           string
	CollectorVersion         string
	VerifiedUntil            time.Time
	CoverageFrom, CoverageTo time.Time
	MaxWindowSeconds         int64
}

type Victoria struct {
	binding        evidence.Binding
	client         *http.Client
	authorize      evidence.Authorizer
	qualifications []Qualification
	slots          chan struct{}
}

func NewVictoria(b evidence.Binding, client *http.Client, authorize evidence.Authorizer, qualifications []Qualification) (*Victoria, error) {
	u, err := url.Parse(b.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || client == nil || authorize == nil || (b.SourceType != "victoriametrics" && b.SourceType != "victorialogs") {
		return nil, ErrInvalid
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Deep copy the frozen registration mapping and proof values.
	frozen := b
	if json.Unmarshal(rawIntentBinding(b), &frozen) != nil {
		return nil, ErrInvalid
	}
	return &Victoria{frozen, &copyClient, authorize, slices.Clone(qualifications), make(chan struct{}, 4)}, nil
}
func rawIntentBinding(b evidence.Binding) []byte { v, _ := json.Marshal(b); return v }

type VictoriaPlan struct {
	intent                                              QueryIntent
	scope                                               graph.Scope
	dimension, unit, digest, catalogDigest, query, path string
	qualification                                       Qualification
	limit                                               int
	sort                                                Sort
}

func (p VictoriaPlan) ExecutionScope() graph.Scope { return CloneScope(p.scope) }
func (p VictoriaPlan) Digest() string              { return p.digest }

var safeLabel = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func (v *Victoria) Compile(intent QueryIntent, binding Binding) (VictoriaPlan, error) {
	var p VictoriaPlan
	decoded, err := DecodeIntent(rawIntent(intent))
	if err != nil {
		return p, err
	}
	intent = decoded

	if intent.NetworkSelection != nil {
		return p, ErrUnsupported
	}
	catalog, err := Catalog()
	if err != nil {
		return p, err
	}
	var dataset *Dataset
	for i := range catalog.Datasets {
		if catalog.Datasets[i].ID == intent.DatasetID {
			dataset = &catalog.Datasets[i]
		}
	}
	if dataset == nil || dataset.Source != v.binding.SourceType || dataset.AuthorizationDomain != "operational" || !slices.Contains(dataset.Operations, intent.Operation) || len(intent.Metrics) != 1 || intent.Metrics[0] != dataset.Metric || len(intent.Dimensions) > 1 || len(intent.Sort) > 1 || intent.Limit < 1 || intent.Limit > 50 {
		return p, ErrUnsupported
	}
	if intent.TimeRange == nil || intent.TimeRange.From.IsZero() || !intent.TimeRange.To.After(intent.TimeRange.From) || intent.TimeRange.To.Sub(intent.TimeRange.From) > 7*24*time.Hour || intent.TimeRange.To.After(time.Now().Add(time.Minute)) || intent.TimeRange.Timezone == "Local" {
		return p, ErrInvalid
	}
	if _, err = time.LoadLocation(intent.TimeRange.Timezone); err != nil {
		return p, ErrInvalid
	}
	trend := intent.Operation == "trend"
	compare := intent.Operation == "compare" || intent.Operation == "compare_percent"
	average := intent.Operation == "average"
	samples := intent.Operation == "samples"
	if samples && (len(intent.Dimensions) != 0 || len(intent.Sort) != 0 || intent.Limit > 20) {
		return p, ErrInvalid
	}
	if trend {
		if intent.StepSeconds < 60 || intent.StepSeconds > 86400 || intent.TimeRange.From.Nanosecond() != 0 || intent.TimeRange.To.Nanosecond() != 0 || len(intent.Dimensions) != 1 || intent.Dimensions[0] != "time" || intent.TimeRange.To.Sub(intent.TimeRange.From)%(time.Duration(intent.StepSeconds)*time.Second) != 0 || int(intent.TimeRange.To.Sub(intent.TimeRange.From)/time.Second)/intent.StepSeconds+1 > intent.Limit {
			return p, ErrInvalid
		}
	} else if intent.StepSeconds != 0 || slices.Contains(intent.Dimensions, "time") {
		return p, ErrUnsupported
	}
	if compare {
		window := intent.ComparisonTimeRange
		if window == nil || window.From.IsZero() || !window.To.After(window.From) || window.To.After(intent.TimeRange.From) || window.To.Sub(window.From) != intent.TimeRange.To.Sub(intent.TimeRange.From) || window.Timezone != intent.TimeRange.Timezone || len(intent.Dimensions) != 1 || intent.Dimensions[0] != "comparison" || len(intent.Sort) != 0 || intent.Limit < 3 {
			return p, ErrInvalid
		}
	} else if intent.ComparisonTimeRange != nil || slices.Contains(intent.Dimensions, "comparison") {
		return p, ErrUnsupported
	}
	scope, err := RestrictScope(binding.Grant, binding.Selection)
	if err != nil {
		return p, err
	}
	if scope.Tenant != v.binding.Tenant || !slices.Contains(v.binding.ScopeMapping.Scopes["cluster"], scope.Cluster) || v.binding.ScopeMapping.NativeTenant != "" || v.binding.ScopeMapping.RequiredLabels["tenant"] != scope.Tenant {
		return p, ErrDenied
	}
	var qualification *Qualification
	for i := range v.qualifications {
		q := &v.qualifications[i]
		operationMatches := q.Operation == intent.Operation || (q.Operation == "" && intent.Operation == "aggregate")
		if operationMatches && q.DatasetID == intent.DatasetID && q.SourceRevision == v.binding.Revision && q.BindingDigest == evidence.BindingScopeDigest(v.binding) && regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(q.EvidenceDigest) && q.CollectorVersion != "" && q.VerifiedUntil.After(time.Now()) {
			qualification = q
		}
	}
	if qualification == nil || qualification.MaxWindowSeconds < 1 || intent.TimeRange.To.Sub(intent.TimeRange.From) > time.Duration(qualification.MaxWindowSeconds)*time.Second || intent.TimeRange.From.Before(qualification.CoverageFrom) || intent.TimeRange.To.After(qualification.CoverageTo) {
		return p, ErrSource
	}
	lookback := 5 * time.Minute
	if trend && (dataset.ID == "metrics.pod-restarts" || dataset.ID == "logs.records") {
		lookback = time.Duration(intent.StepSeconds) * time.Second
	}
	if (trend || compare) && intent.TimeRange.From.Add(-lookback).Before(qualification.CoverageFrom) {
		return p, ErrSource
	}
	if compare && (intent.ComparisonTimeRange.From.Add(-5*time.Minute).Before(qualification.CoverageFrom) || intent.ComparisonTimeRange.To.After(qualification.CoverageTo)) {
		return p, ErrSource
	}
	dimension := ""
	if len(intent.Dimensions) > 0 {
		dimension = intent.Dimensions[0]
		if !slices.Contains(dataset.Dimensions, dimension) {
			return p, ErrUnsupported
		}
	}
	filters := map[string]string{}
	for _, f := range intent.Filters {
		if !slices.Contains(dataset.Filters, f.Field) || f.Operator != "eq" || !ValidText(f.Value, 253) || filters[f.Field] != "" {
			return p, ErrInvalid
		}
		if f.Field == "namespace" && !slices.Contains(scope.Namespaces, f.Value) {
			return p, ErrDenied
		}
		if f.Field == "level" && !slices.Contains([]string{"error", "warn", "info", "debug"}, f.Value) {
			return p, ErrUnsupported
		}
		filters[f.Field] = f.Value
	}
	if ns := filters["namespace"]; ns != "" {
		scope.Namespaces = []string{ns}
	}
	labels := map[string]string{}
	for k, value := range v.binding.ScopeMapping.RequiredLabels {
		if !safeLabel.MatchString(k) || !ValidText(value, 253) {
			return p, ErrDenied
		}
		labels[k] = value
	}
	for k, value := range map[string]string{"tenant": scope.Tenant, "cluster": scope.Cluster} {
		if prior, ok := labels[k]; ok && prior != value {
			return p, ErrDenied
		}
		labels[k] = value
	}
	node := dataset.ID == "metrics.node-ready"
	if node && (!scope.ClusterScoped || len(scope.Resources) > 0) {
		return p, ErrDenied
	}
	if !node && len(scope.Namespaces) == 0 {
		return p, ErrDenied
	}
	if dataset.ID == "metrics.pod-cpu" && intent.TimeRange.From.Add(-5*time.Minute).Before(qualification.CoverageFrom) {
		return p, ErrSource
	}
	for _, ns := range scope.Namespaces {
		if !slices.Contains(v.binding.ScopeMapping.Scopes["namespace"], ns) {
			return p, ErrDenied
		}
	}
	if fixed, ok := labels["namespace"]; ok {
		if len(scope.Namespaces) != 1 || fixed != scope.Namespaces[0] {
			return p, ErrDenied
		}
		delete(labels, "namespace")
	}
	keys := []string{}
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	selectors := []string{}
	logs := v.binding.SourceType == "victorialogs"
	for _, k := range keys {
		op := "="
		if logs {
			op = ":"
		}
		selectors = append(selectors, k+op+strconv.Quote(labels[k]))
	}
	if !node {
		if !logs {
			selectors = append(selectors, `uid!=""`)
		}
		ns := slices.Clone(scope.Namespaces)
		sort.Strings(ns)
		if logs {
			parts := []string{}
			for _, n := range ns {
				parts = append(parts, "namespace:"+strconv.Quote(n))
			}
			selectors = append(selectors, "("+strings.Join(parts, " OR ")+")")
		} else {
			parts := []string{}
			for _, n := range ns {
				parts = append(parts, regexp.QuoteMeta(n))
			}
			selectors = append(selectors, "namespace=~"+strconv.Quote(strings.Join(parts, "|")))
		}
	}
	if len(scope.Resources) > 0 {
		uids := []string{}
		for _, id := range scope.Resources {
			parsed, err := resource.ParseCanonicalID(id)
			if err != nil || parsed.Domain != "k8s" || parsed.Kind != "Pod" || parsed.Tenant != scope.Tenant || parsed.Scope != scope.Cluster {
				return p, ErrDenied
			}
			uids = append(uids, parsed.StableID)
		}
		sort.Strings(uids)
		if logs {
			parts := []string{}
			for _, uid := range uids {
				parts = append(parts, "uid:"+strconv.Quote(uid))
			}
			selectors = append(selectors, "("+strings.Join(parts, " OR ")+")")
		} else {
			parts := []string{}
			for _, uid := range uids {
				parts = append(parts, regexp.QuoteMeta(uid))
			}
			selectors = append(selectors, "uid=~"+strconv.Quote(strings.Join(parts, "|")))
		}
	}
	query, path := "", "/api/v1/query"
	if logs {
		if level := filters["level"]; level != "" {
			selectors = append(selectors, "level:"+strconv.Quote(level))
		}
		selectors = append(selectors, "_time:["+intent.TimeRange.From.UTC().Format(time.RFC3339Nano)+","+intent.TimeRange.To.UTC().Format(time.RFC3339Nano)+")")
		query = strings.Join(selectors, " AND ") + " | stats "
		if dimension != "" && !trend && !compare {
			query += "by (" + dimension + ") "
		}
		query += "count() as value"
		path = "/select/logsql/stats_query"
		if samples {
			query = strings.Join(selectors, " AND ") + " | sort by (_time) desc | limit " + strconv.Itoa(intent.Limit+1)
			path = "/select/logsql/query"
		}
	} else {
		selector := "{" + strings.Join(selectors, ",") + "}"
		seconds := int64(intent.TimeRange.To.Sub(intent.TimeRange.From).Seconds())
		if trend && dataset.ID == "metrics.pod-restarts" {
			seconds = int64(intent.StepSeconds)
		}
		if seconds < 1 {
			return p, ErrInvalid
		}
		group := ""
		if dimension != "" && !trend && !compare {
			group = " by (" + dimension + ")"
		}
		switch dataset.ID {
		case "metrics.pod-restarts":
			query = "sum" + group + "(increase(kube_pod_container_status_restarts_total" + selector + "[" + strconv.FormatInt(seconds, 10) + "s]))"
		case "metrics.pod-cpu":
			selector = strings.TrimSuffix(selector, "}") + `,container!="",container!="POD"}`
			query = "sum" + group + "(avg_over_time(rate(container_cpu_usage_seconds_total" + selector + "[5m])[" + strconv.FormatInt(seconds, 10) + "s:30s]))"
			if trend {
				query = "sum(rate(container_cpu_usage_seconds_total" + selector + "[5m]))"
			}
		case "metrics.pod-memory":
			metric := "container_memory_working_set_bytes" + strings.TrimSuffix(selector, "}") + `,container!="",container!="POD"}`
			if trend || compare {
				metric = "last_over_time(" + metric + "[5m])"
			} else if average {
				metric = "avg_over_time(" + metric + "[" + strconv.FormatInt(seconds, 10) + "s])"
			}
			query = "sum" + group + "(" + metric + ")"
		case "metrics.pod-phase":
			metric := "kube_pod_status_phase" + selector
			if trend || compare {
				query = "sum(last_over_time(" + metric + "[5m]) == bool 1)"
			} else if average {
				query = "sum" + group + "(avg_over_time(" + metric + "[" + strconv.FormatInt(seconds, 10) + "s]))"
			} else {
				query = "sum" + group + "(" + metric + " == 1)"
			}
		case "metrics.node-ready":
			metric := "kube_node_status_condition" + strings.TrimSuffix(selector, "}") + `,condition="Ready",status="true"}`
			if trend || compare {
				query = "sum(last_over_time(" + metric + "[5m]) == bool 1)"
			} else if average {
				query = "sum(avg_over_time(" + metric + "[" + strconv.FormatInt(seconds, 10) + "s]))"
			} else {
				query = "sum(" + metric + " == 1)"
			}
		default:
			return p, ErrUnsupported
		}
	}
	if trend && !logs {
		path = "/api/v1/query_range"
	}
	order := Sort{Field: dataset.Metric, Direction: "desc"}
	if trend {
		order = Sort{Field: "time", Direction: "asc"}
	}
	if len(intent.Sort) > 0 {
		order = intent.Sort[0]
		if (order.Field != dataset.Metric && (dimension == "" || order.Field != dimension)) || !slices.Contains([]string{"asc", "desc"}, order.Direction) {
			return p, ErrUnsupported
		}
		if trend && (order.Field != "time" || order.Direction != "asc") {
			return p, ErrUnsupported
		}
	}
	p = VictoriaPlan{intent, scope, dimension, dataset.Unit, "", catalogDigest(catalog), query, path, *qualification, intent.Limit, order}
	p.digest = graph.ScopeDigest(struct {
		Intent                                 QueryIntent
		Scope                                  graph.Scope
		Binding, Qualification, Query, Catalog string
	}{intent, scope, evidence.BindingScopeDigest(v.binding), graph.ScopeDigest(*qualification), query, p.catalogDigest})
	return p, nil
}

func (v *Victoria) Execute(ctx context.Context, p VictoriaPlan) (QueryResult, error) {
	if p.intent.Operation == "compare" || p.intent.Operation == "compare_percent" {
		return v.executeComparison(ctx, p)
	}
	if p.intent.Operation == "trend" && v.binding.SourceType == "victorialogs" {
		return v.executeLogTrend(ctx, p)
	}
	if p.intent.Operation == "samples" {
		return v.executeLogSamples(ctx, p)
	}
	if p.digest == "" || !p.qualification.VerifiedUntil.After(time.Now()) || v.authorize(ctx, v.binding) != nil {
		return QueryResult{}, ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case v.slots <- struct{}{}:
		defer func() { <-v.slots }()
	case <-ctx.Done():
		return QueryResult{}, ErrSource
	}
	params := url.Values{"query": {p.query}, "time": {p.intent.TimeRange.To.UTC().Format(time.RFC3339Nano)}, "timeout": {"3s"}, "nocache": {"1"}}
	params.Set("deny_partial_response", "1")
	if p.intent.Operation == "trend" {
		params.Set("start", p.intent.TimeRange.From.UTC().Format(time.RFC3339))
		params.Set("end", p.intent.TimeRange.To.UTC().Format(time.RFC3339))
		params.Set("step", strconv.Itoa(p.intent.StepSeconds))
	}
	if v.binding.SourceType == "victorialogs" {
		params.Set("allow_partial_response", "0")
		params.Set("start", p.intent.TimeRange.From.UTC().Format(time.RFC3339Nano))
		params.Set("end", p.intent.TimeRange.To.UTC().Format(time.RFC3339Nano))
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(v.binding.Endpoint, "/")+p.path, strings.NewReader(params.Encode()))
	if err != nil {
		return QueryResult{}, ErrInvalid
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := v.client.Do(req)
	if err != nil {
		return QueryResult{}, ErrSource
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return QueryResult{}, ErrSource
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return QueryResult{}, ErrSource
	}
	var envelope struct {
		Status    string   `json:"status"`
		Warnings  []string `json:"warnings"`
		IsPartial bool     `json:"isPartial"`
		Data      struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string   `json:"metric"`
				Value  []json.RawMessage   `json:"value"`
				Values [][]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	resultType := "vector"
	if p.intent.Operation == "trend" {
		resultType = "matrix"
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Status != "success" || envelope.Data.ResultType != resultType || len(envelope.Data.Result) > 10000 {
		return QueryResult{}, ErrSource
	}
	out := QueryResult{TimeRange: copyQueryTimeRange(p.intent.TimeRange), SchemaVersion: "query-result/v1", DatasetID: p.intent.DatasetID, PlanDigest: p.digest, CatalogDigest: p.catalogDigest, ScopeDigest: graph.ScopeDigest(p.scope), Status: "complete", Exact: true, Dimension: p.dimension, Unit: p.unit, Rows: []Row{}, SnapshotReadAt: time.Now().UTC(), Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, SourceManifest: &SourceManifest{v.binding.SourceID, v.binding.Revision, evidence.BindingScopeDigest(v.binding), graph.ScopeDigest(p.qualification), graph.ScopeDigest(p.query), p.qualification.DatasetID + "/v1", p.intent.TimeRange.From, p.intent.TimeRange.To}}
	if p.intent.Operation == "average" {
		out.SourceManifest.TemplateVersion = p.intent.DatasetID + "-average/v1"
	}
	seen := map[string]bool{}
	if p.intent.Operation == "trend" {
		out.StepSeconds = p.intent.StepSeconds
		out.SourceManifest.TemplateVersion = p.intent.DatasetID + "-trend/v1"
		if len(envelope.Data.Result) > 1 {
			return QueryResult{}, ErrSource
		}
		for _, series := range envelope.Data.Result {
			if len(series.Metric) != 0 || len(series.Value) != 0 || len(series.Values) > p.intent.Limit {
				return QueryResult{}, ErrSource
			}
			for _, point := range series.Values {
				row, err := trendPoint(point, p.intent.TimeRange, p.intent.StepSeconds)
				if err != nil || seen[row.Dimension] {
					return QueryResult{}, ErrSource
				}
				seen[row.Dimension] = true
				out.Rows = append(out.Rows, row)
			}
		}
		want := int(p.intent.TimeRange.To.Sub(p.intent.TimeRange.From)/time.Second)/p.intent.StepSeconds + 1
		if len(out.Rows) > 0 && len(out.Rows) < want {
			out.Exact = false
			out.Status = "partial"
			out.Warnings = append(out.Warnings, "missing_time_points")
		}
	}
	for _, series := range envelope.Data.Result {
		if p.intent.Operation == "trend" {
			break
		}
		if len(series.Value) != 2 {
			return QueryResult{}, ErrSource
		}
		dimension := ""
		for key, value := range series.Metric {
			if key == "__name__" && v.binding.SourceType == "victorialogs" && value == "value" {
				continue
			}
			if key != p.dimension || !ValidText(value, 253) {
				return QueryResult{}, ErrSource
			}
			dimension = value
		}
		if p.dimension != "" && dimension == "" {
			return QueryResult{}, ErrSource
		}
		if seen[dimension] {
			return QueryResult{}, ErrSource
		}
		seen[dimension] = true
		if p.dimension == "namespace" && !slices.Contains(p.scope.Namespaces, dimension) {
			return QueryResult{}, ErrDenied
		}
		var str string
		if json.Unmarshal(series.Value[1], &str) != nil {
			return QueryResult{}, ErrSource
		}
		value, err := strconv.ParseFloat(str, 64)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) || value < 0 || value > 1<<53 {
			return QueryResult{}, ErrSource
		}
		var timestamp float64
		if json.Unmarshal(series.Value[0], &timestamp) != nil || math.IsNaN(timestamp) || math.IsInf(timestamp, 0) || math.Abs(timestamp-float64(p.intent.TimeRange.To.Unix())) > 1 {
			return QueryResult{}, ErrSource
		}
		out.Rows = append(out.Rows, Row{dimension, value})
	}
	if len(envelope.Warnings) > 0 || envelope.IsPartial {
		out.Exact = false
		out.Status = "partial"
		out.Warnings = append(out.Warnings, "upstream_partial")
		out.DegradedSources = append(out.DegradedSources, v.binding.SourceType)
	}
	if len(out.Rows) == 0 {
		out.Exact = false
		out.Status = "no_data"
		out.Warnings = append(out.Warnings, "no_observations")
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if p.sort.Field == p.intent.Metrics[0] && a.Value != b.Value {
			if p.sort.Direction == "asc" {
				return a.Value < b.Value
			}
			return a.Value > b.Value
		}
		if p.sort.Field == p.dimension && p.sort.Direction == "desc" {
			return a.Dimension > b.Dimension
		}
		return a.Dimension < b.Dimension
	})
	if len(out.Rows) > p.limit {
		out.Truncated = true
		out.Rows = out.Rows[:p.limit]
	}
	if v.authorize(ctx, v.binding) != nil || !p.qualification.VerifiedUntil.After(time.Now()) {
		return QueryResult{}, ErrDenied
	}
	if err := contract.Validate("https://ops.local/schemas/query-result/v1", rawResult(out)); err != nil {
		return QueryResult{}, fmt.Errorf("%w: invalid result", ErrSource)
	}
	return out, nil
}

func rawResult(v QueryResult) []byte { b, _ := json.Marshal(v); return b }
