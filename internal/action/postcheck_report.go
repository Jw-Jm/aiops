package action

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/rca"
	"time"
)

type PostCheckReport struct {
	SchemaVersion string         `json:"schemaVersion"`
	ExecutionID   uuid.UUID      `json:"executionId"`
	IncidentID    uuid.UUID      `json:"incidentId"`
	Target        string         `json:"targetCanonicalId"`
	TargetUID     string         `json:"targetUid"`
	CompletedAt   time.Time      `json:"completedAt"`
	Verdict       string         `json:"verdict"`
	Reason        string         `json:"reason"`
	Coverage      string         `json:"coverage"`
	Facts         PostCheckFacts `json:"facts"`
}

func postCheckDecision(f PostCheckFacts, completed time.Time) (string, string) {
	reason := ""
	switch {
	case completed.IsZero():
		reason = "completion_time_missing"
	case !f.Authorized:
		reason = "authorization_unavailable"
	case !f.SourceAvailable:
		reason = "source_unavailable"
	case !f.Complete:
		reason = "facts_incomplete"
	case len(f.EvidenceRefs) == 0:
		reason = "evidence_missing"
	case !f.CollectedAfter.After(completed):
		reason = "observation_not_after_execution"
	case f.CheckedAt.Before(f.CollectedAfter):
		reason = "check_precedes_observation"
	}
	if reason != "" {
		return "inconclusive", reason
	}
	if f.FaultPresent {
		return "not_resolved", "recipe_fault_present"
	}
	return "resolved", "recipe_fault_absent"
}

func makePostCheckReport(e Execution, f PostCheckFacts, completed time.Time) PostCheckReport {
	verdict, reason := postCheckDecision(f, completed)
	if PostCheckApplicability(e.Binding.Target).Status == "unsupported" {
		verdict, reason = "inconclusive", "target_unsupported"
	}
	f.EvidenceRefs = append([]string{}, f.EvidenceRefs...)
	return PostCheckReport{SchemaVersion: "command-post-check/v1", ExecutionID: e.ID, IncidentID: e.Binding.IncidentID, Target: e.Binding.Target, TargetUID: e.Binding.TargetUID, CompletedAt: completed, Verdict: verdict, Reason: reason, Coverage: "recipe_target_only", Facts: f}
}

func validatePostCheckReport(report PostCheckReport, e Execution) error {
	if e.CompletedAt == nil || report.ExecutionID != e.ID || report.IncidentID != e.Binding.IncidentID || report.Target != e.Binding.Target || report.TargetUID != e.Binding.TargetUID || !report.CompletedAt.Equal(*e.CompletedAt) {
		return ErrInvalid
	}
	verdict, reason := postCheckDecision(report.Facts, *e.CompletedAt)
	// Adapter coverage is frozen at recording time. Adding an adapter later
	// must not reinterpret an old unsupported-target report.
	if report.Reason == "target_unsupported" {
		verdict, reason = "inconclusive", "target_unsupported"
	}
	if report.Verdict != verdict || report.Reason != reason || report.Coverage != "recipe_target_only" {
		return ErrInvalid
	}
	return nil
}

