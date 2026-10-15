package investigation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"time"
)

// Only committed, currently disclosable results survive cancellation or budget exhaustion.
// Unsettled requests and model guesses cannot become partial numeric facts.
func assistantPartialAnswerTx(ctx context.Context, tx pgx.Tx, j Job, code string) ([]byte, error) {
	a := AssistantAnswer{SchemaVersion: "assistant-answer/v1", AnswerKind: "unsupported", Summary: "本轮已停止，保留已提交的查询事实；未完成的检查没有结论。", FactRefs: []FactRef{}, QueryResultRefs: []uuid.UUID{}, EvidenceRefs: []string{}, EntityRefs: []string{}, MissingEvidence: []string{"remaining_checks_not_completed"}, Partial: true, DegradedSources: []string{}, Warnings: []string{code}, Hypotheses: []Hypothesis{}, NextChecks: []string{"缩小范围后发起新一轮检查"}}
	a.ScopeSummary = graph.ScopeDigest(j.Scope)
	a.AsOf = time.Now().UTC()
	if j.Assistant.AuthorizationDomain == "operational" || j.Assistant.TaskKind == "data_query" {
		rows, err := tx.Query(ctx, `SELECT result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND state='succeeded' AND tool_name='execute_analysis_query' ORDER BY started_at,step_id`, j.TenantID, j.JobID)
		if err != nil {
			return nil, err
		}
		ids := []uuid.UUID{}
		seen := map[uuid.UUID]bool{}
		for rows.Next() {
			var result []byte
			var wire struct {
				Data struct {
					QueryResultRefs []uuid.UUID `json:"queryResultRefs"`
				} `json:"data"`
			}
			if rows.Scan(&result) != nil || json.Unmarshal(result, &wire) != nil {
				rows.Close()
				return nil, ErrInvalid
			}
			for _, id := range wire.Data.QueryResultRefs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			query, err := loadQueryResultTx(ctx, tx, j, id)
			if err != nil {
				return nil, err
			}
			if len(query.Facts) == 0 {
				continue
			}
			a.QueryResultRefs = append(a.QueryResultRefs, id)
			a.EvidenceRefs = append(a.EvidenceRefs, query.Result.DerivationEvidenceRefs...)
			for _, fact := range query.Facts {
				a.FactRefs = append(a.FactRefs, FactRef{id, fact.FactID})
				if len(a.FactRefs) >= 50 {
					break
				}
			}
			if len(a.FactRefs) >= 50 {
				break
			}
		}
		if len(a.FactRefs) > 0 {
			if j.Assistant.TaskKind == "data_query" {
				a.AnswerKind = "data"
			} else {
				a.AnswerKind = "diagnosis"
			}
		}
	}
	rows, err := tx.Query(ctx, `SELECT step_id,tool_name,result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND state='succeeded' AND tool_name IN('resource_history','resource_changes','resource_findings') ORDER BY started_at,step_id LIMIT 40`, j.TenantID, j.JobID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ref ResourceSourceStep
		var data []byte
		var wire struct {
			Data struct {
				CanonicalID string `json:"canonicalId"`
			} `json:"data"`
		}
		if rows.Scan(&ref.StepID, &ref.Tool, &data) != nil || json.Unmarshal(data, &wire) != nil {
			rows.Close()
			return nil, ErrInvalid
		}
		ref.CanonicalID = wire.Data.CanonicalID
		ref.ContentDigest = graph.ScopeDigest(json.RawMessage(data))
		a.ResourceSourceSteps = append(a.ResourceSourceSteps, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(a.ResourceSourceSteps) > 0 {
		a.AnswerKind = "diagnosis"
		a.Summary = "本轮已停止，保留已提交的运行数据来源与查询事实；未完成的检查没有结论。"
	}
	// The validator derives scope/asOf and checks the same committed-reference
	// manifest as a normal final answer. No model call or extra read path exists.
	return ValidateAssistantAnswerTx(ctx, tx, j, raw(a))
}
