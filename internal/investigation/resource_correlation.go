package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

type ResourceSourceStep struct {
	StepID        uuid.UUID `json:"stepId"`
	Tool          string    `json:"tool"`
	CanonicalID   string    `json:"canonicalId"`
	ContentDigest string    `json:"contentDigest"`
}
type ResourceAssessment struct {
	CanonicalID     string      `json:"canonicalId"`
	RuleID          string      `json:"ruleId"`
	Reason          string      `json:"reason"`
	Supports        []uuid.UUID `json:"supports"`
	Conflicts       []uuid.UUID `json:"conflicts"`
	FactRefs        []FactRef   `json:"factRefs"`
	MissingEvidence []string    `json:"missingEvidence"`
	SourceQuality   string      `json:"sourceQuality"`
	NextChecks      []string    `json:"nextChecks"`
}

// Conclusions are finite, descriptive observations derived by the server from
// committed steps. The model never supplies rules, values, identity or windows.
func AssessResourceHistory(request FaultChainRequest, step uuid.UUID, h ResourceHistory) []ResourceAssessment {
	out := []ResourceAssessment{}
	add := func(rule, reason string, normal bool) {
		a := ResourceAssessment{CanonicalID: h.CanonicalID, RuleID: rule, Reason: reason, Supports: []uuid.UUID{}, Conflicts: []uuid.UUID{}, FactRefs: []FactRef{}, MissingEvidence: slices.Clone(h.MissingEvidence), SourceQuality: "observed_snapshot;event_time_not_inferred", NextChecks: []string{"resource_history", "resource_findings", "execute_analysis_query"}}
		a.MissingEvidence = append(a.MissingEvidence, "causal_mechanism_unverified")
		if normal {
			a.Conflicts = append(a.Conflicts, step)
		} else {
			a.Supports = append(a.Supports, step)
		}
		out = append(out, a)
	}
	previous := map[string]int64{}
	for _, o := range h.Records {
		if o.ObservedAt.Before(request.Fault.From) || !o.ObservedAt.Before(request.Fault.To) {
			continue
		}
		status, _ := o.Projection["status"].(map[string]any)
		kind, _ := o.Projection["kind"].(string)
		phase, _ := status["phase"].(string)
		if kind == "PersistentVolumeClaim" && phase == "Pending" {
			add("PVC/pending", "原 UID 的 PVC 在故障窗观察到 Pending；需结合供给、附加或挂载事件检查，未确认 CSI 故障。", false)
		}
		if kind == "PersistentVolumeClaim" && phase == "Bound" {
			add("PVC/bound-counterevidence", "PVC 已绑定，与未绑定候选相矛盾；Bound 不证明挂载、读写或恢复完成。", true)
		}
		for _, raw := range anySlice(status["conditions"]) {
			c, _ := raw.(map[string]any)
			typ, _ := c["type"].(string)
			value, _ := c["status"].(string)
			switch {
			case typ == "Ready" && value == "False":
				add("K8S/not-ready", "故障窗资源 Ready=False；仅说明自身状态异常，需核对同窗指标与原生事件。", false)
			case typ == "Ready" && value == "True":
				add("K8S/ready-counterevidence", "故障窗存在 Ready=True 观察，仅与该时刻 NotReady 候选相矛盾，不代表全窗正常。", true)
			case kind == "Node" && slices.Contains([]string{"MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"}, typ) && value == "True":
				add("Node/"+typ, "Node 原生条件 "+typ+"=True，支持检查该节点；关联影响不等于共同根因。", false)
			case kind == "Pod" && typ == "PodScheduled" && value == "False":
				add("Pod/scheduling", "PodScheduled=False，支持检查调度与授权范围内的节点、存储事件。", false)
			}
		}
		for _, field := range []string{"containerStatuses", "initContainerStatuses"} {
			for _, raw := range anySlice(status[field]) {
				c, _ := raw.(map[string]any)
				name, _ := c["name"].(string)
				count, ok := numberInt(c["restartCount"])
				key := field + "/" + name
				if prior, seen := previous[key]; ok && seen && count > prior {
					add("Pod/restart-increase", fmt.Sprintf("同一 Pod UID 的容器 %s 重启计数在两个故障窗观察间增加；需结合退出原因、指标和日志检查。", name), false)
				}
				if ok {
					previous[key] = count
				}
				state, _ := c["state"].(map[string]any)
				if waiting, ok := state["waiting"].(map[string]any); ok {
					reason, _ := waiting["reason"].(string)
					if slices.Contains([]string{"CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError", "CreateContainerError", "ContainerCreating"}, reason) {
						add("Pod/waiting", "容器等待原因 "+reason+"，仅表示该观察时刻的运行事实。", false)
					}
				}
				for _, stateKey := range []string{"state", "lastState"} {
					states, _ := c[stateKey].(map[string]any)
					term, _ := states["terminated"].(map[string]any)
					reason, _ := term["reason"].(string)
					stamp, _ := term["finishedAt"].(string)
					at, err := time.Parse(time.RFC3339Nano, stamp)
					if err != nil || at.Before(request.Fault.From) || !at.Before(request.Fault.To) {
						continue
					}
					exit, _ := numberInt(term["exitCode"])
					if reason == "OOMKilled" {
						add("Pod/oom-exit", "容器终止记录在故障窗报告 OOMKilled；不推断 Node 内存耗尽或具体泄漏原因。", false)
					} else if exit != 0 {
						add("Pod/nonzero-exit", "容器终止记录在故障窗报告非零退出；具体原因待事件与日志补证。", false)
					}
				}
			}
		}
	}
	for _, event := range h.Events {
		if event.EventAt == nil || event.EventAt.Before(request.Fault.From) || !event.EventAt.Before(request.Fault.To) {
			continue
		}
		reason, _ := event.Projection["reason"].(string)
		typ, _ := event.Projection["type"].(string)
		if typ == "Warning" && slices.Contains([]string{"FailedScheduling", "FailedMount", "FailedAttachVolume", "FailedBinding", "ProvisioningFailed", "BackOff", "Unhealthy", "Failed"}, reason) {
			add("K8S/event/"+reason, "归属原 UID 的原生 Warning 事件报告 "+reason+"；未采用自由消息关键词推断原因。", false)
		}
	}
	slices.SortFunc(out, func(a, b ResourceAssessment) int { return strings.Compare(graph.ScopeDigest(a), graph.ScopeDigest(b)) })
	return slices.CompactFunc(out, func(a, b ResourceAssessment) bool { return graph.ScopeDigest(a) == graph.ScopeDigest(b) })
}
func anySlice(v any) []any { s, _ := v.([]any); return s }
func numberInt(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), n >= 0 && n == float64(int64(n))
	case int64:
		return n, n >= 0
	}
	return 0, false
}

