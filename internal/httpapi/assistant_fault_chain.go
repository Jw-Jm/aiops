package httpapi

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"time"
)

func (a AssistantTools) evaluateFaultChain(ctx context.Context, j investigation.Job, request investigation.FaultChainRequest, args json.RawMessage) (investigation.FaultChainContext, error) {
	var out investigation.FaultChainContext
	var p struct {
		StepIDs []uuid.UUID `json:"stepIds"`
	}
	if json.Unmarshal(args, &p) != nil || len(p.StepIDs) < 1 || len(p.StepIDs) > 40 {
		return out, investigation.ErrInvalid
	}
	err := persistence.WithTenantTx(ctx, a.Legacy.SP04.Pool, j.TenantID, func(tx pgx.Tx) error {
		if investigation.CheckAssistantJobTx(ctx, tx, j) != nil {
			return investigation.ErrDenied
		}
		relations := []resource.Relation{}
		queries := []investigation.StoredQueryResult{}
		seen := map[uuid.UUID]bool{}
		for _, id := range p.StepIDs {
			if id == uuid.Nil || seen[id] {
				return investigation.ErrInvalid
			}
			seen[id] = true
			var tool string
			var raw []byte
			if tx.QueryRow(ctx, `SELECT tool_name,result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND state='succeeded'`, j.TenantID, j.JobID, id).Scan(&tool, &raw) != nil {
				return investigation.ErrDenied
			}
			var wire struct {
				Tool string          `json:"tool"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(raw, &wire) != nil || wire.Tool != tool {
				return investigation.ErrInvalid
			}
			switch tool {
			case "get_dependencies", "get_impact_scope", "get_resource_context":
				var graph struct {
					Edges []resource.Relation `json:"edges"`
				}
				if json.Unmarshal(wire.Data, &graph) != nil {
					return investigation.ErrInvalid
				}
				relations = append(relations, graph.Edges...)
			case "execute_analysis_query":
				var data struct {
					QueryResultRefs []uuid.UUID `json:"queryResultRefs"`
				}
				if json.Unmarshal(wire.Data, &data) != nil {
					return investigation.ErrInvalid
				}
				for _, id := range data.QueryResultRefs {
					q, err := investigation.ReadCommittedQueryResultTx(ctx, tx, j, id)
					if err != nil {
						return err
					}
					queries = append(queries, q)
				}
			}
		}
		var err error
		out, err = investigation.EvaluateFaultChain(request, relations, queries, time.Now().UTC())
		return err
	})
	return out, err
}
