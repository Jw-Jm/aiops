package investigation

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
)

const FaultChainRuleVersion = "fault-chain-rules/v1"

var faultChainRelationDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func ReadCommittedQueryResultTx(ctx context.Context, tx pgx.Tx, j Job, id uuid.UUID) (StoredQueryResult, error) {
	return loadQueryResultTx(ctx, tx, j, id)
}

type FaultChainReadOnlyCheck struct {
	OriginTurnID   uuid.UUID `json:"originTurnId"`
	RelationDigest string    `json:"relationDigest,omitempty"`
	GapCode        string    `json:"gapCode"`
	CanonicalID    string    `json:"canonicalId"`
	Tool           string    `json:"tool"`
	Direction      string    `json:"direction,omitempty"`
}

// Only these published read-only tools have a meaningful check for these gaps.
func FaultChainGapTool(gap string) string {
	switch gap {
	case "shared_resource_cause_not_checked", "recovery_baseline_and_health_unverified":
		return "get_resource_health"
	case "historical_relation_unverified", "committed_scoped_resource_relations":
		return "get_dependencies"
	case "network_window_statistics", "complete_collector_coverage":
		return "execute_analysis_query"
	}
	return ""
}

type FaultChainRequest struct {
	ReadOnlyCheck     *FaultChainReadOnlyCheck  `json:"readOnlyCheck,omitempty"`
	SchemaVersion     string                    `json:"schemaVersion"`
	TargetCanonicalID string                    `json:"targetCanonicalId"`
	Fault             analytics.QueryTimeRange  `json:"fault"`
	Baseline          *analytics.QueryTimeRange `json:"baseline,omitempty"`
	Recovery          *analytics.QueryTimeRange `json:"recovery,omitempty"`
	MaxEdges          int                       `json:"maxEdges"`
}

func (r FaultChainRequest) Valid(scope graph.Scope, timezone string, now time.Time) bool {
	if c := r.ReadOnlyCheck; c != nil {
		if c.OriginTurnID == uuid.Nil || c.CanonicalID == "" || c.Tool == "" || FaultChainGapTool(c.GapCode) != c.Tool {
			return false
		}
		if c.GapCode == "committed_scoped_resource_relations" {
			if c.RelationDigest != "" {
				return false
			}
		} else if !faultChainRelationDigest.MatchString(c.RelationDigest) {
			return false
		}
		if c.Tool == "execute_analysis_query" {
			peer, err := resource.ParseCanonicalID(c.CanonicalID)
			if err != nil || peer.Kind != "Pod" || !slices.Contains([]string{"incoming", "outgoing", "either"}, c.Direction) {
				return false
			}
		} else if c.Direction != "" {
			return false
		}
	}
	id, err := resource.ParseCanonicalID(r.TargetCanonicalID)
	if err != nil || id.Tenant != scope.Tenant || id.Scope != scope.Cluster || id.Domain != "k8s" || id.APIGroup != "core" {
		return false
	}
	if !slices.Contains([]string{"fault-chain-request/v1", "fault-chain-request/v2"}, r.SchemaVersion) || r.MaxEdges < 1 || r.MaxEdges > 8 || len(scope.Resources) > 0 && !slices.Contains(scope.Resources, r.TargetCanonicalID) {
		return false
	}
	if id.Kind == "Node" {
		if r.SchemaVersion != "fault-chain-request/v2" || !scope.ClusterScoped || len(scope.Namespaces) > 1 {
			return false
		}
	} else if (id.Kind != "Pod" && (r.SchemaVersion != "fault-chain-request/v2" || id.Kind != "PersistentVolumeClaim")) || len(scope.Namespaces) != 1 {
		return false
	}
	for _, w := range []*analytics.QueryTimeRange{&r.Fault, r.Baseline, r.Recovery} {
		if w == nil {
			continue
		}
		if w.From.IsZero() || !w.To.After(w.From) || w.To.Sub(w.From) > time.Hour || w.To.After(now) || w.Timezone != timezone || w.Timezone == "Local" || w.From.Nanosecond() != 0 || w.To.Nanosecond() != 0 {
			return false
		}
	}
	if _, err = time.LoadLocation(timezone); err != nil {
		return false
	}
	if r.Baseline != nil && (r.Baseline.To.After(r.Fault.From) || r.Baseline.To.Sub(r.Baseline.From) != r.Fault.To.Sub(r.Fault.From)) {
		return false
	}
	if r.Recovery != nil && (r.Recovery.From.Before(r.Fault.To) || r.Recovery.To.Sub(r.Recovery.From) != r.Fault.To.Sub(r.Fault.From)) {
		return false
	}
	return true
}
func (r FaultChainRequest) Allows(window *analytics.QueryTimeRange) bool {
	if window == nil {
		return false
	}
	for _, w := range []*analytics.QueryTimeRange{&r.Fault, r.Baseline, r.Recovery} {
		if w != nil && w.From.Equal(window.From) && w.To.Equal(window.To) && w.Timezone == window.Timezone {
			return true
		}
	}
	return false
}

