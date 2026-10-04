package investigation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"time"
)

// settleUnknown charges in-flight work at its reserved upper bound. Caller holds
// the Job and queue row locks; no remote request runs in this transaction.
func settleUnknown(ctx context.Context, tx pgx.Tx, j Job) error {
	rows, err := tx.Query(ctx, `UPDATE investigation.admissions SET state='unknown',consumed=reservation WHERE tenant_id=$1 AND job_id=$2 AND state='reserved' RETURNING reservation`, j.TenantID, j.JobID)
	if err != nil {
		return err
	}
	lost := Usage{}
	for rows.Next() {
		var b []byte
		var u Usage
		if rows.Scan(&b) != nil || json.Unmarshal(b, &u) != nil {
			rows.Close()
			return ErrInvalid
		}
		lost = lost.Add(u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE investigation.steps s SET state='failed',error_code='UNKNOWN_OUTCOME',completed_at=clock_timestamp() WHERE s.tenant_id=$1 AND s.job_id=$2 AND s.state='running' AND EXISTS(SELECT 1 FROM investigation.admissions a WHERE a.tenant_id=s.tenant_id AND a.job_id=s.job_id AND a.step_id=s.step_id AND a.state='unknown')`, j.TenantID, j.JobID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE investigation.jobs SET budget_reserved=$3,budget_consumed=$4 WHERE tenant_id=$1 AND job_id=$2`, j.TenantID, j.JobID, raw(j.Reserved.Sub(lost)), raw(j.Consumed.Add(lost)))
	return err
}

// Stop is a trusted operator/scheduler transition, not an Agent write API. It
// rotates the fencing epoch under both locks before terminating queued/running
// jobs, so old Workers and Contexts cannot publish after cancel or expiration.
func (r Repository) Stop(ctx context.Context, tenant, id uuid.UUID, subject string, expire bool, idempotencyKey ...string) error {
	return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		j, err := load(ctx, tx, tenant, id, true)
		if err != nil {
			return err
		}

		if expire {
			if time.Now().Before(j.ExpiresAt) {
				return ErrLease
			}
		} else {
			scope, err := graph.EffectiveTx(ctx, tx, tenant.String(), subject, j.Scope.Cluster, true)
			if err != nil || graph.ScopeDigest(scope) != j.EffectiveScopeDigest || subject != j.Subject {
				return ErrDenied
			}
			if err = current(ctx, tx, j); err != nil {
				return err
			}
		}
		if len(idempotencyKey) > 0 {
			if expire || len(idempotencyKey) != 1 || idempotencyKey[0] == "" || len(idempotencyKey[0]) > 128 {
				return ErrInvalid
			}
			digest := graph.ScopeDigest(struct {
				Operation string
				JobID     uuid.UUID
			}{"investigation.cancel", id})
			if _, err = tx.Exec(ctx, `INSERT INTO investigation.request_keys(tenant_id,subject,idempotency_key,request_digest,job_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, tenant, subject, idempotencyKey[0], digest, id); err != nil {
				return err
			}
			var boundID uuid.UUID
			var boundDigest string
			if err = tx.QueryRow(ctx, `SELECT job_id,request_digest FROM investigation.request_keys WHERE tenant_id=$1 AND subject=$2 AND idempotency_key=$3`, tenant, subject, idempotencyKey[0]).Scan(&boundID, &boundDigest); err != nil {
				return err
			}
			if boundID != id || boundDigest != digest {
				return ErrConflict
			}
		}
		if j.State != "queued" && j.State != "running" {
			return nil
		}
		if _, err = tx.Exec(ctx, `SELECT platform.sp06_rotate_fence($1,$2,$3)`, tenant, id, uuid.New()); err != nil {
			return err
		}
		if err = settleUnknown(ctx, tx, j); err != nil {
			return err
		}
		state, code := "cancelled", "CANCELLED"
		if expire {
			state, code = "expired", "TIME_BUDGET_EXHAUSTED"
		}
		if _, err = tx.Exec(ctx, `UPDATE investigation.jobs SET state=$3,error_code=$4 WHERE tenant_id=$1 AND job_id=$2`, tenant, id, state, code); err != nil {
			return err
		}
		return event(ctx, tx, j, state, map[string]any{"state": state, "partial": true, "errorCode": code})
	})
}

type Event struct {
	OccurredAt time.Time       `json:"occurredAt"`
	EventID    int64           `json:"eventId"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
}

func (r Repository) Events(ctx context.Context, tenant, id uuid.UUID, subject string, after int64) (Job, []Event, error) {
	var j Job
	out := []Event{}
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var err error
		j, err = load(ctx, tx, tenant, id, false)
		if err != nil {
			return err
		}
		if j.Subject != subject {
			return ErrDenied
		}
		if err = current(ctx, tx, j); err != nil {
			return err
		}
		if len(j.Result) > 0 {
			if err = authorizeResult(ctx, tx, j, j.Result); err != nil {
				return err
			}
		}
		if after < 0 || after > j.EventSeq {
			return ErrInvalid
		}
		rows, err := tx.Query(ctx, `SELECT event_seq,event_type,payload,created_at FROM investigation.events WHERE tenant_id=$1 AND job_id=$2 AND event_seq>$3 ORDER BY event_seq LIMIT 64`, tenant, id, after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Event
			if err = rows.Scan(&e.EventID, &e.Type, &e.Payload, &e.OccurredAt); err != nil {
				return err
			}
			out = append(out, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range out {
			if e.Type == "result" {
				var v struct {
					Result json.RawMessage `json:"result"`
				}
				if json.Unmarshal(e.Payload, &v) != nil {
					return ErrInvalid
				}
				if len(v.Result) > 0 {
					if err = authorizeResult(ctx, tx, j, v.Result); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	return j, out, err
}

func (r Repository) ExpirePass(ctx context.Context, tenant uuid.UUID) error {
	ids := []uuid.UUID{}
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT job_id FROM investigation.jobs WHERE tenant_id=$1 AND schema_version='investigation-job/v2' AND state IN('queued','running') AND expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 50`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err = rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = r.Stop(ctx, tenant, id, "", true); err != nil {
			return err
		}
	}
	return nil
}

func exhaustBudgetTx(ctx context.Context, tx pgx.Tx, j Job) error {
	if err := settleUnknown(ctx, tx, j); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT platform.sp06_rotate_fence($1,$2,$3)`, j.TenantID, j.JobID, uuid.New()); err != nil {
		return err
	}
	partial := Proposal{SchemaVersion: "investigation-result/v1", Status: "unresolved", Summary: "Investigation budget exhausted; committed steps remain available.", EvidenceRefs: []string{}, CandidateUpdates: []CandidateSuggestion{}, ActionPlans: []json.RawMessage{}, Partial: true, DegradedSources: []string{"budget-exhausted"}}
	if _, err := tx.Exec(ctx, `UPDATE investigation.jobs SET state='partial',error_code='BUDGET_EXHAUSTED',result=$3 WHERE tenant_id=$1 AND job_id=$2`, j.TenantID, j.JobID, raw(partial)); err != nil {
		return err
	}
	return event(ctx, tx, j, "result", map[string]any{"state": "partial", "result": partial, "errorCode": "BUDGET_EXHAUSTED"})
}
func (r Repository) ExhaustBudget(ctx context.Context, l Lease) error {
	return r.withLease(ctx, l, func(tx pgx.Tx, j Job) error { return exhaustBudgetTx(ctx, tx, j) })
}
