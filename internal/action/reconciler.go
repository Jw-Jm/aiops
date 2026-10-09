package action

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
	"strings"
	"time"
)

// ReconcilePass can run on competing Workers. The execution row and unique
// attempt fence dispatch; takeover never redispatches a free-form command.
func (s Service) ReconcilePass(ctx context.Context, tenant uuid.UUID, executor KubernetesExecutor) error {
	type pending struct {
		ID         uuid.UUID
		State, Ref string
		Updated    time.Time
		Timeout    int
		Cancel     bool
	}
	items := []pending{}
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT execution_id,state,COALESCE(runner_ref,''),updated_at,COALESCE((binding->'executionOptions'->>'timeoutSeconds')::int,900),cancellation_requested_at IS NOT NULL FROM action.executions WHERE tenant_id=$1 AND (state IN('prepared','dispatching','running') OR (state='execution_unknown' AND cancellation_requested_at IS NOT NULL AND runner_termination_requested_at IS NULL AND runner_ref IS NOT NULL)) ORDER BY created_at LIMIT 20`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p pending
			if err = rows.Scan(&p.ID, &p.State, &p.Ref, &p.Updated, &p.Timeout, &p.Cancel); err != nil {
				return err
			}
			items = append(items, p)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	for _, p := range items {
		if p.State == "prepared" {
			d, err := s.BeginDispatch(ctx, tenant, p.ID)
			if err == ErrConflict || err == ErrDenied {
				continue
			}
			if err != nil {
				return err
			}
			ref, err := executor.Dispatch(ctx, d)
			if err != nil {
				if e := s.MarkUnknown(ctx, tenant, p.ID, "submission_uncertain"); e != nil {
					return e
				}
				continue
			}
			if err = s.RecordRunnerRef(ctx, d, ref); err != nil {
				return err
			}
			continue
		}
		if p.Cancel && p.Ref != "" {
			parts := strings.Split(p.Ref, "/")
			if len(parts) == 2 {
				if err = executor.Client.DeleteJob(ctx, executor.RunnerNamespace, parts[0], parts[1]); err != nil {
					continue
				}
				if err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
					_, err := tx.Exec(ctx, `UPDATE action.executions SET runner_termination_requested_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2`, tenant, p.ID)
					if err != nil {
						return err
					}
					return appendAudit(ctx, tx, workerActor(tenant), p.ID, "command.runner_termination_requested", map[string]any{"runnerRef": p.Ref, "sideEffectsReconciled": false})
				}); err != nil {
					return err
				}
			}
		}
		if p.State == "execution_unknown" {
			continue
		}
		if p.Ref == "" {
			if time.Since(p.Updated) > 30*time.Second {
				if err = s.MarkUnknown(ctx, tenant, p.ID, "worker_lost_dispatch_receipt"); err != nil {
					return err
				}
			}
			continue
		}
		state, readErr := executor.Client.JobState(ctx, executor.RunnerNamespace, p.Ref)
		if readErr != nil || state == "completed" || state == "failed" || time.Since(p.Updated) > time.Duration(p.Timeout+30)*time.Second {
			if err = s.MarkUnknown(ctx, tenant, p.ID, "runner_result_not_proven"); err != nil {
				return err
			}
		}
	}
	return nil
}