func CommittedResourceHistoryHealth(step uuid.UUID, h ResourceHistory) []FaultChainResourceHealth {
	out := []FaultChainResourceHealth{}
	for _, o := range h.Records {
		id, err := resource.ParseCanonicalID(o.CanonicalID)
		if err != nil || !slices.Contains([]string{"Pod", "Node"}, id.Kind) {
			continue
		}
		status, _ := o.Projection["status"].(map[string]any)
		for _, raw := range anySlice(status["conditions"]) {
			c, _ := raw.(map[string]any)
			value, _ := c["status"].(string)
			if c["type"] == "Ready" && slices.Contains([]string{"True", "False", "Unknown"}, value) {
				out = append(out, FaultChainResourceHealth{StepID: step, CanonicalID: o.CanonicalID, Namespace: o.Namespace, ObservedAt: o.ObservedAt, ConditionType: "Ready", ConditionStatus: value, Sources: []graph.SourceAuthority{o.Source}})
			}
		}
	}
	// One latest observation per condition status retains abnormal and normal
	// counterevidence without allowing a busy watch stream to exhaust R3/R5.
	latest := map[string]FaultChainResourceHealth{}
	for _, h := range out {
		prior, ok := latest[h.ConditionStatus]
		if !ok || h.ObservedAt.After(prior.ObservedAt) {
			latest[h.ConditionStatus] = h
		}
	}
	out = []FaultChainResourceHealth{}
	for _, status := range []string{"False", "True", "Unknown"} {
		if h, ok := latest[status]; ok {
			out = append(out, h)
		}
	}
	return out
}

