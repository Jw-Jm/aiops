package deepflow

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"ops-platform/internal/evidence"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed templates/l4-v1.sql
var l4Template string

//go:embed templates/l7-v1.sql
var l7Template string

var ErrDisabled = errors.New("CAPABILITY_DISABLED")

type Endpoint struct {
	CanonicalID, Namespace        string
	PodID, ClusterID, NamespaceID uint64
	IdentityValidFrom             time.Time
}
type Mapper interface {
	ByCanonical(context.Context, string) (Endpoint, error)
	ByBackend(context.Context, uint64) (Endpoint, error)
}
type Adapter struct {
	binding   evidence.Binding
	client    *http.Client
	mapping   Mapper
	authorize evidence.Authorizer
	l7        bool
	mode      string
	l7Mu      sync.RWMutex
	l7Until   time.Time
}

func New(b evidence.Binding, c *http.Client, m Mapper, authorize evidence.Authorizer, l7 bool, mode string) (*Adapter, error) {
	if b.SourceType != "" && b.SourceType != "deepflow" {
		return nil, evidence.ErrArgument
	}
	b.SourceType = "deepflow"
	if mode == "live" {
		live, ok := m.(*LiveMapper)
		if !ok || evidence.BindingScopeDigest(live.binding) != evidence.BindingScopeDigest(b) {
			return nil, evidence.ErrScopeUnverified
		}
	}
	u, err := url.Parse(b.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.Port() == "8123" || u.Port() == "9000" || strings.Contains(strings.ToLower(u.Host), "clickhouse") || c == nil || m == nil || authorize == nil || (mode != "live" && mode != "fixture_only") {
		return nil, evidence.ErrArgument
	}
	bounded := *c
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Adapter{binding: b, client: &bounded, mapping: m, authorize: authorize, l7: l7, mode: mode}, nil
}
func (a *Adapter) Name() string { return "deepflow" }
func (a *Adapter) Capabilities(ctx context.Context) (evidence.CapabilitySet, error) {
	state := a.mode
	if err := a.authorize(ctx, a.binding); err != nil {
		return evidence.CapabilitySet{"network": "disabled/unverified", "l7": "disabled", "trace": "CAPABILITY_DISABLED"}, err
	}
	l7 := "disabled"
	if a.l7Available() {
		l7 = state
	}
	return evidence.CapabilitySet{"network": state, "l7": l7, "trace": "CAPABILITY_DISABLED"}, nil
}
func (a *Adapter) Query(ctx context.Context, q evidence.Query) (evidence.Result, error) {
	return a.query(ctx, q, false)
}
func (a *Adapter) query(ctx context.Context, q evidence.Query, qualifyingL7 bool) (evidence.Result, error) {
	out := evidence.Result{SchemaVersion: "evidence-result/v2", Evidence: []evidence.Evidence{}, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}}
	if q.Template == "GetTraceContext" || (q.Template == "GetL7Context" && !qualifyingL7 && !a.l7Available()) {
		return out, ErrDisabled
	}
	switch q.Template {
	case "GetResourceNetworkHealth", "GetNetworkDependencies", "GetNetworkPath", "FindTCPRetransmission", "FindPacketLoss", "FindConnectionFailure", "GetL7Context":
	default:
		return out, evidence.ErrArgument
	}
	if q.Limit < 1 || q.Limit > 200 || q.From.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > time.Hour || q.To.After(time.Now().Add(time.Minute)) {
		return out, evidence.ErrBudget
	}
	ctx, cancelQuery := context.WithTimeout(ctx, q.TimeBudget())
	defer cancelQuery()
	if err := a.authorize(ctx, a.binding); err != nil {
		return out, fmt.Errorf("%w: binding authorization", evidence.ErrScopeUnverified)
	}
	if a.binding.ScopeMapping.NativeTenant != "" || len(a.binding.ScopeMapping.RequiredLabels) > 0 {
		return out, evidence.ErrScopeUnverified
	}
	for dimension := range a.binding.ScopeMapping.Scopes {
		if dimension != "organization" && dimension != "cluster" && dimension != "namespace" {
			return out, evidence.ErrScopeUnverified
		}
	}
	orgs := a.binding.ScopeMapping.Scopes["organization"]
	if len(orgs) != 1 || len(a.binding.ScopeMapping.Scopes["team"]) > 0 {
		return out, evidence.ErrScopeUnverified
	}
	org, err := strconv.ParseUint(orgs[0], 10, 16)
	if err != nil || org == 0 {
		return out, evidence.ErrScopeUnverified
	}
	endpoint, err := a.mapping.ByCanonical(ctx, q.ResourceCanonicalID)
	if err != nil || endpoint.PodID == 0 || endpoint.ClusterID == 0 || endpoint.NamespaceID == 0 || endpoint.CanonicalID != q.ResourceCanonicalID || endpoint.Namespace != q.Namespace || !q.Scope.Allows(endpoint.CanonicalID, endpoint.Namespace) || q.Scope.Tenant != a.binding.Tenant || !slices.Contains(a.binding.ScopeMapping.Scopes["cluster"], q.Scope.Cluster) || !slices.Contains(a.binding.ScopeMapping.Scopes["namespace"], endpoint.Namespace) {
		return out, fmt.Errorf("%w: endpoint identity or execution scope", evidence.ErrScopeUnverified)
	}
	if a.mode == "live" && (endpoint.IdentityValidFrom.IsZero() || q.From.Before(endpoint.IdentityValidFrom)) {
		return out, fmt.Errorf("%w: identity lifetime", evidence.ErrScopeUnverified)
	}
	// Only integers are bound into the immutable template; no caller SQL,
	// identifiers, regex or operator strings enter this private query path.
	template := l4Template
	if q.Template == "GetL7Context" {
		template = l7Template
	}
	sql := fmt.Sprintf(strings.TrimSpace(template), q.From.Unix(), q.To.Unix(), endpoint.ClusterID, endpoint.ClusterID, endpoint.NamespaceID, endpoint.NamespaceID, endpoint.PodID, endpoint.PodID, q.Limit)
	switch q.Template {
	case "FindTCPRetransmission":
		sql = strings.Replace(sql, " LIMIT ", " AND (retrans_tx > 0 OR retrans_rx > 0) LIMIT ", 1)
	case "FindConnectionFailure":
		sql = strings.Replace(sql, " LIMIT ", " AND status IN (3,4) LIMIT ", 1)
	}
	encoded, _ := json.Marshal(struct {
		Binding evidence.Binding
		Query   evidence.Query
		SQL     string
		Scope   any
	}{a.binding, q, sql, q.Scope})
	out.QueryHash = evidence.Digest(encoded)
	deadline, cancel := context.WithTimeout(ctx, q.TimeBudget())
	defer cancel()
	form := url.Values{"db": {"flow_log"}, "sql": {sql}}
	req, _ := http.NewRequestWithContext(deadline, "POST", strings.TrimSuffix(a.binding.Endpoint, "/")+"/v1/query/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Org-Id", strconv.FormatUint(org, 10))
	degraded := func(code string) (evidence.Result, error) {
		out.Partial = true
		out.Freshness = "unavailable"
		out.Warnings = append(out.Warnings, code)
		out.DegradedSources = []string{"deepflow"}
		return out, nil
	}
	response, err := a.client.Do(req)
	if err != nil {
		return degraded("source_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return degraded("source_http_error")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 {
		return degraded("source_byte_budget")
	}
	var body struct {
		Status string `json:"OPT_STATUS"`
		Result struct {
			Columns []string `json:"columns"`
			Values  [][]any  `json:"values"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Status != "SUCCESS" {
		return degraded("source_contract_invalid")
	}
	indices := map[string]int{}
	for i, v := range body.Result.Columns {
		if _, exists := indices[v]; exists {
			return degraded("duplicate_columns")
		}
		indices[v] = i
	}
	required := []string{"pod_id_0", "pod_id_1", "retrans_tx", "retrans_rx", "status", "tap_side", "observed_at"}
	if q.Template == "GetL7Context" {
		required = append(required, "l7_protocol", "response_code", "rrt")
	}
	for _, v := range required {
		if _, ok := indices[v]; !ok {
			return degraded("missing_columns")
		}
	}
	if len(body.Result.Values) > q.Limit {
		body.Result.Values = body.Result.Values[:q.Limit]
		out.Partial = true
		out.Warnings = append(out.Warnings, "row_budget_exhausted")
	}
	rows := []map[string]any{}
	// Backend IDs repeat across bounded flow rows. Reuse only this query's
	// resolution; final source/native fences still cover the complete result.
	type mappedEndpoint struct {
		endpoint Endpoint
		err      error
	}
	resolved := map[uint64]mappedEndpoint{}
	byBackend := func(id uint64) (Endpoint, error) {
		if value, ok := resolved[id]; ok {
			return value.endpoint, value.err
		}
		endpoint, err := a.mapping.ByBackend(ctx, id)
		resolved[id] = mappedEndpoint{endpoint, err}
		return endpoint, err
	}
	for _, row := range body.Result.Values {
		if len(row) != len(body.Result.Columns) {
			return degraded("invalid_columns")
		}
		numeric := func(name string) (uint64, bool) {
			v, ok := row[indices[name]].(float64)
			return uint64(v), ok && v >= 0 && v < 9007199254740992 && float64(uint64(v)) == v
		}
		left, lok := numeric("pod_id_0")
		right, rok := numeric("pod_id_1")
		if !lok || !rok || left != endpoint.PodID && right != endpoint.PodID {
			return degraded("invalid_endpoint")
		}
		l, le := byBackend(left)
		r, re := byBackend(right)
		baseResolved := le == nil && re == nil && l.CanonicalID != "" && r.CanonicalID != "" && l.ClusterID == endpoint.ClusterID && r.ClusterID == endpoint.ClusterID && l.NamespaceID == endpoint.NamespaceID && r.NamespaceID == endpoint.NamespaceID
		fullyScoped := baseResolved && q.Scope.Allows(l.CanonicalID, l.Namespace) && q.Scope.Allows(r.CanonicalID, r.Namespace)
		localOnly := baseResolved && len(q.Scope.Resources) > 0 && slices.Contains(q.Scope.Namespaces, l.Namespace) && slices.Contains(q.Scope.Namespaces, r.Namespace) && slices.Contains([]string{"GetL7Context", "GetResourceNetworkHealth", "FindTCPRetransmission", "FindConnectionFailure", "FindPacketLoss"}, q.Template)
		if !fullyScoped {
			out.Partial = true
			out.Warnings = append(out.Warnings, "endpoint_unresolved_or_scope_limited")
			if !localOnly {
				continue
			}
		}

		tx, ok := numeric("retrans_tx")
		rx, ok2 := numeric("retrans_rx")
		if !ok || !ok2 {
			return degraded("invalid_counters")
		}
		status, statusOK := numeric("status")
		observed, observedOK := numeric("observed_at")
		point, pointOK := row[indices["tap_side"]].(string)
		statuses := []uint64{0, 2, 3, 4}
		if q.Template == "GetL7Context" {
			statuses = append(statuses, 5, 6)
		}
		if !statusOK || !slices.Contains(statuses, status) || !observedOK || observed > uint64(q.To.Unix()) || observed < uint64(q.From.Unix()) || !pointOK || !slices.Contains([]string{"c", "c-nd", "c-hv", "c-gw-hv", "c-gw", "local", "rest", "s-gw", "s-gw-hv", "s-hv", "s-nd", "s", "c-p", "s-p", "c-app", "s-app", "app"}, point) {
			return degraded("invalid_observation")
		}
		if q.Template == "FindConnectionFailure" && status != 3 && status != 4 || q.Template == "FindTCPRetransmission" && tx == 0 && rx == 0 {
			return degraded("operation_predicate_violated")
		}
		at := time.Unix(int64(observed), 0).UTC()
		if a.mode == "live" && (l.IdentityValidFrom.IsZero() || r.IdentityValidFrom.IsZero() || at.Before(l.IdentityValidFrom) || at.Before(r.IdentityValidFrom)) {
			return degraded("identity_lifetime_mismatch")
		}
		projected := map[string]any{"status": status, "observationPoint": point, "observedAt": at}
		if fullyScoped {
			projected["from"] = l.CanonicalID
			projected["to"] = r.CanonicalID
		} else {
			projected["resourceCanonicalId"] = q.ResourceCanonicalID
			projected["peerScopeLimited"] = true
		}
		if q.Template == "GetL7Context" {
			protocol, pok := numeric("l7_protocol")
			rrt, rrtok := numeric("rrt")
			if !pok || protocol == 0 || protocol > 65535 || !rrtok {
				return degraded("invalid_l7_metadata")
			}
			projected["protocolId"] = protocol
			projected["responseTimeMicroseconds"] = rrt
			if row[indices["response_code"]] != nil {
				code, cok := numeric("response_code")
				if !cok || code > 2147483647 {
					return degraded("invalid_response_code")
				}
				projected["responseCode"] = code
			}
		} else {
			projected["retransTx"] = tx
			projected["retransRx"] = rx
		}
		rows = append(rows, projected)
	}
	if q.Template == "GetNetworkPath" {
		out.Partial = true
		out.Warnings = append(out.Warnings, "path_incomplete_l4_observation_only")
	}
	if q.Template == "FindPacketLoss" {
		out.Partial = true
		out.Warnings = append(out.Warnings, "packet_loss_not_directly_observed; retransmission_is_only_supporting_evidence")
	}
	if a.mode == "fixture_only" {
		out.Warnings = append(out.Warnings, "fixture_only")
	}
	if err := a.authorize(ctx, a.binding); err != nil {
		return evidence.Result{}, fmt.Errorf("%w: final binding authorization", evidence.ErrScopeUnverified)
	}
	if a.mode == "live" {
		if err := a.mapping.(*LiveMapper).ValidateCurrent(ctx); err != nil {
			return evidence.Result{}, fmt.Errorf("%w: final native identity", evidence.ErrScopeUnverified)
		}
	}
	if len(rows) == 0 {
		out.Warnings = append(out.Warnings, "no_evidence")
		return out, nil
	}
	data, _ := json.Marshal(rows)
	if len(data) > q.ByteBudget() {
		// Keep a whole-row prefix within the admitted payload budget. These
		// observations still prove individual scoped relations, but cannot
		// prove complete coverage or population counts. Statistics use the
		// separate fixed aggregate path, never this truncated detail payload.
		low, high := 0, len(rows)
		for low < high {
			mid := (low + high + 1) / 2
			prefix, _ := json.Marshal(rows[:mid])
			if len(prefix) <= q.ByteBudget() {
				low = mid
			} else {
				high = mid - 1
			}
		}
		if low == 0 {
			return degraded("evidence_byte_budget")
		}
		rows = rows[:low]
		data, _ = json.Marshal(rows)
		out.Partial = true
		out.Warnings = append(out.Warnings, "evidence_byte_budget_exhausted")
	}
	observedFrom, observedTo := rows[0]["observedAt"].(time.Time), rows[0]["observedAt"].(time.Time)
	for _, row := range rows {
		at := row["observedAt"].(time.Time)
		if at.Before(observedFrom) {
			observedFrom = at
		}
		if at.After(observedTo) {
			observedTo = at
		}
	}
	out.Evidence = []evidence.Evidence{{SourceScopeDigest: evidence.BindingScopeDigest(a.binding), SchemaVersion: "evidence/v2", EvidenceID: uuid.Must(uuid.NewV7()).String(), TenantID: a.binding.Tenant, ResourceCanonicalID: q.ResourceCanonicalID, Type: "network", DataClass: "D1", SourceRegistrationID: a.binding.SourceID, SourceRevision: a.binding.Revision, SourceSystem: "deepflow", BackendLogicalID: a.binding.BackendLogicalID, QueryTemplateVersion: "deepflow-v7.2.0/" + q.Template, QueryHash: out.QueryHash, EffectiveScope: q.Scope, EvaluatedAt: time.Now(), ObservedFrom: observedFrom, ObservedTo: observedTo, SourceRetentionUntil: observedTo.Add(time.Duration(a.binding.SourceRetentionSeconds) * time.Second), TimeReliable: false, ReplayState: "archive_pending", ContentDigest: evidence.Digest(data), DerivationEvidenceRefs: []string{}, IndependenceGroup: a.binding.SourceID, Data: data}}
	return out, nil
}

func (a *Adapter) l7Available() bool {
	if !a.l7 {
		return false
	}
	if a.mode == "fixture_only" {
		return true
	}
	a.l7Mu.RLock()
	defer a.l7Mu.RUnlock()
	return a.l7Until.After(time.Now())
}

// QualifyL7 proves a bounded, nonempty actual L7 query separately from L4.
// Expiring proof cannot be enabled merely by a configuration boolean.
func (a *Adapter) QualifyL7(ctx context.Context, q evidence.Query) (err error) {
	// Keep an unexpired proof during renewal; a failed completed renewal clears
	// it. Every foreground query still revalidates source and native identities.
	defer func() {
		if err != nil {
			a.l7Mu.Lock()
			a.l7Until = time.Time{}
			a.l7Mu.Unlock()
		}
	}()
	if !a.l7 || a.mode != "live" {
		return ErrDisabled
	}
	q.Template = "GetL7Context"
	result, err := a.query(ctx, q, true)
	if err != nil {
		return err
	}
	if result.Partial || len(result.Evidence) == 0 || len(result.DegradedSources) > 0 {
		return evidence.ErrScopeUnverified
	}
	a.l7Mu.Lock()
	a.l7Until = time.Now().Add(3 * time.Minute)
	a.l7Mu.Unlock()
	return nil
}
