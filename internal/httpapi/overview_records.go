package httpapi

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"ops-platform/internal/rca"
	"time"
)

type overviewIncident struct {
	IncidentID          string     `json:"incidentId"`
	Title               string     `json:"title"`
	ResourceCanonicalID string     `json:"resourceCanonicalId"`
	State               string     `json:"state"`
	Severity            string     `json:"severity"`
	StartedAt           *time.Time `json:"startedAt"`
	DurationSeconds     *float64   `json:"durationSeconds"`
	RCARevision         int64      `json:"rcaRevision"`
	RCAStatus           string     `json:"rcaStatus"`
	Impact              any        `json:"impact"`
}

// Namespace and linked-Finding checks match the Incident list. Resource filters
// also fence every linked Finding, rather than just its primary resource.
const overviewIncidentsSQL = `WITH visible AS (
 SELECT t.*, ` + incidentSeveritySQL + ` AS severity
 FROM incident.records t WHERE t.tenant_id=$1 AND t.cluster_uid=$2
 AND (($3 AND t.namespace='') OR t.namespace=ANY($4::text[]))
 AND (cardinality($5::text[])=0 OR t.resource_canonical_id=ANY($5::text[]))
 AND NOT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id)
  WHERE l.tenant_id=t.tenant_id AND l.incident_id=t.incident_id AND
   (NOT (($3 AND f.namespace='') OR f.namespace=ANY($4::text[]))
    OR (cardinality($5::text[])>0 AND NOT f.resource_canonical_id=ANY($5::text[]))))
)`

func (h *SP04Handlers) overviewRecords(ctx context.Context, actor auth.RequestContext, scope graph.Scope) (map[string]any, []overviewIncident, error) {
	counts := map[string]any{}
	items := []overviewIncident{}
	args := []any{actor.TenantID, scope.Cluster, scope.ClusterScoped, append([]string{}, scope.Namespaces...), append([]string{}, scope.Resources...)}
	err := persistence.WithTenantTx(ctx, h.Pool, actor.TenantID, func(tx pgx.Tx) error {
		var all, active int64
		if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE lifecycle_state='firing') FROM finding.records
   WHERE tenant_id=$1 AND cluster_uid=$2 AND (($3 AND namespace='') OR namespace=ANY($4::text[]))
   AND (cardinality($5::text[])=0 OR resource_canonical_id=ANY($5::text[]))`, args...).Scan(&all, &active); err != nil {
			return err
		}
		counts["finding"], counts["activeFinding"] = all, active
		var incidents, open, critical int64
		if err := tx.QueryRow(ctx, overviewIncidentsSQL+` SELECT count(*),count(*) FILTER(WHERE state='open'),count(*) FILTER(WHERE state='open' AND severity='critical') FROM visible`, args...).Scan(&incidents, &open, &critical); err != nil {
			return err
		}
		counts["incident"], counts["openIncident"], counts["criticalIncident"] = incidents, open, critical
		var running int64
		if err := tx.QueryRow(ctx, overviewIncidentsSQL+` SELECT count(*) FROM investigation.jobs j WHERE tenant_id=$1 AND state='running'
   AND (incident_id IS NULL OR EXISTS(SELECT 1 FROM visible WHERE incident_id=j.incident_id))
   AND requesting_subject=$6 AND effective_scope->>'clusterUid'=$2
   AND (NOT COALESCE((effective_scope->>'clusterScoped')::boolean,false) OR $3)
   AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(effective_scope->'namespaces') n WHERE NOT n=ANY($4::text[]))
   AND (cardinality($5::text[])=0 OR (jsonb_array_length(effective_scope->'resources')>0 AND NOT EXISTS(
    SELECT 1 FROM jsonb_array_elements_text(effective_scope->'resources') r WHERE NOT r=ANY($5::text[]))))`, append(args, actor.Subject)...).Scan(&running); err != nil {
			return err
		}
		counts["runningInvestigation"] = running
		var raw []byte
		if err := tx.QueryRow(ctx, overviewIncidentsSQL+` SELECT COALESCE(jsonb_agg(row),'[]'::jsonb) FROM (
   SELECT jsonb_build_object('incidentId',incident_id,'title',COALESCE((SELECT f.payload->>'normalizedSymptom' FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=visible.tenant_id AND l.incident_id=visible.incident_id ORDER BY f.finding_id LIMIT 1),resource_canonical_id),'resourceCanonicalId',resource_canonical_id,
    'state',state,'severity',severity,'startedAt',started_at,'durationSeconds',CASE WHEN started_at IS NOT NULL THEN GREATEST(0,EXTRACT(EPOCH FROM clock_timestamp()-started_at)) END,
    'rcaRevision',current_rca_revision,'rcaStatus',CASE WHEN current_rca_revision>0 THEN 'unverified' ELSE 'unresolved' END,'impact',NULL) AS row
   FROM visible WHERE state='open' AND severity IN('critical','warning')
   ORDER BY CASE severity WHEN 'critical' THEN 0 ELSE 1 END, started_at ASC NULLS LAST,incident_id LIMIT 5
  ) selected`, args...).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &items)
	})
	if err != nil {
		return nil, nil, err
	}
	// Reuse the RCA reader's current reference authorization. A frozen impact
	// summary is disclosed only when all its original inputs remain authorized.
	for i := range items {
		item := &items[i]
		if h.SP05 == nil || item.RCARevision < 1 {
			continue
		}
		rev, err := (rca.Repository{Pool: h.Pool}).Read(ctx, actor.TenantID, item.IncidentID, item.RCARevision)
		if err != nil || h.SP05.authorizeRCARead(ctx, actor, scope, rev) != nil {
			continue
		}
		item.RCAStatus = rev.Result.Status
		item.Impact = map[string]any{"direct": len(rev.Result.Impact.DirectlyAffected), "indirect": len(rev.Result.Impact.IndirectlyAffected), "dependencyOnly": len(rev.Result.Impact.DependencyOnly), "partial": rev.Result.Impact.Partial, "frozenRevision": item.RCARevision}
	}
	return counts, items, nil
}
