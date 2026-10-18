package action

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"time"
)

func validExecutionRelation(id *uuid.UUID, relation string) bool {
	if id == nil {
		return relation == ""
	}
	return *id != uuid.Nil && (relation == "follow_up" || relation == "rollback")
}

// Links describe a new manual execution. They confer no execution authority and
// cannot silently follow a recreated resource or another Incident.
func validateRelatedExecution(binding Binding, parent Execution) error {
	if !validExecutionRelation(binding.RelatedExecutionID, binding.ExecutionRelation) || binding.RelatedExecutionID == nil {
		return ErrInvalid
	}
	namespace := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	if *binding.RelatedExecutionID != parent.ID || binding.TenantID != parent.Binding.TenantID || binding.IncidentID != parent.Binding.IncidentID || binding.Target != parent.Binding.Target || binding.TargetUID == "" || binding.TargetUID != parent.Binding.TargetUID || binding.ClusterUID != parent.Binding.ClusterUID || namespace(binding.Namespace) != namespace(parent.Binding.Namespace) {
		return ErrDenied
	}
	if parent.CompletedAt == nil || parent.CompletedAt.IsZero() || (parent.State != "succeeded" && parent.State != "failed") {
		return ErrConflict
	}
	return nil
}

func relatedExecutionTx(ctx context.Context, tx pgx.Tx, binding Binding) error {
	if binding.RelatedExecutionID == nil {
		if binding.ExecutionRelation != "" {
			return ErrInvalid
		}
		return nil
	}
	parent := Execution{ID: *binding.RelatedExecutionID}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT binding,state,completed_at FROM action.executions WHERE tenant_id=$1 AND execution_id=$2 FOR SHARE`, binding.TenantID, parent.ID).Scan(&raw, &parent.State, &parent.CompletedAt); err != nil {
		return ErrDenied
	}
	if json.Unmarshal(raw, &parent.Binding) != nil {
		return ErrDenied
	}
	return validateRelatedExecution(binding, parent)
}

type ExecutionLink struct {
	ExecutionID uuid.UUID `json:"executionId"`
	Relation    string    `json:"relation"`
	Direction   string    `json:"direction"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"createdAt"`
}

func executionLinksTx(ctx context.Context, tx pgx.Tx, a auth.RequestContext, current Execution) ([]ExecutionLink, bool, error) {
	parentID := uuid.Nil
	if current.Binding.RelatedExecutionID != nil {
		parentID = *current.Binding.RelatedExecutionID
	}
	rows, err := tx.Query(ctx, `SELECT execution_id,binding,state,completed_at,created_at FROM action.executions WHERE tenant_id=$1 AND (binding->>'relatedExecutionId'=$2 OR execution_id=$3) ORDER BY (execution_id=$3) DESC,created_at DESC,execution_id LIMIT 22`, a.TenantID, current.ID.String(), parentID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	links := []ExecutionLink{}
	children := 0
	truncated := false
	parentFound := parentID == uuid.Nil
	for rows.Next() {
		var e Execution
		var raw []byte
		if err := rows.Scan(&e.ID, &raw, &e.State, &e.CompletedAt, &e.CreatedAt); err != nil {
			return nil, false, err
		}
		if json.Unmarshal(raw, &e.Binding) != nil {
			return nil, false, ErrInvalid
		}
		link := ExecutionLink{ExecutionID: e.ID, State: e.State, CreatedAt: e.CreatedAt, Direction: "child", Relation: e.Binding.ExecutionRelation}
		if e.ID == parentID {
			parentFound = true
			if err := validateRelatedExecution(current.Binding, e); err != nil {
				return nil, false, err
			}
			link.Direction, link.Relation = "parent", current.Binding.ExecutionRelation
		} else {
			if err := validateRelatedExecution(e.Binding, current); err != nil {
				return nil, false, err
			}
			children++
			if children > 20 {
				truncated = true
				continue
			}
		}
		links = append(links, link)
	}
	if !parentFound && rows.Err() == nil {
		return nil, false, ErrDenied
	}
	return links, truncated, rows.Err()
}
