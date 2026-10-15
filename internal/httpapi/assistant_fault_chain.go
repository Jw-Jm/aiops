package httpapi

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"slices"
	"time"
)

// faultChainEvaluationRequest uses only the API-frozen turn context. Models
// select committed Steps, never the target, comparison windows or edge limit.
func faultChainEvaluationRequest(input assistant.TurnInput, j investigation.Job) (investigation.FaultChainRequest, error) {
	if j.Assistant == nil || j.Assistant.AuthorizationDomain != "operational" {
		return investigation.FaultChainRequest{}, investigation.ErrDenied
	}
	if input.FaultChainRequest != nil {
		return *input.FaultChainRequest, nil
	}
	if input.RequestedAt.IsZero() {
		return investigation.FaultChainRequest{}, investigation.ErrInvalid
	}
	target, namespace := "", ""
	window := input.RequestedTimeRange
	switch j.Assistant.TaskKind {
	case "resource_troubleshooting":
		rc := input.Binding.ResourceContext
		if rc == nil || len(rc.Resources) != 1 {
			return investigation.FaultChainRequest{}, investigation.ErrInvalid
		}
		target, namespace = rc.Resources[0].CanonicalID, rc.Resources[0].Namespace
		if window == nil {
			window = &analytics.QueryTimeRange{From: rc.TimeRange.From, To: rc.TimeRange.To, Timezone: rc.TimeRange.Timezone}
		}
	case "incident_followup":
		ref := input.ReferenceResolution
		if j.IncidentID == uuid.Nil || input.Binding.IncidentID != j.IncidentID || ref == nil || ref.Status != "resolved" || ref.CanonicalID == "" || len(j.Scope.Namespaces) != 1 {
			return investigation.FaultChainRequest{}, investigation.ErrInvalid
		}
		target, namespace = ref.CanonicalID, j.Scope.Namespaces[0]

	default:
		return investigation.FaultChainRequest{}, investigation.ErrInvalid
	}
	if window == nil {
		return investigation.FaultChainRequest{}, investigation.ErrInvalid
	}
	request := investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: target, Fault: *window, MaxEdges: 4}
	if !j.Scope.Allows(target, namespace) || !request.Valid(j.Scope, input.Binding.Timezone, input.RequestedAt) {
		return investigation.FaultChainRequest{}, investigation.ErrDenied
	}
	return request, nil
}

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
		changes := []evidence.Evidence{}
		health := []investigation.FaultChainResourceHealth{}
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
				Tool         string          `json:"tool"`
				Data         json.RawMessage `json:"data"`
				EvidenceRefs []string        `json:"evidenceRefs"`
			}
			if json.Unmarshal(raw, &wire) != nil || wire.Tool != tool {
				return investigation.ErrInvalid
			}
			switch tool {
			case "get_resource_health":
				var snapshot graph.Result
				if json.Unmarshal(wire.Data, &snapshot) != nil {
					return investigation.ErrInvalid
				}
				// Older workers have no qualified observation source metadata.
				// Keep the health gap instead of assigning a current revision to
				// a potentially older snapshot.
				if len(snapshot.ObservationSources) == 0 {
					continue
				}
				if rca.CheckGraphSources(ctx, tx, j.TenantID, snapshot.ObservationSources, snapshot) != nil {
					return investigation.ErrDenied
				}
				for _, node := range snapshot.Nodes {
					if !j.Scope.Allows(node.CanonicalID, node.Namespace) {
						return investigation.ErrDenied
					}
				}
				health = append(health, investigation.CommittedFaultChainResourceHealth(id, snapshot)...)
			case "get_recent_changes":
				for _, ref := range wire.EvidenceRefs {
					id, err := uuid.Parse(ref)
					if err != nil {
						return investigation.ErrInvalid
					}
					e, err := evidence.GetTx(ctx, tx, j.TenantID, id, j.Scope)
					if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, e.DataClass) {
						return investigation.ErrDenied
					}
					changes = append(changes, e)
					if len(changes) > 80 {
						return investigation.ErrInvalid
					}
				}
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
		out, err = investigation.EvaluateFaultChainWithResourceHealth(request, relations, queries, time.Now().UTC(), health, changes)
		return err
	})
	return out, err
}