type FaultChainContextRef struct {
	StepID        uuid.UUID `json:"stepId"`
	ContentDigest string    `json:"contentDigest"`
}
type ChainObservation struct {
	ResourceHealth   *FaultChainResourceHealth `json:"resourceHealth,omitempty"`
	RelatedRelations []resource.Relation       `json:"relatedRelations,omitempty"`
	RuleID           string                    `json:"ruleId"`
	Reason           string                    `json:"reason"`
	Supports         []string                  `json:"supports"`
	Conflicts        []string                  `json:"conflicts"`
	FactRefs         []FactRef                 `json:"factRefs"`
}
type FaultChainEdge struct {
	Relation           resource.Relation  `json:"relation"`
	RelationDigest     string             `json:"relationDigest"`
	CallDirection      string             `json:"callDirection"`
	TemporalAssessment string             `json:"temporalAssessment"`
	ComparisonStatus   string             `json:"comparisonStatus"`
	QueryResultRefs    []uuid.UUID        `json:"queryResultRefs"`
	Observations       []ChainObservation `json:"observations"`
	MissingEvidence    []string           `json:"missingEvidence"`
	NextChecks         []string           `json:"nextChecks"`
}
type FaultChainContext struct {
	SourceSteps          []ResourceSourceStep       `json:"sourceSteps,omitempty"`
	ResourceAssessments  []ResourceAssessment       `json:"resourceAssessments,omitempty"`
	ResourceHealthChecks []FaultChainResourceHealth `json:"resourceHealthChecks,omitempty"`
	SchemaVersion        string                     `json:"schemaVersion"`
	RuleVersion          string                     `json:"ruleVersion"`
	Request              FaultChainRequest          `json:"request"`
	AsOf                 time.Time                  `json:"asOf"`
	Status               string                     `json:"status"`
	Edges                []FaultChainEdge           `json:"edges"`
	QueryResultRefs      []uuid.UUID                `json:"queryResultRefs"`
	EvidenceRefs         []string                   `json:"evidenceRefs"`
	MissingEvidence      []string                   `json:"missingEvidence"`
}

