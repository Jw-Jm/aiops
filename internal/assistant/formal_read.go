package assistant

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"ops-platform/internal/rca"
)

func (r Repository) FormalReference(ctx context.Context, tenant, turn uuid.UUID, subject string, index int) (map[string]any, error) {
	out := map[string]any{}
	if index < 0 || index > 10 {
		return out, investigation.ErrInvalid
	}
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		t, err := loadTurn(ctx, tx, tenant, turn)
		if err != nil {
			return err
		}
		c, err := loadConversation(ctx, tx, tenant, t.ConversationID, subject, false)
		if err != nil {
			return err
		}
		if _, err = authorizeConversation(ctx, tx, c); err != nil {
			return err
		}
		if err = authorizeTurnAnswer(ctx, tx, c, turn, t.Answer); err != nil {
			return err
		}
		var answer investigation.AssistantAnswer
		if json.Unmarshal(t.Answer, &answer) != nil || index >= len(answer.FormalRefs) {
			return investigation.ErrInvalid
		}
		ref := answer.FormalRefs[index]
		j, err := investigation.AssistantReadJobTx(ctx, tx, tenant, subject, c.ConversationID, turn, c.Binding.ExecutionScope)
		if err != nil {
			return err
		}
		if err = investigation.AuthorizeFormalReferenceTx(ctx, tx, j, ref); err != nil {
			return err
		}
		out["reference"] = ref
		if ref.Kind == "rca" {
			var data []byte
			if err = tx.QueryRow(ctx, `SELECT result FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND revision=$3`, tenant, ref.IncidentID, ref.Revision).Scan(&data); err != nil {
				return err
			}
			var rev rca.Revision
			if json.Unmarshal(data, &rev) != nil {
				return investigation.ErrInvalid
			}
			refs := []string{}
			for _, e := range rev.InputManifest.Evidence {
				refs = append(refs, e.EvidenceID)
			}
			out["metadata"] = map[string]any{"revision": rev.Revision, "createdAt": rev.CreatedAt, "source": rev.Source, "superseded": rev.Superseded, "status": rev.Result.Status, "candidates": rev.Result.Candidates, "missingEvidence": rev.Result.Bundle.Missing, "evidenceRefs": refs}
		} else {
			var data []byte
			var state string
			if err = tx.QueryRow(ctx, `SELECT content,state FROM action.plans WHERE tenant_id=$1 AND action_plan_id=$2 AND revision=$3`, tenant, *ref.ActionPlanID, ref.Revision).Scan(&data, &state); err != nil {
				return err
			}
			var plan map[string]any
			if json.Unmarshal(data, &plan) != nil {
				return investigation.ErrInvalid
			}
			safe := map[string]any{"state": state, "revision": ref.Revision}
			for _, key := range []string{"targetCanonicalId", "rcaRevision", "iteration", "reason", "expectedImpact", "preconditions", "riskSummary", "postCheckInstructions", "rollbackSuggestion"} {
				if value, ok := plan[key]; ok {
					safe[key] = value
				}
			}
			out["metadata"] = safe
		}
		return nil
	})
	return out, err
}