func ReadResourceSourceStepTx(ctx context.Context, tx pgx.Tx, j Job, ref ResourceSourceStep) (json.RawMessage, error) {
	var raw []byte
	var tool string
	if tx.QueryRow(ctx, `SELECT tool_name,result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND state='succeeded'`, j.TenantID, j.JobID, ref.StepID).Scan(&tool, &raw) != nil || tool != ref.Tool || graph.ScopeDigest(json.RawMessage(raw)) != ref.ContentDigest {
		return nil, ErrDenied
	}
	if slices.Contains([]string{"resource_history", "resource_findings", "resource_changes", "execute_analysis_query"}, tool) {
		var wire struct {
			Data struct {
				CanonicalID string `json:"canonicalId"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &wire) != nil || wire.Data.CanonicalID != ref.CanonicalID {
			return nil, ErrDenied
		}
	}
	if err := authorizeStepResult(ctx, tx, j, ref.StepID, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func AssessResourceQuery(request FaultChainRequest, canonical string, step uuid.UUID, q StoredQueryResult, structuredErrors bool) []ResourceAssessment {
	r := q.Result
	if canonical == "" || r.NetworkContext != nil || r.TimeRange == nil || *r.TimeRange != request.Fault || r.Status != "complete" || !r.Exact || r.Truncated || len(r.DegradedSources) > 0 || validateStoredQueryResult(q) != nil {
		return nil
	}
	values := map[string]float64{}
	for _, row := range r.Rows {
		values[row.Dimension] = row.Value
	}
	current, ok := values["current"]
	if !ok && len(r.Rows) == 1 {
		current = r.Rows[0].Value
		ok = true
	}
	if !ok {
		return nil
	}
	rule, reason := "", ""
	normal := false
	switch r.DatasetID {
	case "metrics.pod-restarts":
		if current > 0 {
			rule = "Pod/restart-statistic"
			reason = "固定来源在故障窗记录重启增量；与原生容器状态一起检查，未确认 OOM 或其他原因。"
		} else {
			rule = "Pod/no-observed-restart"
			reason = "完整来源在该统计窗口的重启增量为零，仅反驳该窗口发生重启的候选。"
			normal = true
		}
	case "metrics.pod-memory", "metrics.pod-cpu":
		baseline, has := values["baseline"]
		if has && r.ComparisonTimeRange != nil && request.Baseline != nil && *r.ComparisonTimeRange == *request.Baseline && current > baseline {
			rule = "K8S/resource-metric-increase"
			reason = "资源指标较冻结基线上升；仅表示变化，不能据此判断资源耗尽。"
		}
	case "logs.records":
		if !structuredErrors {
			return nil
		}
		if current > 0 {
			rule = "K8S/structured-error-logs"
			reason = "固定日志来源按结构化 error 等级统计到日志；不使用自由关键词推断故障原因。"
		} else {
			rule = "K8S/no-structured-error-logs"
			reason = "完整日志来源在该窗口未观察到结构化 error 等级；不能证明应用正常。"
			normal = true
		}
	case "metrics.node-ready":
		if current == 0 {
			rule = "Node/ready-statistic"
			reason = "固定 UID 指标来源在窗口末端未记录 Ready=True；结合 Node 原生状态检查。"
		} else {
			rule = "Node/ready-statistic-counterevidence"
			reason = "固定 UID 指标在窗口末端记录 Ready=True，仅与该时刻未就绪候选相矛盾。"
			normal = true
		}
	}
	if rule == "" {
		return nil
	}
	a := ResourceAssessment{CanonicalID: canonical, RuleID: rule, Reason: reason, Supports: []uuid.UUID{}, Conflicts: []uuid.UUID{}, FactRefs: []FactRef{}, MissingEvidence: []string{"causal_mechanism_unverified"}, SourceQuality: "qualified_fixed_source;exact_window_statistic", NextChecks: []string{"resource_history", "resource_findings", "execute_analysis_query"}}
	if normal {
		a.Conflicts = []uuid.UUID{step}
	} else {
		a.Supports = []uuid.UUID{step}
	}
	for _, f := range q.Facts {
		a.FactRefs = append(a.FactRefs, FactRef{q.QueryResultID, f.FactID})
	}
	if len(a.FactRefs) > 100 {
		a.FactRefs = a.FactRefs[:100]
	}
	return []ResourceAssessment{a}
}

func ReadResourceSourceStepForClientTx(ctx context.Context, tx pgx.Tx, j Job, ref ResourceSourceStep) (json.RawMessage, error) {
	data, err := ReadResourceSourceStepTx(ctx, tx, j, ref)
	if err != nil {
		return nil, err
	}
	if err := chargeRead(ctx, tx, j, "resource-source-step", int64(len(data))); err != nil {
		return nil, err
	}
	return data, nil
}
