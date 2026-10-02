package resourcestore

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/datascope"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"slices"
)

// Source projection is narrowed before identity writes or Graph publication.
// A query grant cannot broaden the namespace/cluster scope of ingestion.
func (r Repository) FilterSnapshot(ctx context.Context, tenant, source, cluster string, s kubernetes.Snapshot) (kubernetes.Snapshot, error) {
	out := s
	out.Objects = nil
	tid, err := uuid.Parse(tenant)
	if err != nil {
		return out, resource.ErrIdentity
	}
	var mapping datascope.Mapping
	err = persistence.WithTenantTx(ctx, r.Pool, tid, func(tx pgx.Tx) error {
		var raw []byte
		var revision int64
		var backend string
		if err := tx.QueryRow(ctx, `SELECT s.data_scope_mapping,s.revision,s.backend_logical_id FROM platform.source_registrations s JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) JOIN platform.tenants t USING(tenant_id) WHERE s.tenant_id=$1 AND s.source_id=$2 AND s.source_type='kubernetes' AND s.status='active' AND c.cluster_uid=$3 AND c.status='active' AND t.status='active'`, tid, source, cluster).Scan(&raw, &revision, &backend); err != nil {
			return err
		}
		if r.ExpectedRevision < 1 || revision != r.ExpectedRevision || backend != r.BackendLogicalID || json.Unmarshal(raw, &mapping) != nil || mapping.NativeTenant != "" || !slices.Contains(mapping.Scopes["cluster"], cluster) || len(mapping.Scopes["namespace"]) == 0 {
			return resource.ErrIdentity
		}
		for dimension := range mapping.Scopes {
			if dimension != "cluster" && dimension != "namespace" {
				return resource.ErrIdentity
			}
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	for _, o := range s.Objects {
		if ns := o.GetNamespace(); ns != "" && !slices.Contains(mapping.Scopes["namespace"], ns) {
			continue
		}
		allowed := true
		for key, value := range mapping.RequiredLabels {
			if o.GetLabels()[key] != value {
				allowed = false
				break
			}
		}
		if allowed {
			out.Objects = append(out.Objects, o)
		}
	}
	return out, nil
}
