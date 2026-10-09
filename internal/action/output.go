package action

import (
	"bytes"
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
	"time"
)

type OutputChunk struct {
	ExecutionID uuid.UUID `json:"executionId"`
	Stream      string    `json:"stream"`
	Seq         int64     `json:"seq"`
	Bytes       []byte    `json:"bytes"`
	ObservedAt  time.Time `json:"observedAt"`
}

func (s Service) AppendOutput(ctx context.Context, tenant, id uuid.UUID, token string, c OutputChunk) error {
	if c.ExecutionID != id || c.Seq < 1 || len(c.Bytes) > MaxChunkBytes || len(c.Bytes) == 0 || (c.Stream != "stdout" && c.Stream != "stderr") || len(token) != 43 {
		return ErrInvalid
	}
	return persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		e, err := executionTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		var valid bool
		var seq int64
		if err = tx.QueryRow(ctx, `SELECT claimed_at IS NOT NULL AND dispatch_token_digest=$3,output_seq FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, tenant, id, Digest([]byte(token))).Scan(&valid, &seq); err != nil {
			return err
		}
		if !valid {
			return ErrDenied
		}
		if c.Seq <= seq {
			var stream string
			var b []byte
			err = tx.QueryRow(ctx, `SELECT stream,bytes FROM action.output_chunks WHERE tenant_id=$1 AND execution_id=$2 AND seq=$3`, tenant, id, c.Seq).Scan(&stream, &b)
			if err != nil || stream != c.Stream || !bytes.Equal(b, c.Bytes) {
				return ErrConflict
			}
			return nil
		}
		if e.State != "running" || c.Seq != seq+1 {
			return ErrConflict
		}
		remaining := e.Binding.Options.MaxOutputBytes - e.OutputBytes
		if remaining < int64(len(c.Bytes)) {
			// Reject rather than acknowledge an unstored sequence. The bounded
			// Runner truncates locally; a bad callback must not create events.
			return ErrConflict
		}
		err = tx.QueryRow(ctx, `INSERT INTO action.output_chunks(tenant_id,execution_id,seq,stream,bytes) VALUES($1,$2,$3,$4,$5) RETURNING observed_at`, tenant, id, c.Seq, c.Stream, c.Bytes).Scan(&c.ObservedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE action.executions SET output_seq=$3,output_bytes=output_bytes+$4 WHERE tenant_id=$1 AND execution_id=$2`, tenant, id, c.Seq, len(c.Bytes))
		if err != nil {
			return err
		}
		return event(ctx, tx, tenant, id, "output", map[string]any{"seq": c.Seq, "stream": c.Stream, "bytes": len(c.Bytes), "observedAt": c.ObservedAt})
	})
}
func (s Service) Finish(ctx context.Context, tenant, id uuid.UUID, token string, exitCode int, finalSeq int64, truncated bool) error {
	if exitCode < 0 || exitCode > 255 || len(token) != 43 {
		return ErrInvalid
	}
	return persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		e, err := executionTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		var valid bool
		var seq int64
		if err = tx.QueryRow(ctx, `SELECT claimed_at IS NOT NULL AND dispatch_token_digest=$3,output_seq FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, tenant, id, Digest([]byte(token))).Scan(&valid, &seq); err != nil {
			return err
		}
		if !valid {
			return ErrDenied
		}
		if e.State == "succeeded" || e.State == "failed" {
			if e.ExitCode != nil && *e.ExitCode == exitCode && seq == finalSeq {
				return nil
			}
			return ErrConflict
		}
		if e.State != "running" && e.State != "execution_unknown" {
			return ErrConflict
		}
		if e.State == "execution_unknown" && e.ExitCode != nil {
			if *e.ExitCode == exitCode && seq == finalSeq && e.Truncated == truncated {
				return nil
			}
			return ErrConflict
		}
		state := "failed"
		if exitCode == 0 {
			state = "succeeded"
		}
		if seq != finalSeq {
			state = "execution_unknown"
		}
		if e.State == "execution_unknown" {
			state = "execution_unknown"
		} // A late callback alone does not reconcile target side effects.
		_, err = tx.Exec(ctx, `UPDATE action.executions SET state=$3,exit_code=$4,completed_at=COALESCE(completed_at,clock_timestamp()),output_truncated=output_truncated OR $5 WHERE tenant_id=$1 AND execution_id=$2`, tenant, id, state, exitCode, truncated)
		if err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, workerActor(tenant), id, "command.completed", map[string]any{"state": state, "exitCode": exitCode, "outputSeq": seq}); err != nil {
			return err
		}
		return event(ctx, tx, tenant, id, "state", map[string]any{"state": state, "exitCode": exitCode, "postCheck": "inconclusive"})
	})
}
