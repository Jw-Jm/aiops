package action

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"ops-platform/internal/rca"
	"time"
)

type PostCheckFacts struct {
	RecipeVersion   uuid.UUID `json:"recipeVersion"`
	RecipeName      string    `json:"recipeName"`
	CollectedAfter  time.Time `json:"collectedAfter"`
	CheckedAt       time.Time `json:"checkedAt"`
	Authorized      bool      `json:"authorized"`
	SourceAvailable bool      `json:"sourceAvailable"`
	Complete        bool      `json:"complete"`
	FaultPresent    bool      `json:"faultPresent"`
	EvidenceRefs    []string  `json:"evidenceRefs"`
}

// Facts must be supplied by a trusted current Recipe/Investigation adapter,
// never deserialized from an operator or model-supplied verdict.
func JudgePostCheck(f PostCheckFacts, completedAt time.Time) string {
	if !f.Authorized || !f.SourceAvailable || !f.Complete || len(f.EvidenceRefs) == 0 || !f.CollectedAfter.After(completedAt) || f.CheckedAt.Before(f.CollectedAfter) {
		return "inconclusive"
	}
	if f.FaultPresent {
		return "not_resolved"
	}
	return "resolved"
}
func workerActor(tenant uuid.UUID) auth.RequestContext {
	return auth.RequestContext{TenantID: tenant, Subject: "platform-worker"}
}
func (s Service) RecordPostCheck(ctx context.Context, tenant, id uuid.UUID, f PostCheckFacts) error {
	return persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		e, err := executionTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if e.State != "succeeded" && e.State != "failed" {
			return ErrConflict
		}
		var completed time.Time
		if err = tx.QueryRow(ctx, `SELECT completed_at FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, tenant, id).Scan(&completed); err != nil {
			return err
		}

		scope, scopeErr := graph.EffectiveTx(ctx, tx, tenant.String(), e.Binding.Subject, e.Binding.ClusterUID, true)
		ns := ""
		if e.Binding.Namespace != nil {
			ns = *e.Binding.Namespace
		}
		if scopeErr != nil || !scope.Allows(e.Binding.Target, ns) {
			f.Authorized = false
		}
		if f.Complete {
			for _, ref := range f.EvidenceRefs {
				refID, err := uuid.Parse(ref)
				if err != nil {
					f.Complete = false
					break
				}
				ev, err := evidence.GetTx(ctx, tx, tenant, refID, scope)
				if err != nil || !ev.TimeReliable || ev.ResourceCanonicalID != e.Binding.Target || !ev.ObservedFrom.After(completed) || !ev.ObservedTo.After(completed) {
					f.Complete = false
					break
				}
			}
			var raw []byte
			if tx.QueryRow(ctx, `SELECT content FROM platform.registry_versions WHERE tenant_id=$1 AND version_id=$2 AND logical_name=$3`, tenant, f.RecipeVersion, f.RecipeName).Scan(&raw) != nil {
				f.Complete = false
			} else {
				recipe, err := rca.DecodeRecipe(raw)
				if err != nil || rca.VerifyRecipeForRead(ctx, tx, tenant, &f.RecipeVersion, e.Binding.ClusterUID, ns, recipe, s.Trust) != nil {
					f.Complete = false
				}
			}
		}
		verdict := JudgePostCheck(f, completed)
		if _, err = tx.Exec(ctx, `UPDATE action.executions SET post_check=$3,post_checked_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2`, tenant, id, verdict); err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, workerActor(tenant), id, "command.post_checked", map[string]any{"verdict": verdict, "evidenceRefs": f.EvidenceRefs, "checkedAt": f.CheckedAt}); err != nil {
			return err
		}
		return event(ctx, tx, tenant, id, "post_check", map[string]any{"verdict": verdict, "evidenceRefs": f.EvidenceRefs})
	})
}
