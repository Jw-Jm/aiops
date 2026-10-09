package action

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"slices"
	"time"
)

type PublishProfileRequest struct {
	Profile     Profile `json:"profile"`
	SignerKeyID string  `json:"signerKeyId"`
	Signature   []byte  `json:"signature"`
}

func (s Service) PublishProfile(ctx context.Context, a auth.RequestContext, in PublishProfileRequest, key string) error {
	if !slices.Contains(a.Roles, auth.PlatformAdmin) || !time.Now().Before(a.TokenExpiresAt) || s.AdmitProfile == nil {
		return ErrDenied
	}
	if err := VerifyProfile(ctx, s.Trust, a.TenantID, in.Profile, in.SignerKeyID, in.Signature); err != nil {
		return err
	}
	if err := s.AdmitProfile(ctx, in.Profile); err != nil {
		return err
	}
	return persistence.WithTenantTx(ctx, s.Pool, a.TenantID, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2 AND role_name='platform_admin' AND status='active' FOR SHARE)`, a.TenantID, a.Subject).Scan(&exists); err != nil || !exists {
			return ErrDenied
		}
		if _, err := auth.TouchCurrentStepUpSession(ctx, tx, a, []string{auth.StepUpACRLevel2}); err != nil {
			return err
		}
		d, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: "publish-execution-profile"}, key, persistence.Digest(Digest(Canonical(in))))
		if err != nil {
			return err
		}
		if d.Kind == persistence.DecisionReplay {
			return nil
		}
		if d.Kind != persistence.DecisionProceed {
			return ErrConflict
		}
		p := in.Profile
		if err := hostAdmittedTx(ctx, tx, a.TenantID, p); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO platform.execution_profile_versions(tenant_id,profile_version_id,logical_name,version_number,content,digest,signature,signer_key_id,status,published_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'published',$9)`, a.TenantID, uuid.Must(uuid.NewV7()), p.Name, p.Version, Canonical(p), Digest(Canonical(p)), in.Signature, in.SignerKeyID, a.Subject)
		if err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, a, p.ID, "execution_profile.published", map[string]any{"version": p.Version, "digest": Digest(Canonical(p))}); err != nil {
			return err
		}
		return complete(ctx, tx, d, map[string]any{"executionProfileId": p.ID, "version": p.Version}, 201)
	})
}
func (s Service) RetireProfile(ctx context.Context, a auth.RequestContext, id uuid.UUID, version int, key string) error {
	if !slices.Contains(a.Roles, auth.PlatformAdmin) || !time.Now().Before(a.TokenExpiresAt) {
		return ErrDenied
	}
	return persistence.WithTenantTx(ctx, s.Pool, a.TenantID, func(tx pgx.Tx) error {
		var locked uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT binding_id FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2 AND role_name='platform_admin' AND status='active' FOR SHARE`, a.TenantID, a.Subject).Scan(&locked); err != nil {
			return ErrDenied
		}
		if _, err := auth.TouchCurrentStepUpSession(ctx, tx, a, []string{auth.StepUpACRLevel2}); err != nil {
			return err
		}
		d, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: "retire-execution-profile"}, key, persistence.Digest(Digest(Canonical([]any{id, version}))))
		if err != nil {
			return err
		}
		if d.Kind == persistence.DecisionReplay {
			return nil
		}
		if d.Kind != persistence.DecisionProceed {
			return ErrConflict
		}
		tag, err := tx.Exec(ctx, `UPDATE platform.execution_profile_versions SET status='retired' WHERE tenant_id=$1 AND content->>'executionProfileId'=$2 AND version_number=$3 AND status='published'`, a.TenantID, id.String(), version)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		if err = appendAudit(ctx, tx, a, id, "execution_profile.retired", map[string]any{"version": version}); err != nil {
			return err
		}
		return complete(ctx, tx, d, map[string]any{"status": "retired"}, 200)
	})
}
func executionTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Execution, error) {
	var e Execution
	var b []byte
	var suggested *string
	e.ID = id
	err := tx.QueryRow(ctx, `SELECT binding,request_digest,state,suggestion_match,comparator_version,exit_code,output_bytes,output_truncated,post_check,created_at,risk_acknowledgement_id,policy_decision_id,claimed_at,completed_at,output_archive_ref,output_digest,output_previews,COALESCE((SELECT (content->>'iteration')::int FROM action.plans WHERE tenant_id=$1 AND action_plan_id=(action.executions.binding->>'actionPlanId')::uuid),1),(SELECT content->>'suggestedCommand' FROM action.plans WHERE tenant_id=$1 AND action_plan_id=(action.executions.binding->>'actionPlanId')::uuid) FROM action.executions WHERE tenant_id=$1 AND execution_id=$2 FOR UPDATE`, tenant, id).Scan(&b, &e.RequestDigest, &e.State, &e.SuggestionMatch, &e.ComparatorVersion, &e.ExitCode, &e.OutputBytes, &e.Truncated, &e.PostCheck, &e.CreatedAt, &e.AckID, &e.PolicyID, &e.StartedAt, &e.CompletedAt, &e.ArchiveRef, &e.OutputDigest, &e.OutputPreviews, &e.Iteration, &suggested)
	if err != nil {
		return e, err
	}
	if json.Unmarshal(b, &e.Binding) != nil {
		return e, ErrInvalid
	}
	if suggested != nil {
		d := Digest([]byte(*suggested))
		e.SuggestedDigest = &d
	}
	return e, nil
}
func (s Service) Get(ctx context.Context, a auth.RequestContext, id uuid.UUID) (Execution, error) {
	var e Execution
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		var err error
		e, err = executionTx(ctx, tx, a.TenantID, id)
		if err != nil {
			return err
		}
		_, err = effectiveRead(ctx, tx, a, e.Binding)
		return err
	})
	return e, err
}
func (s Service) CancelExecution(ctx context.Context, a auth.RequestContext, id uuid.UUID, key string) (Execution, error) {
	var e Execution
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		var err error
		e, err = executionTx(ctx, tx, a.TenantID, id)
		if err != nil {
			return err
		}
		if _, err = effectiveRead(ctx, tx, a, e.Binding); err != nil {
			return err
		}
		d, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: "cancel-command-execution"}, key, persistence.Digest(Digest(Canonical(id))))
		if err != nil {
			return err
		}
		if d.Kind == persistence.DecisionReplay {
			return json.Unmarshal(d.Response.Body, &e)
		}
		if d.Kind != persistence.DecisionProceed {
			return ErrConflict
		}
		switch e.State {
		case "prepared":
			e.State = "cancelled"
		case "dispatching", "running":
			e.State = "execution_unknown"
		case "execution_unknown":
		default:
			return ErrConflict
		}
		_, err = tx.Exec(ctx, `UPDATE action.executions SET state=$3,cancellation_requested_at=clock_timestamp(),updated_at=clock_timestamp(),completed_at=CASE WHEN $3 IN('cancelled','execution_unknown') THEN clock_timestamp() ELSE completed_at END WHERE tenant_id=$1 AND execution_id=$2`, a.TenantID, id, e.State)
		if err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, a, id, "command.cancel_requested", map[string]any{"state": e.State, "terminationConfirmed": e.State == "cancelled"}); err != nil {
			return err
		}
		if err = event(ctx, tx, a.TenantID, id, "state", map[string]any{"state": e.State}); err != nil {
			return err
		}
		return complete(ctx, tx, d, e, 200)
	})
	return e, err
}

func (s Service) ListProfiles(ctx context.Context, a auth.RequestContext) ([]Profile, error) {
	out := []Profile{}
	if a.TenantID == uuid.Nil || a.Subject == "" || !time.Now().Before(a.TokenExpiresAt) {
		return nil, ErrDenied
	}
	err := persistence.WithTenantTx(ctx, s.Pool, a.TenantID, func(tx pgx.Tx) error {
		admin := false
		if slices.Contains(a.Roles, auth.PlatformAdmin) {
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2 AND role_name='platform_admin' AND status='active')`, a.TenantID, a.Subject).Scan(&admin); err != nil {
				return err
			}
		}
		if !admin && !slices.Contains(a.Roles, auth.Operator) {
			return ErrDenied
		}
		rows, err := tx.Query(ctx, `SELECT content FROM platform.execution_profile_versions WHERE tenant_id=$1 AND status='published' ORDER BY logical_name,version_number`, a.TenantID)
		if err != nil {
			return err
		}
		candidates := []Profile{}
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			p, err := DecodeProfile(raw)
			if err == nil {
				candidates = append(candidates, p)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, p := range candidates {
			if admin {
				out = append(out, p)
				continue
			}
			for _, target := range p.AllowedTargets {
				b := Binding{Target: target, ClusterUID: p.ClusterUID}
				if p.Namespace != "" {
					b.Namespace = &p.Namespace
				}
				if _, err = effectiveRead(ctx, tx, a, b); err == nil {
					out = append(out, p)
					break
				}
			}
		}
		return nil
	})
	return out, err
}