// EvaluateFaultChain contains finite descriptive predicates. It never changes
// Incident/Finding/RCA state or treats co-occurrence as causal confirmation.
func EvaluateFaultChain(request FaultChainRequest, relations []resource.Relation, queries []StoredQueryResult, now time.Time, committedEvidence ...[]evidence.Evidence) (FaultChainContext, error) {
	out := FaultChainContext{SchemaVersion: "fault-chain-context/v1", RuleVersion: FaultChainRuleVersion, Request: request, AsOf: now, Status: "partial", Edges: []FaultChainEdge{}, QueryResultRefs: []uuid.UUID{}, EvidenceRefs: []string{}, MissingEvidence: []string{}}
	if request.MaxEdges < 1 || request.MaxEdges > 8 {
		return out, ErrInvalid
	}
	changes := []evidence.Evidence{}
	for _, items := range committedEvidence {
		changes = append(changes, items...)
		if len(changes) > 80 {
			return out, ErrInvalid
		}
	}
	slices.SortFunc(changes, func(a, b evidence.Evidence) int { return slices.Compare([]byte(a.EvidenceID), []byte(b.EvidenceID)) })
	relations = slices.Clone(relations)
	slices.SortFunc(relations, func(a, b resource.Relation) int {
		if (a.Kind == "communicates_with") != (b.Kind == "communicates_with") {
			if a.Kind == "communicates_with" {
				return -1
			}
			return 1
		}
		if a.Kind == "communicates_with" && b.Kind == "communicates_with" {
			peer := func(r resource.Relation) string {
				if r.From == request.TargetCanonicalID {
					return r.To
				}
				return r.From
			}
			if order := slices.Compare([]byte(peer(a)), []byte(peer(b))); order != 0 {
				return order
			}
			if (a.From == request.TargetCanonicalID) != (b.From == request.TargetCanonicalID) {
				if a.From == request.TargetCanonicalID {
					return -1
				}
				return 1
			}
		}
		return slices.Compare([]byte(graph.ScopeDigest(a)), []byte(graph.ScopeDigest(b)))
	})
	seen := map[string]bool{}
	for _, r := range relations {
		if r.From == "" || r.To == "" || r.Kind == "" || r.ObservedAt.IsZero() || r.Provenance.SourceRegistrationID == "" {
			continue
		}
		if len(out.Edges) >= request.MaxEdges {
			out.MissingEvidence = append(out.MissingEvidence, "remaining_relations_not_checked")
			break
		}
		if r.From != request.TargetCanonicalID && r.To != request.TargetCanonicalID {
			continue
		}
		digest := graph.ScopeDigest(r)
		if seen[digest] {
			continue
		}
		seen[digest] = true
		edge := FaultChainEdge{Relation: r, RelationDigest: digest, CallDirection: "unknown", TemporalAssessment: "unreliable", ComparisonStatus: "baseline_missing", QueryResultRefs: []uuid.UUID{}, Observations: []ChainObservation{}, MissingEvidence: []string{"causal_mechanism_unverified", "clock_and_event_time_unverified", "historical_relation_unverified", "change_source_not_checked", "shared_resource_cause_not_checked"}, NextChecks: []string{"核对两端应用日志与依赖自身状态", "检查符合范围的 Node 与变更证据"}}
		for _, q := range queries {
			n := q.Result.NetworkContext
			if n == nil {
				continue
			}
			s := n.Selection
			if !((s.TargetCanonicalID == r.From && s.PeerCanonicalID == r.To) || (s.TargetCanonicalID == r.To && s.PeerCanonicalID == r.From)) {
				continue
			}
			// Each directed relation keeps its own observation population.
			from, to := s.TargetCanonicalID, s.PeerCanonicalID
			if s.Direction == "incoming" {
				from, to = to, from
			}
			if r.Kind == "communicates_with" && s.Direction != "either" && (r.From != from || r.To != to) {
				continue
			}
			if !request.Allows(q.Result.TimeRange) || q.Result.ComparisonTimeRange != nil && !request.Allows(q.Result.ComparisonTimeRange) {
				return out, ErrDenied
			}
			if validateStoredQueryResult(q) != nil {
				return out, ErrInvalid
			}
			edge.QueryResultRefs = append(edge.QueryResultRefs, q.QueryResultID)
			out.QueryResultRefs = append(out.QueryResultRefs, q.QueryResultID)
			out.EvidenceRefs = append(out.EvidenceRefs, q.Result.DerivationEvidenceRefs...)
			// The query's fixed outgoing/incoming pair establishes observation
			// direction; graph edge direction itself is never relabelled as propagation.
			if s.Direction == "outgoing" {
				edge.CallDirection = s.TargetCanonicalID + " → " + s.PeerCanonicalID
			} else if s.Direction == "incoming" {
				edge.CallDirection = s.PeerCanonicalID + " → " + s.TargetCanonicalID
			}
			if !q.Result.Exact || !n.CoverageComplete || q.Result.Truncated {
				edge.ComparisonStatus = "coverage_insufficient"
				// An observation with no population is a gap in what was
				// observed, not proof that the collector coverage is unknown.
				// Name the reason that applies so the gap check does not send
				// the caller to re-read a window that has nothing to read.
				if len(q.Result.Rows) == 0 && q.Result.Status == "no_data" {
					edge.MissingEvidence = append(edge.MissingEvidence, "nonempty_observation_population")
				} else {
					edge.MissingEvidence = append(edge.MissingEvidence, "complete_collector_coverage")
				}
				continue
			}
			values := map[string]float64{}
			for _, row := range q.Result.Rows {
				values[row.Dimension] = row.Value
			}
			current, currentOK := values["current"]
			baseline, baseOK := values["baseline"]
			if q.Result.Status == "no_data" || !currentOK {
				edge.MissingEvidence = append(edge.MissingEvidence, "nonempty_observation_population")
				continue
			}
			errors := float64(-1)
			if q.Result.NetworkContext.Protocol == "http" {
				population := float64(0)
				for _, stat := range q.Result.Statistics {
					if stat.Key == "current.responses" {
						population = stat.Value
					}
					if stat.Key == "current.5xx" {
						errors = stat.Value
					}
				}
				if baseOK {
					baselinePopulation := float64(0)
					for _, stat := range q.Result.Statistics {
						if stat.Key == "baseline.responses" {
							baselinePopulation = stat.Value
						}
					}
					if baselinePopulation == 0 {
						edge.MissingEvidence = append(edge.MissingEvidence, "nonempty_baseline_population")
						edge.ComparisonStatus = "coverage_insufficient"
						baseOK = false
					}
				}
				if population == 0 {
					edge.MissingEvidence = append(edge.MissingEvidence, "nonempty_observation_population")
					continue
				}
			}
			factRefs := []FactRef{}
			for _, f := range q.Facts {
				if f.Column != "value" {
					continue
				}
				factRefs = append(factRefs, FactRef{q.QueryResultID, f.FactID})
			}
			ob := ChainObservation{RuleID: "observation/v1", Reason: "已提交的网络观测；未证明故障原因。", Supports: []string{}, Conflicts: []string{}, FactRefs: factRefs}
			if !baseOK && errors > 0 && (q.Result.DatasetID == "network.http-5xx-rate" || q.Result.DatasetID == "network.http-5xx" || q.Result.DatasetID == "network.http-summary") {
				ob.RuleID = "R1/observed-http-5xx"
				ob.Reason = "合格完成响应中观察到 HTTP 5xx，支持调查响应异常；缺少可用基线及服务端自身证据，不能确认具体原因。"
				if len(q.Result.DerivationEvidenceRefs) > 0 {
					ob.Supports = []string{q.Result.DerivationEvidenceRefs[len(q.Result.DerivationEvidenceRefs)-1]}
				}
			}
			if baseOK {
				edge.ComparisonStatus = "comparable"
				if current > baseline {
					switch q.Result.DatasetID {
					case "network.http-5xx", "network.http-5xx-rate", "network.http-summary":
						ob.RuleID = "R1/http-response-increase"
						ob.Reason = "故障窗 HTTP 5xx 观测较基线增加，建议检查服务端及其依赖。"
					case "network.http-response-time":
						ob.RuleID = "R1/http-latency-increase"
						ob.Reason = "完成 HTTP 响应平均耗时增加，具体延迟来源尚待检查。"
					case "network.tcp-retransmissions", "network.connection-failure-observations", "network.tcp-summary":
						ob.RuleID = "R2/transport-observation-increase"
						ob.Reason = "传输异常观测较基线增加；不等同于已测得丢包或因果。"
					}
					if ob.RuleID != "observation/v1" {
						ob.Supports = append(ob.Supports, q.Result.DerivationEvidenceRefs...)
					}
				}
			}
			// Zero contradicts only the narrow 5xx claim for a nonempty population.
			if q.Result.DatasetID == "network.http-5xx" && current == 0 || (q.Result.DatasetID == "network.http-5xx-rate" || q.Result.DatasetID == "network.http-summary") && errors == 0 {
				ob.RuleID = "R6/no-observed-http-5xx"
				ob.Reason = "合格完成会话中未观察到 5xx，仅与该窗口存在 5xx 的候选相矛盾。"
				if len(q.Result.DerivationEvidenceRefs) > 0 {
					ob.Conflicts = append(ob.Conflicts, q.Result.DerivationEvidenceRefs[len(q.Result.DerivationEvidenceRefs)-1])
				}
			}
			if request.Recovery != nil && q.Result.TimeRange.From.Equal(request.Recovery.From) {
				ob.RuleID = "R5/recovery-observation"
				ob.Supports = []string{}
				ob.Conflicts = []string{}
				ob.Reason = "恢复窗口的独立观测，缺少与故障窗口的同口径比较。"
				if baseOK && q.Result.ComparisonTimeRange != nil && q.Result.ComparisonTimeRange.From.Equal(request.Fault.From) {
					if current < baseline {
						ob.Reason = "恢复窗口的异常指标低于故障窗口，支持改善观察；不能据此自动关闭 Incident。"
						ob.Supports = slices.Clone(q.Result.DerivationEvidenceRefs)
					} else {
						ob.Reason = "恢复窗口的异常指标没有低于故障窗口，尚未观察到改善。"
						ob.Supports = []string{}
					}
				}
			}
			edge.Observations = append(edge.Observations, ob)
			// Additional fields of a qualified combined snapshot retain their
			// own units and Facts; they do not require another source read.
			for _, metric := range []struct{ dataset, key, rule, reason string }{
				{"network.http-summary", "mean_response_ms", "R1/http-latency-increase", "同口径完成响应的平均耗时增加，具体延迟来源尚待检查。"},
				{"network.tcp-summary", "retransmissions", "R2/retransmission-observation-increase", "同口径重传计数增加，支持检查传输路径；不能据此测得丢包率或确认因果。"},
			} {
				if q.Result.DatasetID != metric.dataset {
					continue
				}
				stats := map[string]float64{}
				refs := []FactRef{}
				for i, stat := range q.Result.Statistics {
					stats[stat.Key] = stat.Value
					if stat.Key == "baseline."+metric.key || stat.Key == "current."+metric.key {
						for _, f := range q.Facts {
							if f.Column == "statistic" && f.Row == i {
								refs = append(refs, FactRef{q.QueryResultID, f.FactID})
							}
						}
					}
				}
				value, ok := stats["current."+metric.key]
				prior, hasPrior := stats["baseline."+metric.key]
				if !ok || !hasPrior || !baseOK {
					continue
				}
				extra := ChainObservation{RuleID: metric.rule, Reason: metric.reason, Supports: []string{}, Conflicts: []string{}, FactRefs: refs}
				if request.Recovery != nil && q.Result.TimeRange.From.Equal(request.Recovery.From) {
					extra.RuleID = "R5/recovery-observation"
					extra.Reason = "恢复窗口的同口径指标没有降低，尚未观察到改善。"
					if value < prior {
						extra.Reason = "恢复窗口的同口径指标低于故障窗口，支持改善观察；不自动关闭 Incident。"
						extra.Supports = slices.Clone(q.Result.DerivationEvidenceRefs)
					}
				} else if value > prior {
					extra.Supports = slices.Clone(q.Result.DerivationEvidenceRefs)
				} else {
					continue
				}
				edge.Observations = append(edge.Observations, extra)
			}
		}
		if len(edge.QueryResultRefs) == 0 {
			if request.SchemaVersion == "fault-chain-request/v2" && r.Kind != "communicates_with" {
				edge.NextChecks = []string{"核对授权范围内资源的历史健康、原生事件与 Finding"}
			} else {
				edge.MissingEvidence = append(edge.MissingEvidence, "network_window_statistics")
			}
		}
		applyFaultChainChanges(request, &edge, queries, changes, &out)
		out.Edges = append(out.Edges, edge)
	}
	// A linked check retains committed statistics even if the current graph
	// no longer contains the original historical relation.
	if c := request.ReadOnlyCheck; c != nil && c.Tool == "execute_analysis_query" {
		for _, q := range queries {
			n := q.Result.NetworkContext
			if n != nil && n.Selection.TargetCanonicalID == request.TargetCanonicalID && n.Selection.PeerCanonicalID == c.CanonicalID && string(n.Selection.Direction) == c.Direction && request.Allows(q.Result.TimeRange) {
				out.QueryResultRefs = append(out.QueryResultRefs, q.QueryResultID)
				out.EvidenceRefs = append(out.EvidenceRefs, q.Result.DerivationEvidenceRefs...)
			}
		}
	}
	slices.Sort(out.EvidenceRefs)
	out.EvidenceRefs = slices.Compact(out.EvidenceRefs)
	slices.SortFunc(out.QueryResultRefs, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	out.QueryResultRefs = slices.Compact(out.QueryResultRefs)
	if len(out.Edges) == 0 {
		out.MissingEvidence = append(out.MissingEvidence, "committed_scoped_resource_relations")
	}
	return out, nil
}

// ReadFaultChainContextTx accepts only a committed evaluator Step of this Job.
// All parent query/source references are reauthorized on every read.
func ReadFaultChainContextTx(ctx context.Context, tx pgx.Tx, j Job, ref FaultChainContextRef) (FaultChainContext, error) {
	var out FaultChainContext
	var rawResult []byte
	if CheckAssistantJobTx(ctx, tx, j) != nil {
		return out, ErrDenied
	}
	if tx.QueryRow(ctx, `SELECT result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND tool_name='evaluate_fault_chain' AND state='succeeded'`, j.TenantID, j.JobID, ref.StepID).Scan(&rawResult) != nil {
		return out, ErrDenied
	}
	var wire struct {
		Data struct {
			Context FaultChainContext `json:"context"`
		} `json:"data"`
	}
	if json.Unmarshal(rawResult, &wire) != nil || graph.ScopeDigest(wire.Data.Context) != ref.ContentDigest {
		return out, ErrDenied
	}
	out = wire.Data.Context
	for _, ref := range out.SourceSteps {
		if _, err := ReadResourceSourceStepTx(ctx, tx, j, ref); err != nil {
			return FaultChainContext{}, err
		}
	}
	if contract.Validate("https://ops.local/schemas/fault-chain-context/v1", raw(out)) != nil {
		return FaultChainContext{}, ErrInvalid
	}
	for _, h := range out.ResourceHealthChecks {
		if err := readFaultChainHealthTx(ctx, tx, j, h); err != nil {
			return FaultChainContext{}, err
		}
	}
	for _, edge := range out.Edges {
		for _, ob := range edge.Observations {
			if ob.ResourceHealth == nil {
				continue
			}
			h := ob.ResourceHealth
			if err := readFaultChainHealthTx(ctx, tx, j, *h); err != nil {
				return FaultChainContext{}, err
			}
			for _, relation := range ob.RelatedRelations {
				for _, id := range []string{relation.From, relation.To} {
					var ns string
					if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, id).Scan(&ns) != nil || !j.Scope.Allows(id, ns) {
						return FaultChainContext{}, ErrDenied
					}
				}
				var active bool
				if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.source_registrations WHERE tenant_id=$1 AND source_id::text=$2 AND status='active')`, j.TenantID, relation.Provenance.SourceRegistrationID).Scan(&active) != nil || !active {
					return FaultChainContext{}, ErrDenied
				}
			}
		}
		for _, id := range []string{edge.Relation.From, edge.Relation.To} {
			var ns string
			if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, id).Scan(&ns) != nil || !j.Scope.Allows(id, ns) {
				return FaultChainContext{}, ErrDenied
			}
		}
		var active bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.source_registrations WHERE tenant_id=$1 AND source_id::text=$2 AND status='active')`, j.TenantID, edge.Relation.Provenance.SourceRegistrationID).Scan(&active) != nil || !active {
			return FaultChainContext{}, ErrDenied
		}
	}
	for _, id := range out.EvidenceRefs {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return FaultChainContext{}, ErrDenied
		}
		e, err := evidence.GetTx(ctx, tx, j.TenantID, parsed, j.Scope)
		if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, e.DataClass) {
			return FaultChainContext{}, ErrDenied
		}
	}
	for _, id := range out.QueryResultRefs {
		if _, err := loadQueryResultTx(ctx, tx, j, id); err != nil {
			return FaultChainContext{}, err
		}
	}
	return out, nil
}

