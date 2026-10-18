package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"math"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
	"time"
)

// The existing worker pass owns sampling; telemetry has no new scheduler and
// cannot extend a business transaction or confer additional DB privileges.
func collectBusinessMetrics(ctx context.Context, pool *pgxpool.Pool, m *observability.Metrics) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	m.InvalidateBusinessSnapshot()
	if err := pool.Ping(bounded); err != nil {
		m.SetDatabaseAvailable(false)
		return
	}
	m.SetDatabaseAvailable(true)
	rows, err := pool.Query(bounded, `SELECT tenant_id FROM platform.tenants ORDER BY tenant_id`)
	if err != nil {
		return
	}
	tenants := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) != nil {
			rows.Close()
			return
		}
		tenants = append(tenants, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	queues := map[string]float64{"finding_pending": 0, "finding_deadletter": 0, "incident_pending": 0, "investigation_queued": 0, "investigation_running": 0}
	results := map[string]map[string]float64{"tool": {}, "tool_admission": {}, "action": {}}
	var totalSeconds, totalJobs float64
	for _, tenant := range tenants {
		err = persistence.WithTenantTx(bounded, pool, tenant, func(tx pgx.Tx) error {
			var pending, dead, incident, queued, running int64
			var seconds, jobs float64
			if err := tx.QueryRow(bounded, `SELECT
    (SELECT count(*) FROM finding.outbox WHERE state IN('pending','claimed')),
    (SELECT count(*) FROM finding.outbox WHERE state='deadletter'),
    (SELECT count(*) FROM incident.outbox WHERE state='pending'),
    count(*) FILTER(WHERE state='queued'),count(*) FILTER(WHERE state='running'),
    COALESCE(sum(EXTRACT(EPOCH FROM updated_at-created_at)) FILTER(WHERE state IN('succeeded','partial','failed','cancelled','expired') AND updated_at>=clock_timestamp()-interval '5 minutes'),0)::double precision,
    count(*) FILTER(WHERE state IN('succeeded','partial','failed','cancelled','expired') AND updated_at>=clock_timestamp()-interval '5 minutes')::double precision
    FROM investigation.jobs`).Scan(&pending, &dead, &incident, &queued, &running, &seconds, &jobs); err != nil {
				return err
			}
			var raw []byte
			if err := tx.QueryRow(bounded, `SELECT jsonb_build_object(
             'tool_admission',COALESCE((SELECT jsonb_object_agg(state,n) FROM (SELECT a.state,count(*) n FROM investigation.admissions a JOIN investigation.steps s USING(tenant_id,job_id,step_id) WHERE a.created_at>=clock_timestamp()-interval '5 minutes' AND s.tool_name<>'model' GROUP BY a.state) admissions),'{}'::jsonb),
             'tool',COALESCE((SELECT jsonb_object_agg(state,n) FROM (SELECT state,count(*) n FROM investigation.steps WHERE started_at>=clock_timestamp()-interval '5 minutes' AND tool_name<>'model' GROUP BY state) s),'{}'::jsonb),
             'action',COALESCE((SELECT jsonb_object_agg(state,n) FROM (SELECT state,count(*) n FROM action.executions WHERE updated_at>=clock_timestamp()-interval '5 minutes' GROUP BY state) e),'{}'::jsonb))`).Scan(&raw); err != nil {
				return err
			}
			var recent map[string]map[string]float64
			if err := json.Unmarshal(raw, &recent); err != nil {
				return err
			}
			for area, states := range recent {
				if results[area] == nil {
					return fmt.Errorf("unsupported metric area")
				}
				for state, value := range states {
					results[area][state] += value
				}
			}

			queues["finding_pending"] += float64(pending)
			queues["finding_deadletter"] += float64(dead)
			queues["incident_pending"] += float64(incident)
			queues["investigation_queued"] += float64(queued)
			queues["investigation_running"] += float64(running)
			totalSeconds += seconds
			totalJobs += jobs
			return nil
		})
		if err != nil {
			return
		}
	}
	mean := math.NaN()
	if totalJobs > 0 {
		mean = totalSeconds / totalJobs
	}
	m.SetBusinessSnapshot(queues, mean, results)
}
