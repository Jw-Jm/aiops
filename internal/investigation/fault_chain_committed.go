package investigation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"slices"
	"time"
)

func EvaluateCommittedFaultChainTx(ctx context.Context, tx pgx.Tx, j Job, request FaultChainRequest, stepIDs []uuid.UUID) (FaultChainContext, error) {
	var out FaultChainContext
	if len(stepIDs) < 1 || len(stepIDs) > 40 {
		return out, ErrInvalid
	}
	err := func() error {
		if CheckAssistantJobTx(ctx, tx, j) != nil {
			return ErrDenied
		}
		relations := []resource.Relation{}
		queries := []StoredQueryResult{}
		changes := []evidence.Evidence{}
		health := []FaultChainResourceHealth{}
		sourceSteps := []ResourceSourceStep{}
		assessments := []ResourceAssessment{}
		seen := map[uuid.UUID]bool{}
		for _, id := range stepIDs {
			if id == uuid.Nil || seen[id] {
				return ErrInvalid
			}
			seen[id] = true
			var tool string
			var raw []byte
			if tx.QueryRow(ctx, `SELECT tool_name,result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND state='succeeded'`, j.TenantID, j.JobID, id).Scan(&tool, &raw) != nil {
				return ErrDenied
			}
			var wire struct {
				Tool         string          `json:"tool"`
				Data         json.RawMessage `json:"data"`
				EvidenceRefs []string        `json:"evidenceRefs"`
			}
			if json.Unmarshal(raw, &wire) != nil || wire.Tool != tool {
				return ErrInvalid
			}
			switch tool {
			case "resource_history", "resource_changes", "resource_findings":
				var data struct {
					CanonicalID string `json:"canonicalId"`
				}
				if json.Unmarshal(wire.Data, &data) != nil {
					return ErrInvalid
				}
				ref := ResourceSourceStep{StepID: id, Tool: tool, CanonicalID: data.CanonicalID, ContentDigest: graph.ScopeDigest(json.RawMessage(raw))}
				if _, err := ReadResourceSourceStepTx(ctx, tx, j, ref); err != nil {
					return err
				}
				sourceSteps = append(sourceSteps, ref)
				for _, ref := range wire.EvidenceRefs {
					id, err := uuid.Parse(ref)
					if err != nil {
						return ErrInvalid
					}
					e, err := evidence.GetTx(ctx, tx, j.TenantID, id, j.Scope)
					if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, e.DataClass) {
						return ErrDenied
					}
					if e.Type == "change" || e.Type == "change_event" {
						changes = append(changes, e)
					}
				}
				if tool == "resource_findings" {
					var findings struct {
						Findings []struct {
							State      string    `json:"state"`
							ObservedAt time.Time `json:"observedAt"`
						}
					}
					if json.Unmarshal(wire.Data, &findings) != nil {
						return ErrInvalid
					}
					for _, f := range findings.Findings {
						if f.ObservedAt.Before(request.Fault.From) || !f.ObservedAt.Before(request.Fault.To) {
							continue
						}
						if f.State == "firing" {
							assessments = append(assessments, ResourceAssessment{CanonicalID: data.CanonicalID, RuleID: "Finding/firing-occurrence", Reason: "故障窗存在已提交的标准 Finding occurrence；严重度与来源保留原记录，不自动确认 RCA。", Supports: []uuid.UUID{id}, Conflicts: []uuid.UUID{}, FactRefs: []FactRef{}, MissingEvidence: []string{"causal_mechanism_unverified"}, SourceQuality: "committed_occurrence_revision", NextChecks: []string{"resource_history", "execute_analysis_query"}})
							break
						}
					}
				}
				if tool == "resource_history" {
					var history ResourceHistory
					if json.Unmarshal(wire.Data, &history) != nil {
						return ErrInvalid
					}
					assessments = append(assessments, AssessResourceHistory(request, id, history)...)
					relations = append(relations, history.Relations...)
					health = append(health, CommittedResourceHistoryHealth(id, history)...)
				}
			case "get_resource_health":
				var snapshot graph.Result
				if json.Unmarshal(wire.Data, &snapshot) != nil {
					return ErrInvalid
				}
				// Older workers have no qualified observation source metadata.
				// Keep the health gap instead of assigning a current revision to
				// a potentially older snapshot.
				if len(snapshot.ObservationSources) == 0 {
					continue
				}
				if rca.CheckGraphSources(ctx, tx, j.TenantID, snapshot.ObservationSources, snapshot) != nil {
					return ErrDenied
				}
				for _, node := range snapshot.Nodes {
					if !j.Scope.Allows(node.CanonicalID, node.Namespace) {
						return ErrDenied
					}
				}
				health = append(health, CommittedFaultChainResourceHealth(id, snapshot)...)
			case "get_recent_changes":
				for _, ref := range wire.EvidenceRefs {
					id, err := uuid.Parse(ref)
					if err != nil {
						return ErrInvalid
					}
					e, err := evidence.GetTx(ctx, tx, j.TenantID, id, j.Scope)
					if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, e.DataClass) {
						return ErrDenied
					}
					changes = append(changes, e)
					if len(changes) > 80 {
						return ErrInvalid
					}
				}
			case "query_deepflow", "get_dependencies", "get_impact_scope", "get_resource_context":
				if tool == "query_deepflow" {
					if err := authorizeResult(ctx, tx, j, raw); err != nil {
						return err
					}
				}
				if err := authorizeResourceToolTx(ctx, tx, j, tool, wire.Data); err != nil {
					return err
				}
				if j.Assistant.CatalogVersion == "assistant-tools/v2" {
					sourceSteps = append(sourceSteps, ResourceSourceStep{StepID: id, Tool: tool, CanonicalID: request.TargetCanonicalID, ContentDigest: graph.ScopeDigest(json.RawMessage(raw))})
				}
				var graph struct {
					Edges []resource.Relation `json:"edges"`
				}
				if json.Unmarshal(wire.Data, &graph) != nil {
					return ErrInvalid
				}
				relations = append(relations, graph.Edges...)
			case "execute_analysis_query":
				var data struct {
					QueryResultRefs []uuid.UUID        `json:"queryResultRefs"`
					CanonicalID     string             `json:"canonicalId"`
					Filters         []analytics.Filter `json:"filters"`
				}
				if json.Unmarshal(wire.Data, &data) != nil {
					return ErrInvalid
				}
				if data.CanonicalID != "" {
					sourceSteps = append(sourceSteps, ResourceSourceStep{StepID: id, Tool: tool, CanonicalID: data.CanonicalID, ContentDigest: graph.ScopeDigest(json.RawMessage(raw))})
				}
				for _, id := range data.QueryResultRefs {
					q, err := ReadCommittedQueryResultTx(ctx, tx, j, id)
					if err != nil {
						return err
					}
					structuredErrors := slices.Contains(data.Filters, analytics.Filter{Field: "level", Operator: "eq", Value: "error"})
					assessments = append(assessments, AssessResourceQuery(request, data.CanonicalID, id, q, structuredErrors)...)
					queries = append(queries, q)
				}
			}
		}
		var err error
		healthTruncated := len(health) > 40
		if healthTruncated {
			health = health[:40]
		}
		out, err = EvaluateFaultChainWithResourceHealth(request, relations, queries, time.Now().UTC(), health, changes)
		if len(assessments) > 64 {
			assessments = assessments[:64]
			out.MissingEvidence = append(out.MissingEvidence, "resource_assessment_limit_reached")
		}
		if healthTruncated {
			out.MissingEvidence = append(out.MissingEvidence, "remaining_resource_health_not_evaluated")
		}
		out.SourceSteps = sourceSteps
		out.ResourceAssessments = assessments
		for _, q := range queries {
			if q.Result.NetworkContext == nil && request.Allows(q.Result.TimeRange) {
				if q.Result.Status != "complete" || !q.Result.Exact {
					out.MissingEvidence = append(out.MissingEvidence, q.Result.DatasetID+"_"+q.Result.Status)
				}
				out.QueryResultRefs = append(out.QueryResultRefs, q.QueryResultID)
				out.EvidenceRefs = append(out.EvidenceRefs, q.Result.DerivationEvidenceRefs...)
			}
		}
		slices.Sort(out.EvidenceRefs)
		out.EvidenceRefs = slices.Compact(out.EvidenceRefs)
		return err
	}()
	return out, err
}
