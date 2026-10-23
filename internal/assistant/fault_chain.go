package assistant

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
)

func (r Repository) FaultChainContext(ctx context.Context, tenant, turn uuid.UUID, subject string) (investigation.FaultChainContext, error) {
	var out investigation.FaultChainContext
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		rs, err := newReadSubjects(ctx, tx, tenant, subject)
		if err != nil {
			return err
		}
		c, t, read, err := loadReadableTurn(ctx, tx, tenant, turn, &rs)
		if err != nil {
			return err
		}
		if err = authorizeTurnAnswer(ctx, tx, c, turn, t.Answer, read); err != nil {
			return err
		}
		var answer investigation.AssistantAnswer
		if json.Unmarshal(t.Answer, &answer) != nil || answer.FaultChainContextRef == nil {
			return investigation.ErrInvalid
		}
		readCtx, j, err := investigation.AuthorizeHistoricalReadTx(ctx, tx, tenant, read, c.ConversationID, turn, c.Binding.ExecutionScope)
		if err != nil {
			return err
		}
		out, err = investigation.ReadFaultChainContextTx(readCtx, tx, j, *answer.FaultChainContextRef)
		return err
	})
	return out, err
}

// ResourceSourceStep reads a committed parent only through its evaluator closure.
func (r Repository) ResourceSourceStep(ctx context.Context, tenant, turn, step uuid.UUID, subject string) (json.RawMessage, error) {
	var out json.RawMessage
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		rs, err := newReadSubjects(ctx, tx, tenant, subject)
		if err != nil {
			return err
		}
		c, t, read, err := loadReadableTurn(ctx, tx, tenant, turn, &rs)
		if err != nil {
			return err
		}
		readCtx, j, err := investigation.AuthorizeHistoricalReadTx(ctx, tx, tenant, read, c.ConversationID, turn, c.Binding.ExecutionScope)
		if err != nil {
			return err
		}
		var answer investigation.AssistantAnswer
		if json.Unmarshal(t.Answer, &answer) != nil {
			return investigation.ErrDenied
		}
		for _, ref := range answer.ResourceSourceSteps {
			if ref.StepID == step {
				out, err = investigation.ReadResourceSourceStepForClientTx(readCtx, tx, j, ref)
				return err
			}
		}
		if answer.FaultChainContextRef == nil {
			return investigation.ErrDenied
		}
		chain, err := investigation.ReadFaultChainContextTx(readCtx, tx, j, *answer.FaultChainContextRef)
		if err != nil {
			return err
		}
		for _, ref := range chain.SourceSteps {
			if ref.StepID == step {
				out, err = investigation.ReadResourceSourceStepForClientTx(readCtx, tx, j, ref)
				return err
			}
		}
		return investigation.ErrDenied
	})
	return out, err
}
