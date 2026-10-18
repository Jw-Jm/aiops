package httpapi

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
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
	if input.FrozenCorrelationRequest != nil {
		if !input.FrozenCorrelationRequest.Valid(j.Scope, input.Binding.Timezone, input.RequestedAt) {
			return investigation.FaultChainRequest{}, investigation.ErrDenied
		}
		return *input.FrozenCorrelationRequest, nil
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
	request := investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v2", TargetCanonicalID: target, Fault: *window, MaxEdges: 4}
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
		var err error
		out, err = investigation.EvaluateCommittedFaultChainTx(ctx, tx, j, request, p.StepIDs)
		return err
	})
	return out, err
}

// A relation read is explicit in the ledger and bound to the original Pod/window.
// Keep resource restrictions; never inherit the worker's broader namespace grant.
func faultChainRelationScope(input assistant.TurnInput, j investigation.Job, target string, window *analytics.QueryTimeRange, namespace string) (graph.Scope, error) {
	request, err := faultChainEvaluationRequest(input, j)
	id, parseErr := resource.ParseCanonicalID(target)
	if err != nil || parseErr != nil || id.Domain != "k8s" || id.Kind != "Pod" || target != request.TargetCanonicalID || window == nil || !window.From.Equal(request.Fault.From) || !window.To.Equal(request.Fault.To) || namespace == "" || !j.Scope.Allows(target, namespace) {
		return graph.Scope{}, investigation.ErrDenied
	}
	scope := analytics.CloneScope(j.Scope)
	scope.Namespaces = []string{namespace}
	scope.ClusterScoped = false
	return scope, nil
}
