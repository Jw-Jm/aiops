package investigation

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/persistence"
	"slices"
)

func authorizeResult(ctx context.Context, tx pgx.Tx, j Job, b []byte) error {
	var v struct {
		EvidenceRefs []string `json:"evidenceRefs"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ErrInvalid
	}
	for _, ref := range v.EvidenceRefs {
		id, err := uuid.Parse(ref)
		if err != nil {
			return ErrInvalid
		}
		e, err := evidence.GetTx(ctx, tx, j.TenantID, id, j.Scope)
		if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, e.DataClass) {
			return ErrDenied
		}
	}
	return nil
}

// A cached result may contain data outside its Evidence envelope (notably a
// provider response). A narrowed Context cannot reuse a result admitted with a
// broader data ceiling, even when its top-level Evidence list is empty.
func authorizeStepResult(ctx context.Context, tx pgx.Tx, j Job, id uuid.UUID, result []byte) error {
	var classes []string
	if err := tx.QueryRow(ctx, `SELECT a.allowed_data_classes FROM investigation.admissions a JOIN investigation.steps s USING(tenant_id,job_id,step_id) WHERE a.tenant_id=$1 AND a.job_id=$2 AND a.step_id=$3 AND a.lease_generation=s.lease_generation AND a.state='settled'`, j.TenantID, j.JobID, id).Scan(&classes); err != nil {
		return ErrDenied
	}
	for _, c := range classes {
		if !slices.Contains(j.Budget.AllowedDataClasses, c) {
			return ErrDenied
		}
	}
	restricted, err := restrictClasses(j, classes)
	if err != nil {
		return err
	}
	return authorizeResult(ctx, tx, restricted, result)
}
func (r Repository) RecoverStep(ctx context.Context, l Lease, id uuid.UUID, digest string) (json.RawMessage, error) {
	var b []byte
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if digest == "" {
			return ErrInvalid
		}
		if err := tx.QueryRow(ctx, `SELECT result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND args_digest=$4 AND state='succeeded' AND tool_name<>'model'`, j.TenantID, j.JobID, id, digest).Scan(&b); err != nil {
			return err
		}
		if err := authorizeStepResult(ctx, tx, j, id, b); err != nil {
			return err
		}
		return chargeRead(ctx, tx, j, "step", int64(len(b)))
	})
	if errors.Is(err, ErrBudget) {
		_ = r.ExhaustBudget(ctx, l)
	}
	if err != nil {
		b = nil
	}
	return b, err
}
func (r Repository) ReadSteps(ctx context.Context, tenant, id uuid.UUID, subject string) ([]Step, error) {
	out := []Step{}
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		j, err := load(ctx, tx, tenant, id, false)
		if err != nil {
			return err
		}
		if j.Subject != subject {
			return ErrDenied
		}
		if err = current(ctx, tx, j); err != nil {
			return err
		}
		out, err = readSteps(ctx, tx, j)
		return err
	})
	return out, err
}
func readSteps(ctx context.Context, tx pgx.Tx, j Job) ([]Step, error) {
	out := []Step{}
	rows, err := tx.Query(ctx, `SELECT step_id,tool_name,args_digest,state,result,error_code FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 ORDER BY started_at,step_id LIMIT 128`, j.TenantID, j.JobID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s Step
		if err = rows.Scan(&s.StepID, &s.Name, &s.ArgsDigest, &s.State, &s.Result, &s.ErrorCode); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, s := range out {
		if s.State == "succeeded" {
			if err = authorizeStepResult(ctx, tx, j, s.StepID, s.Result); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
func (r Repository) SettleModel(ctx context.Context, l Lease, id uuid.UUID, result json.RawMessage, used *Usage, code string) error {
	if code != "" && code != "MODEL_FAILURE" {
		return ErrInvalid
	}
	if code == "" {
		var summary struct {
			Provider   string          `json:"provider"`
			UsageKnown bool            `json:"usageKnown"`
			Response   json.RawMessage `json:"response"`
		}
		if strictJSON(result, &summary) != nil || summary.Provider != "openai-compatible" {
			return ErrInvalid
		}
		var response struct {
			Choices []struct {
				Message json.RawMessage `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal(summary.Response, &response) != nil || len(response.Choices) != 1 || len(response.Choices[0].Message) == 0 || string(response.Choices[0].Message) == "null" {
			return ErrInvalid
		}
		// The ledger, not a caller's optional usage counters, owns accounting.
		// Unknown provider usage is always charged at the admitted upper bound.
		if !summary.UsageKnown {
			used = nil
		}
	} else {
		used = nil
	}
	// Check and settlement share the Job lock. This endpoint cannot forge tool
	// results, approve candidates or complete the Job.
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		var name string
		if tx.QueryRow(ctx, `SELECT tool_name FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3`, j.TenantID, j.JobID, id).Scan(&name) != nil || name != "model" {
			return ErrDenied
		}
		if code == "" {
			if _, err := readSteps(ctx, tx, j); err != nil {
				return err
			}
		}
		return r.settleTx(ctx, tx, j, l, id, result, used, code)
	})
	if errors.Is(err, ErrBudget) {
		_ = r.ExhaustBudget(ctx, l)
	}
	return err
}
