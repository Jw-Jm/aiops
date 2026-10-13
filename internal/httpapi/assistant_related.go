package httpapi

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"time"
)

type assistantRelatedIncident struct {
	IncidentID uuid.UUID `json:"incidentId"`
	State      string    `json:"state"`
	Revision   int64     `json:"revision"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// A resource link does not confer access to an Incident. Every associated
// Finding must be visible in this turn's scope before even metadata is returned.
func (a AssistantTools) relatedIncidents(ctx context.Context, j investigation.Job, ids []string) ([]assistantRelatedIncident, bool, error) {
	out := []assistantRelatedIncident{}
	truncated := false
	err := persistence.WithTenantTx(ctx, a.Legacy.SP04.Pool, j.TenantID, func(tx pgx.Tx) error {
		if err := investigation.CheckAssistantJobTx(ctx, tx, j); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT i.incident_id,i.state,i.revision,i.updated_at FROM incident.records i
WHERE i.tenant_id=$1 AND i.cluster_uid=$2 AND (i.namespace=ANY($3::text[]) OR (i.namespace='' AND $4))
AND (i.resource_canonical_id=ANY($5::text[]) OR EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=i.tenant_id AND l.incident_id=i.incident_id AND f.resource_canonical_id=ANY($5::text[])))
AND NOT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=i.tenant_id AND l.incident_id=i.incident_id AND NOT COALESCE(f.cluster_uid=$2 AND (f.namespace=ANY($3::text[]) OR (f.namespace='' AND $4)) AND (cardinality($6::text[])=0 OR f.resource_canonical_id=ANY($6::text[])),false))
ORDER BY i.updated_at DESC,i.incident_id LIMIT 51`, j.TenantID, j.Scope.Cluster, append([]string{}, j.Scope.Namespaces...), j.Scope.ClusterScoped, ids, append([]string{}, j.Scope.Resources...))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item assistantRelatedIncident
			if err = rows.Scan(&item.IncidentID, &item.State, &item.Revision, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		if len(out) > 50 {
			out = out[:50]
			truncated = true
		}
		return rows.Err()
	})
	return out, truncated, err
}
