package action

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/authz"
	"ops-platform/internal/graph"
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
		if _, err := auth.TouchCurrentStepUpSession(ctx, tx, a, []string{auth.StepUpACRLevel2}); err != nil {
			return err
		}
		if err := authz.RequireAdmin(ctx, tx, a); err != nil {
			if errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrIdentityMismatch) {
				return ErrDenied
			}
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
		if _, err := auth.TouchCurrentStepUpSession(ctx, tx, a, []string{auth.StepUpACRLevel2}); err != nil {
			return err
		}
		if err := authz.RequireAdmin(ctx, tx, a); err != nil {
			if errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrIdentityMismatch) {
				return ErrDenied
			}
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
const executionColumns = `execution_id,binding,authority,request_digest,state,suggestion_match,comparator_version,exit_code,output_bytes,output_truncated,post_check,created_at,risk_acknowledgement_id,policy_decision_id,claimed_at,completed_at,output_archive_ref,output_digest,output_previews,post_checked_at,COALESCE((SELECT (content->>'iteration')::int FROM action.plans WHERE tenant_id=$1 AND action_plan_id=(action.executions.binding->>'actionPlanId')::uuid),1),(SELECT content->>'suggestedCommand' FROM action.plans WHERE tenant_id=$1 AND action_plan_id=(action.executions.binding->>'actionPlanId')::uuid),step_up_session_id,step_up_proof_id,step_up_operation,step_up_binding_digest,step_up_expires_at`

type executionScanner interface {
	Scan(dest ...any) error
}

func scanExecution(s executionScanner) (Execution, error) {
	var e Execution
	var b []byte
	var authorityRaw []byte
	var suggested *string
	var stepSession, stepProof *uuid.UUID
	var stepOperation *string
	var stepDigest []byte
	var stepExpires *time.Time
	err := s.Scan(&e.ID, &b, &authorityRaw, &e.RequestDigest, &e.State, &e.SuggestionMatch, &e.ComparatorVersion, &e.ExitCode, &e.OutputBytes, &e.Truncated, &e.PostCheck, &e.CreatedAt, &e.AckID, &e.PolicyID, &e.StartedAt, &e.CompletedAt, &e.ArchiveRef, &e.OutputDigest, &e.OutputPreviews, &e.PostCheckedAt, &e.Iteration, &suggested, &stepSession, &stepProof, &stepOperation, &stepDigest, &stepExpires)
	if err != nil {
		return e, err
	}
	if json.Unmarshal(b, &e.Binding) != nil || json.Unmarshal(authorityRaw, &e.Authority) != nil {
		return e, ErrInvalid
	}
	if stepSession != nil && stepProof != nil && stepOperation != nil && len(stepDigest) == sha256.Size && stepExpires != nil {
		e.stepUp = &frozenStepUp{SessionID: *stepSession, ProofID: *stepProof, Operation: *stepOperation, BindingDigest: stepDigest, ExpiresAt: *stepExpires}
	}
	if suggested != nil {
		d := Digest([]byte(*suggested))
		e.SuggestedDigest = &d
	}
	return e, nil
}

func executionTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Execution, error) {
	e, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM action.executions WHERE tenant_id=$1 AND execution_id=$2 FOR UPDATE`, tenant, id))
	if err != nil {
		return e, err
	}
	if e.ID != id {
		return Execution{}, ErrInvalid
	}
	return e, nil
}

// ListExecutions returns one tenant-wide page newest first. Rows the caller's
// effective scope cannot read are skipped, never leaked; the keyset cursor is
// the last returned execution id, so hidden rows are re-scanned on later pages.
func (s Service) ListExecutions(ctx context.Context, a auth.RequestContext, before *uuid.UUID, state string, limit int) ([]Execution, *uuid.UUID, error) {
	out := []Execution{}
	var next *uuid.UUID
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		scopes := map[string]graph.Scope{}
		cursor := before
		for len(out) < limit {
			rows, err := tx.Query(ctx, `SELECT `+executionColumns+` FROM action.executions WHERE tenant_id=$1 AND ($2::uuid IS NULL OR execution_id<$2) AND ($3='' OR state=$3) ORDER BY execution_id DESC LIMIT 100`, a.TenantID, cursor, state)
			if err != nil {
				return err
			}
			// Buffer the batch before filtering: authorization re-proof queries
			// the same transaction and cannot overlap an open result set.
			batch := []Execution{}
			for rows.Next() {
				var e Execution
				if e, err = scanExecution(rows); err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, e)
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return err
			}
			if len(batch) == 0 {
				return nil
			}
			scannedAll := true
			for i, e := range batch {
				ok, rerr := readable(ctx, tx, a, e.Binding, scopes)
				if rerr != nil {
					return rerr
				}
				if !ok {
					continue
				}
				out = append(out, e)
				if len(out) == limit {
					scannedAll = i == len(batch)-1
					break
				}
			}
			if len(out) == limit {
				// A page that ends exactly on the last stored row has no
				// successor; anything else may continue from the last row.
				if scannedAll && len(batch) < 100 {
					return nil
				}
				last := out[len(out)-1].ID
				next = &last
				return nil
			}
			if len(batch) < 100 {
				return nil
			}
			last := batch[len(batch)-1].ID
			cursor = &last
		}
		return nil
	})
	return out, next, err
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
			err := authz.RequireAdmin(ctx, tx, a)
			if err == nil {
				admin = true
			} else if !errors.Is(err, authz.ErrUnauthorized) {
				if errors.Is(err, authz.ErrIdentityMismatch) {
					return ErrDenied
				}
				return err
			}
		}
		if !admin {
			if !slices.Contains(a.Roles, auth.Operator) {
				return ErrDenied
			}
			if _, native := auth.NativeSessionFromContext(ctx); native {
				allowed, err := authz.NativeRoleAuthorized(ctx, tx, a, "operator")
				if err != nil {
					if errors.Is(err, authz.ErrIdentityMismatch) {
						return ErrDenied
					}
					return err
				}
				if !allowed {
					return ErrDenied
				}
			}
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