func readFaultChainHealthTx(ctx context.Context, tx pgx.Tx, j Job, h FaultChainResourceHealth) error {
	var rawStep []byte
	if tx.QueryRow(ctx, `SELECT result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND tool_name IN('get_resource_health','resource_history') AND state='succeeded'`, j.TenantID, j.JobID, h.StepID).Scan(&rawStep) != nil {
		return ErrDenied
	}
	var parent struct {
		Tool string          `json:"tool"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(rawStep, &parent) != nil {
		return ErrDenied
	}
	if parent.Tool == "resource_history" {
		var history ResourceHistory
		if json.Unmarshal(parent.Data, &history) != nil || authorizeResourceToolTx(ctx, tx, j, parent.Tool, parent.Data) != nil {
			return ErrDenied
		}
		for _, candidate := range CommittedResourceHistoryHealth(h.StepID, history) {
			if graph.ScopeDigest(candidate) == graph.ScopeDigest(h) {
				return nil
			}
		}
		return ErrDenied
	}
	if parent.Tool != "get_resource_health" {
		return ErrDenied
	}
	snapshot, err := decodeFaultChainHealthSnapshot(parent.Data)
	if err != nil || !j.Scope.Allows(h.CanonicalID, h.Namespace) || rca.CheckGraphSources(ctx, tx, j.TenantID, snapshot.ObservationSources, snapshot) != nil {
		return ErrDenied
	}
	matched := false
	for _, candidate := range CommittedFaultChainResourceHealth(h.StepID, snapshot) {
		if graph.ScopeDigest(candidate) == graph.ScopeDigest(h) {
			matched = true
		}
	}
	if !matched {
		return ErrDenied
	}
	return nil
}
