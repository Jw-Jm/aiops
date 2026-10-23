package assistant

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
)

func validateFaultChainCheck(request investigation.FaultChainRequest, parent investigation.FaultChainContext) bool {
	c := request.ReadOnlyCheck
	if c == nil || c.Tool == "" || investigation.FaultChainGapTool(c.GapCode) != c.Tool || parent.Request.ReadOnlyCheck != nil {
		return false
	}
	frozen := request
	frozen.ReadOnlyCheck = nil
	if graph.ScopeDigest(frozen) != graph.ScopeDigest(parent.Request) {
		return false
	}
	if c.GapCode == "committed_scoped_resource_relations" {
		return c.RelationDigest == "" && c.CanonicalID == parent.Request.TargetCanonicalID && c.Direction == "" && slices.Contains(parent.MissingEvidence, c.GapCode)
	}
	for _, edge := range parent.Edges {
		if edge.RelationDigest != c.RelationDigest || !slices.Contains(edge.MissingEvidence, c.GapCode) {
			continue
		}
		if c.Tool == "execute_analysis_query" {
			if edge.Relation.Kind != "communicates_with" {
				return false
			}
			peer := edge.Relation.To
			if peer == parent.Request.TargetCanonicalID {
				peer = edge.Relation.From
			}
			direction := edge.CallDirection
			if direction == "unknown" {
				direction = "either"
			}
			return c.CanonicalID == peer && c.CanonicalID != parent.Request.TargetCanonicalID && c.Direction == direction
		}
		if c.Direction != "" {
			return false
		}
		if c.CanonicalID == parent.Request.TargetCanonicalID {
			return true
		}
		if c.Tool == "get_resource_health" && (c.CanonicalID == edge.Relation.From || c.CanonicalID == edge.Relation.To) {
			return true
		}
		if c.Tool == "get_resource_health" {
			for _, observation := range edge.Observations {
				if observation.ResourceHealth != nil && observation.ResourceHealth.CanonicalID == c.CanonicalID {
					return true
				}
				for _, path := range observation.RelatedRelations {
					if path.From == c.CanonicalID || path.To == c.CanonicalID {
						return true
					}
				}
			}
		}
	}
	return false
}

func validateFaultChainCheckTx(ctx context.Context, tx pgx.Tx, c Conversation, q SubmitMessage) error {
	check := q.FaultChainRequest.ReadOnlyCheck
	if q.ParentTurnID == nil || *q.ParentTurnID != check.OriginTurnID {
		return investigation.ErrInvalid
	}
	parent, err := loadTurn(ctx, tx, c.TenantID, check.OriginTurnID)
	if err != nil || parent.ConversationID != c.ConversationID {
		return investigation.ErrDenied
	}
	if err := authorizeTurnAnswer(ctx, tx, c, parent.TurnID, parent.Answer, ownerRead(c, c.OwnerSubject)); err != nil {
		return err
	}
	job, err := investigation.AssistantReadJobTx(ctx, tx, c.TenantID, c.OwnerSubject, c.ConversationID, parent.TurnID, c.Binding.ExecutionScope)
	if err != nil {
		return err
	}
	var answer investigation.AssistantAnswer
	if json.Unmarshal(parent.Answer, &answer) != nil || answer.FaultChainContextRef == nil {
		return investigation.ErrInvalid
	}
	original, err := investigation.ReadFaultChainContextTx(ctx, tx, job, *answer.FaultChainContextRef)
	if err != nil {
		return err
	}
	if !validateFaultChainCheck(*q.FaultChainRequest, original) {
		return investigation.ErrDenied
	}
	var ns string
	if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, c.TenantID, check.CanonicalID).Scan(&ns) != nil || !c.Binding.ExecutionScope.Allows(check.CanonicalID, ns) {
		return investigation.ErrDenied
	}
	return nil
}
