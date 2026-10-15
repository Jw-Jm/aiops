package deepflow

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
)

//go:embed templates/l7-summary-v1.sql
var l7SummaryTemplate string

//go:embed templates/l4-summary-v1.sql
var l4SummaryTemplate string

// SummaryQuery fixes both native identities. Neither backend IDs nor SQL are
// accepted from callers. ObservationPoint comes from qualified server config.
type SummaryQuery struct {
	Target, Peer, Direction, ObservationPoint string
	From, To                                  time.Time
	Scope                                     graph.Scope
	L7                                        bool
}
type NetworkSummary struct {
	Namespace            string    `json:"namespace"`
	SchemaVersion        string    `json:"schemaVersion"`
	Target               string    `json:"target"`
	Peer                 string    `json:"peer"`
	Direction            string    `json:"direction"`
	ObservationPoint     string    `json:"observationPoint"`
	From                 time.Time `json:"from"`
	To                   time.Time `json:"to"`
	IdentityValidFrom    time.Time `json:"identityValidFrom"`
	Observations         uint64    `json:"observations"`
	Responses            uint64    `json:"responses"`
	Errors               uint64    `json:"errors"`
	DurationMicroseconds uint64    `json:"durationMicroseconds"`
	Retransmissions      uint64    `json:"retransmissions"`
	Failures             uint64    `json:"failures"`
	Protocol             string    `json:"protocol"`
	Population           string    `json:"population"`
	TimeReliable         bool      `json:"timeReliable"`
}

func (a *Adapter) Binding() evidence.Binding {
	var b evidence.Binding
	raw, _ := json.Marshal(a.binding)
	_ = json.Unmarshal(raw, &b)
	return b
}

func (a *Adapter) Summary(ctx context.Context, q SummaryQuery) (NetworkSummary, evidence.Result, error) {
	return a.summary(ctx, q, a.resolveSummaryPair)
}
func (a *Adapter) resolveSummaryPair(ctx context.Context, target, peer string) (Endpoint, Endpoint, error) {
	if live, ok := a.mapping.(*LiveMapper); ok {
		return live.ByCanonicalPair(ctx, target, peer)
	}
	l, err := a.mapping.ByCanonical(ctx, target)
	if err != nil {
		return Endpoint{}, Endpoint{}, err
	}
	r, err := a.mapping.ByCanonical(ctx, peer)
	return l, r, err
}