// Every disclosure path uses current evidence/source and Recipe authorization;
// a frozen report is not authority to reveal subsequently revoked inputs.
func (s Service) postCheckEventReadable(ctx context.Context, tx pgx.Tx, a auth.RequestContext, execution Execution, payload []byte) error {
	binding := execution.Binding
	var stored struct {
		EvidenceRefs []string         `json:"evidenceRefs"`
		Report       *PostCheckReport `json:"report"`
		ReportDigest string           `json:"reportDigest"`
	}
	if json.Unmarshal(payload, &stored) != nil || len(stored.EvidenceRefs) > 50 {
		return ErrInvalid
	}
	scope, err := graph.EffectiveTx(ctx, tx, a.TenantID.String(), a.Subject, binding.ClusterUID, true)
	if err != nil {
		return ErrDenied
	}
	for _, ref := range stored.EvidenceRefs {
		id, err := uuid.Parse(ref)
		if err != nil {
			return ErrDenied
		}
		ev, err := evidence.GetTx(ctx, tx, a.TenantID, id, scope)
		if err != nil || ev.ResourceCanonicalID != binding.Target {
			return ErrDenied
		}
	}
	if r := stored.Report; r != nil {
		if contract.Validate("https://ops.local/schemas/command-post-check/v1", Canonical(r)) != nil || Digest(Canonical(r)) != stored.ReportDigest || validatePostCheckReport(*r, execution) != nil || len(r.Facts.EvidenceRefs) != len(stored.EvidenceRefs) {
			return ErrInvalid
		}
		for i, ref := range r.Facts.EvidenceRefs {
			if ref != stored.EvidenceRefs[i] {
				return ErrInvalid
			}
		}
		if r.Facts.RecipeVersion != uuid.Nil {
			var raw []byte
			if tx.QueryRow(ctx, `SELECT content FROM platform.registry_versions WHERE tenant_id=$1 AND version_id=$2 AND logical_name=$3`, a.TenantID, r.Facts.RecipeVersion, r.Facts.RecipeName).Scan(&raw) != nil {
				return ErrDenied
			}
			recipe, err := rca.DecodeRecipe(raw)
			ns := ""
			if binding.Namespace != nil {
				ns = *binding.Namespace
			}
			if err != nil || rca.VerifyRecipeForRead(ctx, tx, a.TenantID, &r.Facts.RecipeVersion, binding.ClusterUID, ns, recipe, s.Trust) != nil {
				return ErrDenied
			}
		}
	}
	return nil
}

type PostCheckView struct {
	ExecutionID    uuid.UUID        `json:"executionId"`
	ExecutionState string           `json:"executionState"`
	Verdict        string           `json:"verdict"`
	Status         string           `json:"status"`
	CheckedAt      *time.Time       `json:"checkedAt"`
	Applicability  PostCheckSupport `json:"applicability"`
	Report         *PostCheckReport `json:"report"`
	ReportDigest   string           `json:"reportDigest,omitempty"`
	DetailStatus   string           `json:"detailStatus"`
	Executions     []ExecutionLink  `json:"relatedExecutions"`
	LinksTruncated bool             `json:"linksTruncated"`
}

func (s Service) GetPostCheck(ctx context.Context, a auth.RequestContext, id uuid.UUID) (PostCheckView, error) {
	var out PostCheckView
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		e, err := executionTx(ctx, tx, a.TenantID, id)
		if err != nil {
			return err
		}
		if _, err = effectiveRead(ctx, tx, a, e.Binding); err != nil {
			return err
		}
		out = PostCheckView{ExecutionID: id, ExecutionState: e.State, Verdict: e.PostCheck, CheckedAt: e.PostCheckedAt, Status: "pending", Applicability: PostCheckApplicability(e.Binding.Target), DetailStatus: "not_recorded", Executions: []ExecutionLink{}}
		if e.PostCheckedAt != nil {
			out.Status, out.DetailStatus = "completed", "legacy_detail_unavailable"
			var payload []byte
			err = tx.QueryRow(ctx, `SELECT payload FROM action.events WHERE tenant_id=$1 AND execution_id=$2 AND event_type='post_check' ORDER BY event_seq DESC LIMIT 1`, a.TenantID, id).Scan(&payload)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				if err = s.postCheckEventReadable(ctx, tx, a, e, payload); err != nil {
					return err
				}
				var stored struct {
					Report       *PostCheckReport `json:"report"`
					ReportDigest string           `json:"reportDigest"`
				}
				if json.Unmarshal(payload, &stored) != nil {
					return ErrInvalid
				}
				out.Report = stored.Report
				out.ReportDigest = stored.ReportDigest
				if out.Report != nil {
					if out.Report.ExecutionID != id || out.Report.Verdict != out.Verdict {
						return ErrInvalid
					}
					out.DetailStatus = "recorded"
				}
			}
		} else if e.State == "cancelled" {
			out.Status = "not_applicable"
		} else if e.State == "execution_unknown" {
			out.Status = "execution_unknown"
		}
		out.Executions, out.LinksTruncated, err = executionLinksTx(ctx, tx, a, e)
		return err
	})
	return out, err
}
