package resourcestore

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
)

// ReconcileNativeSnapshot restores deletion knowledge lost across Worker restarts.
// Only a healthy, complete, unfiltered native list can prove an identity disappeared.
func (r Repository) ReconcileNativeSnapshot(ctx context.Context, tenant, source, cluster string, s kubernetes.Snapshot) error {
	if s.ObservationOnly || s.State.LastListCompletedAt.IsZero() || s.State.LastError != "" {
		return nil
	}
	tid, err := uuid.Parse(tenant)
	if err != nil {
		return resource.ErrIdentity
	}
	sid, err := uuid.Parse(source)
	if err != nil {
		return resource.ErrIdentity
	}
	present := map[string]bool{}
	for _, o := range s.Objects {
		present[string(o.GetUID())] = true
	}
	return persistence.WithTenantTx(ctx, r.Pool, tid, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,41))`, tenant); err != nil {
			return err
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.source_registrations s JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) JOIN platform.tenants t USING(tenant_id) WHERE s.tenant_id=$1 AND s.source_id=$2 AND s.source_type='kubernetes' AND s.status='active' AND c.status='active' AND t.status='active' AND c.cluster_uid=$3 AND s.revision=$4 AND s.backend_logical_id=$5)`, tid, sid, cluster, r.ExpectedRevision, r.BackendLogicalID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return resource.ErrIdentity
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT e.canonical_id FROM platform.resource_entities e JOIN platform.resource_aliases a USING(tenant_id,canonical_id) WHERE e.tenant_id=$1 AND a.source_id=$2 AND a.scope=$3 AND a.alias_kind='uid' AND e.deleted_at IS NULL AND e.observed_at<=$4`, tid, sid, cluster, s.State.LastListCompletedAt)
		if err != nil {
			return err
		}
		missing := []string{}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			id, err := resource.ParseCanonicalID(raw)
			if err != nil {
				rows.Close()
				return err
			}
			group := id.APIGroup
			if group == "core" {
				group = ""
			}
			gvr, _ := meta.UnsafeGuessKindToResource(schema.GroupVersionKind{Group: group, Version: s.GVR.Version, Kind: id.Kind})
			if id.Domain == "k8s" && id.Scope == cluster && gvr.Group == s.GVR.Group && gvr.Resource == s.GVR.Resource && !present[id.StableID] {
				missing = append(missing, raw)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range missing {
			if _, err := tx.Exec(ctx, `UPDATE platform.resource_entities SET deleted_at=clock_timestamp() WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL AND observed_at<=$3`, tid, id, s.State.LastListCompletedAt); err != nil {
				return err
			}
		}
		return nil
	})
}
