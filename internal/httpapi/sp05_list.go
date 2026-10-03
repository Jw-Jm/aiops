package httpapi

import (
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"time"
)

func sp05ListSQL(kind string, tenant uuid.UUID, cluster, after string, scope graph.Scope, limit int, r *http.Request) (string, []any, error) {
	q := r.URL.Query()
	if after != "" {
		if _, err := uuid.Parse(after); err != nil {
			return "", nil, finding.ErrInvalid
		}
	}
	var from, to *time.Time
	clockNames := []string{"observedFrom", "observedTo"}
	if kind == "incidents" {
		clockNames = []string{"openedFrom", "openedTo"}
	}
	for n, key := range clockNames {
		if value := q.Get(key); value != "" {
			clock, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return "", nil, finding.ErrInvalid
			}
			if n == 0 {
				from = &clock
			} else {
				to = &clock
			}
		}
	}
	if from != nil && to != nil && from.After(*to) {
		return "", nil, finding.ErrInvalid
	}
	if value := q.Get("incidentId"); value != "" {
		if _, err := uuid.Parse(value); err != nil {
			return "", nil, finding.ErrInvalid
		}
	}
	table, id, state, payload, clock := "finding.records", "finding_id", "lifecycle_state", "t.payload", "t.observed_at"
	args := []any{tenant, cluster, after, scope.ClusterScoped, scope.Namespaces, q.Get("state"), q.Get("resourceCanonicalId"), limit + 1, q.Get("severity"), from, to}
	extra := ` AND ($9='' OR t.payload->>'severity'=$9) AND ($12='' OR EXISTS(SELECT 1 FROM incident.finding_links l WHERE l.tenant_id=t.tenant_id AND l.finding_id=t.finding_id AND l.incident_id::text=$12)) AND ($13='' OR EXISTS(SELECT 1 FROM platform.source_registrations s WHERE s.tenant_id=t.tenant_id AND s.source_id=t.source_id AND s.source_type=$13))`
	if kind == "incidents" {
		table, id, state, clock = "incident.records", "incident_id", "state", "t.created_at"
		severity := `(SELECT COALESCE((array_agg(f.payload->>'severity' ORDER BY CASE f.payload->>'severity' WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 WHEN 'info' THEN 2 ELSE 3 END))[1],'unknown') FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=t.tenant_id AND l.incident_id=t.incident_id)`
		payload = `jsonb_build_object('schemaVersion','incident/v2','tenantId',t.tenant_id,'incidentId',t.incident_id,'state',t.state,'revision',t.revision,'severity',` + severity + `,'currentRcaRevision',t.current_rca_revision,'resourceCanonicalId',t.resource_canonical_id,'namespace',t.namespace,'clusterUid',t.cluster_uid,'fingerprint',t.fingerprint,'policyVersion',t.policy_version,'createdAt',t.created_at,'updatedAt',t.updated_at)`
		extra = ` AND ($9='' OR ` + severity + `=$9) AND ($12='' OR COALESCE((SELECT v.result->'result'->>'status' FROM incident.rca_revisions v WHERE v.tenant_id=t.tenant_id AND v.incident_id=t.incident_id AND v.revision=t.current_rca_revision),'unresolved')=$12) AND NOT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=t.tenant_id AND l.incident_id=t.incident_id AND NOT (($4 AND f.namespace='') OR f.namespace=ANY($5::text[])))`
		args = append(args, q.Get("rcaStatus"))
	} else {
		args = append(args, q.Get("incidentId"), q.Get("sourceSystem"))
	}
	sql := fmt.Sprintf(`SELECT t.%s,%s FROM %s t WHERE t.tenant_id=$1 AND t.cluster_uid=$2 AND ($3='' OR t.%s::text>$3) AND (($4 AND t.namespace='') OR t.namespace=ANY($5::text[])) AND ($6='' OR t.%s=$6) AND ($7='' OR t.resource_canonical_id=$7) AND ($10::timestamptz IS NULL OR %s>=$10) AND ($11::timestamptz IS NULL OR %s<=$11)%s ORDER BY t.%s LIMIT $8`, id, payload, table, id, state, clock, clock, extra, id)
	return sql, args, nil
}
