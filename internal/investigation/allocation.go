package investigation

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"strings"
	"time"
)

type Allocation struct {
	StepID     uuid.UUID       `json:"stepId"`
	JTI        string          `json:"jti"`
	ArgsDigest string          `json:"argsDigest"`
	Committed  bool            `json:"committed"`
	Result     json.RawMessage `json:"result,omitempty"`
}

func (r Repository) RegisterContext(ctx context.Context, l Lease, c InvocationClaims) error {
	return r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if err := c.Bind(j, l, j.ToolCatalogDigest, ""); err != nil {
			return err
		}
		if !c.Valid(time.Now()) {
			return ErrInvalid
		}
		_, err := tx.Exec(ctx, `INSERT INTO investigation.contexts(tenant_id,job_id,context_id,audience,session_nonce,claims_digest,lease_generation,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, l.TenantID, l.JobID, c.ContextID, c.Audience, c.SessionNonce, graph.ScopeDigest(c), l.Generation, time.Unix(c.ExpiresAt, 0))
		return err
	})
}
func (r Repository) AllocateTool(ctx context.Context, l Lease, name string, args json.RawMessage) (Allocation, error) {
	out := Allocation{StepID: uuid.Must(uuid.NewV7()), JTI: uuid.NewString(), ArgsDigest: ArgumentsDigest(args)}
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		rows, err := tx.Query(ctx, `SELECT step_id FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name=$3 AND args_digest=$4 AND state='succeeded' ORDER BY started_at LIMIT 1`, l.TenantID, l.JobID, name, out.ArgsDigest)
		if err != nil {
			return err
		}
		if rows.Next() {
			if err = rows.Scan(&out.StepID); err != nil {
				rows.Close()
				return err
			}
			out.Committed = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if out.Committed {
			return nil
		}
		// An allocation has no remote side effect. Reuse an identical pending
		// allocation and bound all other pending allocations by the remaining
		// tool budget; an Agent cannot grow this ledger without limit.
		err = tx.QueryRow(ctx, `SELECT a.step_id,a.jti FROM investigation.call_allocations a WHERE a.tenant_id=$1 AND a.job_id=$2 AND a.tool_name=$3 AND a.args_digest=$4 AND a.lease_generation=$5 AND NOT EXISTS(SELECT 1 FROM investigation.admissions c WHERE c.tenant_id=a.tenant_id AND c.job_id=a.job_id AND c.step_id=a.step_id) ORDER BY a.created_at LIMIT 1`, j.TenantID, j.JobID, name, out.ArgsDigest, l.Generation).Scan(&out.StepID, &out.JTI)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var pending int64
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM investigation.call_allocations a WHERE a.tenant_id=$1 AND a.job_id=$2 AND a.lease_generation=$3 AND NOT EXISTS(SELECT 1 FROM investigation.admissions c WHERE c.tenant_id=a.tenant_id AND c.job_id=a.job_id AND c.step_id=a.step_id)`, j.TenantID, j.JobID, l.Generation).Scan(&pending); err != nil {
			return err
		}
		if pending >= j.Budget.ToolCalls-j.Reserved.ToolCalls-j.Consumed.ToolCalls {
			return ErrBudget
		}
		_, err = tx.Exec(ctx, `INSERT INTO investigation.call_allocations(tenant_id,job_id,step_id,jti,tool_name,args_digest,lease_generation,model) VALUES($1,$2,$3,$4,$5,$6,$7,false)`, l.TenantID, l.JobID, out.StepID, out.JTI, name, out.ArgsDigest, l.Generation)
		return err
	})
	if errors.Is(err, ErrBudget) {
		_ = r.ExhaustBudget(ctx, l)
	}
	return out, err
}
func (r Repository) AllocateModel(ctx context.Context, l Lease, reserve Usage, args json.RawMessage) (Allocation, error) {
	out := Allocation{StepID: uuid.Must(uuid.NewV7()), JTI: uuid.NewString(), ArgsDigest: ArgumentsDigest(args)}
	var request struct {
		RequestDigest string `json:"requestDigest"`
	}
	if strictJSON(args, &request) != nil || len(request.RequestDigest) != 71 || !strings.HasPrefix(request.RequestDigest, "sha256:") {
		return out, ErrInvalid
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(request.RequestDigest, "sha256:")); err != nil {
		return out, ErrInvalid
	}
	if reserve.ModelRequests != 1 || reserve.ToolCalls != 0 || reserve.RawQueries != 0 || reserve.ResultBytes != 65536 || reserve.InputTokens != 16384 || reserve.OutputTokens < 1 || reserve.OutputTokens > 8192 || reserve.ModelCostMicros != 0 {
		return out, ErrBudget
	}
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		err := tx.QueryRow(ctx, `SELECT step_id,result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='model' AND args_digest=$3 AND state='succeeded' ORDER BY started_at LIMIT 1`, j.TenantID, j.JobID, out.ArgsDigest).Scan(&out.StepID, &out.Result)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := readSteps(ctx, tx, j); err != nil {
			return err
		}
		if err := chargeRead(ctx, tx, j, "model", int64(len(out.Result))); err != nil {
			return err
		}
		out.Committed = true
		return nil
	})
	if err != nil || out.Committed {
		if errors.Is(err, ErrBudget) {
			_ = r.ExhaustBudget(ctx, l)
		}
		if err != nil {
			out.Result = nil
		}
		return out, err
	}
	_, err = r.BeginCall(ctx, l, Call{StepID: out.StepID, ContextID: uuid.New(), JTI: out.JTI, Name: "model", ArgsDigest: out.ArgsDigest, Reserve: reserve, Model: true})
	return out, err
}

// ArgumentsDigest canonicalizes object key order and whitespace at both adapters.
func ArgumentsDigest(b []byte) string {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	return graph.ScopeDigest(v)
}
