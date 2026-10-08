//go:build ops_readonly_recovery

// This finite operator observes one authorized Job; it cannot execute commands.
package main

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/persistence"
	"os"
	"time"
)

type configuration struct{ DatabaseURL, TenantID, JobID, Output string }

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	var c configuration
	b, err := os.ReadFile(os.Args[1])
	if err != nil || json.Unmarshal(b, &c) != nil {
		os.Exit(2)
	}
	tenant, err := uuid.Parse(c.TenantID)
	if err != nil {
		os.Exit(2)
	}
	job, err := uuid.Parse(c.JobID)
	if err != nil {
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		os.Exit(2)
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		os.Exit(2)
	}
	defer pool.Close()
	for ctx.Err() == nil {
		var raw []byte
		err = persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT jsonb_build_object('jobId',q.job_id,'fencingEpoch',q.fencing_epoch,'committedToolStepId',t.step_id,'committedToolResultDigest',t.result_digest,'pendingModelStepId',m.step_id,'modelReservation',m.reservation,'observedAt',clock_timestamp()) FROM investigation.worker_queue q JOIN LATERAL(SELECT step_id,result_digest FROM investigation.steps WHERE tenant_id=q.tenant_id AND job_id=q.job_id AND tool_name<>'model' AND state='succeeded' ORDER BY started_at LIMIT 1)t ON true JOIN LATERAL(SELECT a.step_id,a.reservation FROM investigation.admissions a JOIN investigation.steps s USING(tenant_id,job_id,step_id) WHERE a.tenant_id=q.tenant_id AND a.job_id=q.job_id AND a.state='reserved' AND s.tool_name='model' AND s.state='running' ORDER BY a.created_at LIMIT 1)m ON true WHERE q.tenant_id=$1 AND q.job_id=$2`, tenant, job).Scan(&raw)
		})
		if err == nil {
			if os.WriteFile(c.Output, append(raw, '\n'), 0600) != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	os.Exit(1)
}