// SummaryBatch freezes one verified pair for bounded window reads and rechecks
// both native/backend identities after all reads. Nothing is archived until the
// complete batch passes this fence; it is not a cross-request identity cache.
func (a *Adapter) SummaryBatch(ctx context.Context, queries []SummaryQuery) ([]NetworkSummary, []evidence.Result, error) {
	if len(queries) < 1 || len(queries) > 10 {
		return nil, nil, evidence.ErrBudget
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	first := queries[0]
	for _, q := range queries {
		if q.Target != first.Target || q.Peer != first.Peer || q.Direction != first.Direction || q.ObservationPoint != first.ObservationPoint || q.L7 != first.L7 || graph.ScopeDigest(q.Scope) != graph.ScopeDigest(first.Scope) {
			return nil, nil, evidence.ErrArgument
		}
	}
	resolve := func() (Endpoint, Endpoint, error) { return a.resolveSummaryPair(ctx, first.Target, first.Peer) }
	var left, right Endpoint
	var err error
	if live, ok := a.mapping.(*LiveMapper); ok {
		var snapshot summaryPairSnapshot
		snapshot, err = live.beginSummaryPair(ctx, first.Target, first.Peer)
		left, right = snapshot.left, snapshot.right
		resolve = func() (Endpoint, Endpoint, error) { return live.finishSummaryPair(ctx, snapshot) }
	} else {
		left, right, err = resolve()
	}
	if err != nil {
		slog.Warn("DeepFlow pair fence unavailable", "stage", "initial", "causeType", fmt.Sprintf("%T", err), "scopeRejected", errors.Is(err, evidence.ErrScopeUnverified), "canceled", errors.Is(err, context.Canceled), "deadlineExceeded", errors.Is(err, context.DeadlineExceeded))
		return nil, nil, fmt.Errorf("%w: initial endpoint pair: %w", evidence.ErrScopeUnverified, err)
	}
	frozen := func(context.Context, string, string) (Endpoint, Endpoint, error) { return left, right, nil }
	summaries := []NetworkSummary{}
	results := []evidence.Result{}
	for _, q := range queries {
		s, r, err := a.summary(ctx, q, frozen)
		if err != nil {
			return nil, nil, err
		}
		summaries = append(summaries, s)
		results = append(results, r)
	}
	l2, r2, err := resolve()
	if err != nil {
		slog.Warn("DeepFlow pair fence unavailable", "stage", "final", "causeType", fmt.Sprintf("%T", err), "scopeRejected", errors.Is(err, evidence.ErrScopeUnverified), "canceled", errors.Is(err, context.Canceled), "deadlineExceeded", errors.Is(err, context.DeadlineExceeded))
		return nil, nil, fmt.Errorf("%w: final endpoint pair: %w", evidence.ErrScopeUnverified, err)
	}
	if l2 != left || r2 != right || a.authorize(ctx, a.binding) != nil {
		slog.Warn("DeepFlow pair fence unavailable", "stage", "final", "identityChanged", l2 != left || r2 != right)
		return nil, nil, evidence.ErrScopeUnverified
	}
	return summaries, results, nil
}
func (a *Adapter) summary(ctx context.Context, q SummaryQuery, resolve func(context.Context, string, string) (Endpoint, Endpoint, error)) (NetworkSummary, evidence.Result, error) {
	s := NetworkSummary{SchemaVersion: "network-summary/v1", Target: q.Target, Peer: q.Peer, Direction: q.Direction, ObservationPoint: q.ObservationPoint, From: q.From, To: q.To, Protocol: "tcp", Population: "scoped_single_observation_point_records"}
	out := evidence.Result{SchemaVersion: "evidence-result/v2", Evidence: []evidence.Evidence{}, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}}
	if q.L7 {
		s.Protocol = "http"
		s.Population = "completed_http_session_observations"
		if !a.l7Available() {
			return s, out, ErrDisabled
		}
	}
	if q.From.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > time.Hour || q.To.After(time.Now()) || q.From.Nanosecond() != 0 || q.To.Nanosecond() != 0 || !slices.Contains([]string{"incoming", "outgoing", "either"}, q.Direction) || !slices.Contains([]string{"c", "s", "c-p", "s-p", "c-app", "s-app", "app"}, q.ObservationPoint) {
		return s, out, evidence.ErrArgument
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := a.authorize(ctx, a.binding); err != nil {
		return s, out, evidence.ErrScopeUnverified
	}
	if q.Scope.Tenant != a.binding.Tenant || !slices.Contains(a.binding.ScopeMapping.Scopes["cluster"], q.Scope.Cluster) || a.binding.ScopeMapping.NativeTenant != "" || len(a.binding.ScopeMapping.RequiredLabels) > 0 {
		return s, out, evidence.ErrScopeUnverified
	}
	for dimension := range a.binding.ScopeMapping.Scopes {
		if !slices.Contains([]string{"organization", "cluster", "namespace"}, dimension) {
			return s, out, evidence.ErrScopeUnverified
		}
	}
	orgs := a.binding.ScopeMapping.Scopes["organization"]
	if len(orgs) != 1 {
		return s, out, evidence.ErrScopeUnverified
	}
	org, err := strconv.ParseUint(orgs[0], 10, 16)
	if err != nil || org == 0 {
		return s, out, evidence.ErrScopeUnverified
	}
	resolvePair := func() (Endpoint, Endpoint, error) { return resolve(ctx, q.Target, q.Peer) }
	left, right, err := resolvePair()
	if err != nil {
		return s, out, fmt.Errorf("%w: initial endpoint pair", evidence.ErrScopeUnverified)
	}
	if left.CanonicalID != q.Target || right.CanonicalID != q.Peer || left.ClusterID == 0 || left.NamespaceID == 0 || left.PodID == 0 || right.PodID == 0 || left.ClusterID != right.ClusterID || left.NamespaceID != right.NamespaceID || left.Namespace != right.Namespace || !q.Scope.Allows(left.CanonicalID, left.Namespace) || !q.Scope.Allows(right.CanonicalID, right.Namespace) || !slices.Contains(a.binding.ScopeMapping.Scopes["namespace"], left.Namespace) {
		return s, out, evidence.ErrScopeUnverified
	}
	s.Namespace = left.Namespace
	s.IdentityValidFrom = left.IdentityValidFrom
	if right.IdentityValidFrom.After(s.IdentityValidFrom) {
		s.IdentityValidFrom = right.IdentityValidFrom
	}
	if a.mode == "live" && (left.IdentityValidFrom.IsZero() || right.IdentityValidFrom.IsZero() || q.From.Before(s.IdentityValidFrom)) {
		return s, out, evidence.ErrScopeUnverified
	}
	forward := fmt.Sprintf("(pod_id_0 = %d AND pod_id_1 = %d)", left.PodID, right.PodID)
	reverse := fmt.Sprintf("(pod_id_0 = %d AND pod_id_1 = %d)", right.PodID, left.PodID)
	predicate := forward
	if q.Direction == "incoming" {
		predicate = reverse
	}
	if q.Direction == "either" {
		predicate = "(" + forward + " OR " + reverse + ")"
	}
	template := l4SummaryTemplate
	if q.L7 {
		template = l7SummaryTemplate
	}
	sql := fmt.Sprintf(strings.TrimSpace(template), q.From.Unix(), q.To.Unix(), left.ClusterID, left.ClusterID, left.NamespaceID, left.NamespaceID, predicate, q.ObservationPoint)
	rawPlan, _ := json.Marshal(struct {
		Query   SummaryQuery
		Binding evidence.Binding
		SQL     string
	}{q, a.binding, sql})
	out.QueryHash = evidence.Digest(rawPlan)
	fail := func(code string) (NetworkSummary, evidence.Result, error) {
		out.Partial = true
		out.Freshness = "unavailable"
		out.Warnings = append(out.Warnings, code)
		out.DegradedSources = []string{"deepflow"}
		return s, out, nil
	}
	form := url.Values{"db": {"flow_log"}, "sql": {sql}}
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(a.binding.Endpoint, "/")+"/v1/query/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Org-Id", strconv.FormatUint(org, 10))
	response, err := a.client.Do(req)
	if err != nil {
		return fail("source_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fail("source_http_error")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (32<<10)+1))
	if err != nil || len(raw) > 32<<10 {
		return fail("source_byte_budget")
	}
	var body struct {
		Status string `json:"OPT_STATUS"`
		Result struct {
			Columns []string            `json:"columns"`
			Values  [][]json.RawMessage `json:"values"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Status != "SUCCESS" {
		return fail("summary_contract_invalid")
	}
	indices := map[string]int{}
	for i, column := range body.Result.Columns {
		if _, exists := indices[column]; exists {
			return fail("summary_columns_invalid")
		}
		indices[column] = i
	}
	required := []string{"status", "observations", "retrans_tx", "retrans_rx"}
	maxGroups := 256
	if q.L7 {
		required = []string{"response_code", "observations", "duration_us"}
		maxGroups = 400
	}
	if len(indices) != len(required) || len(body.Result.Values) > maxGroups {
		return fail("summary_group_budget")
	}
	for _, column := range required {
		if _, ok := indices[column]; !ok {
			return fail("summary_columns_invalid")
		}
	}
	groups := map[uint64]bool{}
	add := func(destination *uint64, value uint64) bool {
		if value > uint64(1<<53-1)-*destination {
			return false
		}
		*destination += value
		return true
	}
	for _, row := range body.Result.Values {
		if len(row) != len(required) {
			return fail("summary_columns_invalid")
		}
		numeric := func(column string) (uint64, bool) {
			var value json.Number
			if json.Unmarshal(row[indices[column]], &value) != nil {
				return 0, false
			}
			v, err := strconv.ParseUint(value.String(), 10, 64)
			return v, err == nil && v <= uint64(1<<53-1)
		}
		observations, ok := numeric("observations")
		if !ok {
			return fail("summary_value_invalid")
		}
		keyColumn := "status"
		if q.L7 {
			keyColumn = "response_code"
		}
		key, ok := numeric(keyColumn)
		if !ok || groups[key] {
			return fail("summary_population_invalid")
		}
		groups[key] = true
		if !add(&s.Observations, observations) {
			return fail("summary_value_overflow")
		}
		if q.L7 {
			duration, ok := numeric("duration_us")
			if !ok || key < 200 || key >= 600 {
				return fail("summary_population_invalid")
			}
			if !add(&s.Responses, observations) || !add(&s.DurationMicroseconds, duration) {
				return fail("summary_value_overflow")
			}
			if key >= 500 && !add(&s.Errors, observations) {
				return fail("summary_value_overflow")
			}
		} else {
			tx, ok := numeric("retrans_tx")
			rx, ok2 := numeric("retrans_rx")
			if !ok || !ok2 || key > 255 {
				return fail("summary_population_invalid")
			}
			if !add(&s.Retransmissions, tx) || !add(&s.Retransmissions, rx) {
				return fail("summary_value_overflow")
			}
			if (key == 3 || key == 4) && !add(&s.Failures, observations) {
				return fail("summary_value_overflow")
			}
		}
	}

	if a.authorize(ctx, a.binding) != nil {
		return NetworkSummary{}, evidence.Result{}, evidence.ErrScopeUnverified
	}
	// Re-resolve both ends after the query, including fixture mappers used by
	// contract tests. Native container changes cannot renew historical identity.
	l2, r2, err := resolvePair()
	if err != nil || l2 != left || r2 != right {
		return NetworkSummary{}, evidence.Result{}, fmt.Errorf("%w: final endpoint pair", evidence.ErrScopeUnverified)
	}
	// The final pair resolution already performs a full backend refresh with
	// native snapshots before and after it. A third native list would repeat
	// that fence and consume the shared Kubernetes read budget.
	if a.authorize(ctx, a.binding) != nil {
		return NetworkSummary{}, evidence.Result{}, evidence.ErrScopeUnverified
	}
	if a.mode == "fixture_only" {
		out.Warnings = append(out.Warnings, "fixture_only")
	}
	out.Warnings = append(out.Warnings, "unreliable-event-time", "observation_population_not_all_user_requests")
	if q.L7 {
		out.Warnings = append(out.Warnings, "incomplete_sessions_excluded_not_counted")
	} else {
		out.Warnings = append(out.Warnings, "status_3_4_error_observations_not_unique_failed_connections")
	}
	data, _ := json.Marshal(s)
	if err := contract.Validate("https://ops.local/schemas/network-summary/v1", data); err != nil {
		return fail("summary_contract_invalid")
	}
	now := time.Now().UTC()
	out.Evidence = []evidence.Evidence{{SchemaVersion: "evidence/v2", EvidenceID: uuid.Must(uuid.NewV7()).String(), TenantID: a.binding.Tenant, ResourceCanonicalID: q.Target, Type: "network", DataClass: "D1", SourceRegistrationID: a.binding.SourceID, SourceRevision: a.binding.Revision, SourceSystem: "deepflow", BackendLogicalID: a.binding.BackendLogicalID, SourceScopeDigest: evidence.BindingScopeDigest(a.binding), QueryTemplateVersion: "deepflow-v7.2.0/network-summary/v1", QueryHash: out.QueryHash, EffectiveScope: q.Scope, EvaluatedAt: now, ObservedFrom: q.From, ObservedTo: q.To, SourceRetentionUntil: q.To.Add(time.Duration(a.binding.SourceRetentionSeconds) * time.Second), TimeReliable: false, ReplayState: "archive_pending", ContentDigest: evidence.Digest(data), DerivationEvidenceRefs: []string{}, IndependenceGroup: a.binding.SourceID, Data: data}}
	return s, out, nil
}
