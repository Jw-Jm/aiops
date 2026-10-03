package incident

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

func linkedActivity(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string) (bool, error) {
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2 AND f.lifecycle_state IS DISTINCT FROM 'resolved')`, tenant, id).Scan(&active)
	return active, err
}

// A deliberate regrouping learns the new complete group's state now. It must
// retain the full settle window, and ordinary source/Graph health gates still apply.
func recomputeRecovery(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, i *Incident) error {
	active, err := linkedActivity(ctx, tx, tenant, i.IncidentID)
	if err != nil {
		return err
	}
	if active {
		i.RecoveryKnownAt = nil
	} else if i.RecoveryKnownAt == nil {
		now := time.Now().UTC()
		i.RecoveryKnownAt = &now
	}
	return nil
}
