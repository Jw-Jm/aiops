package evidence

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
)

// RegisteredBinding freezes the operator's mapping while preserving the
// runtime revision/backend supplied in configuration. It grants no query proof.
func (r Repository) RegisteredBinding(ctx context.Context, expected Binding) (Binding, error) {
	var raw []byte
	err := persistence.WithTenantTx(ctx, r.Pool, uuid.MustParse(expected.Tenant), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2`, expected.Tenant, expected.SourceID).Scan(&raw)
	})
	if err != nil {
		return expected, err
	}
	if json.Unmarshal(raw, &expected.ScopeMapping) != nil {
		return expected, ErrScopeUnverified
	}
	return expected, r.CheckBinding(ctx, expected)
}

func evidenceSourceDigest(ctx context.Context, tx pgx.Tx, e Evidence) (string, error) {
	var mapping []byte
	err := tx.QueryRow(ctx, `SELECT s.data_scope_mapping FROM platform.source_registrations s JOIN platform.tenants t USING(tenant_id) LEFT JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE s.tenant_id=$1 AND s.source_id=$2 AND s.revision=$3 AND s.backend_logical_id=$4 AND s.source_type=$5 AND s.status='active' AND t.status='active' AND (c.cluster_id IS NULL OR c.status='active')`, e.TenantID, e.SourceRegistrationID, e.SourceRevision, e.BackendLogicalID, e.SourceSystem).Scan(&mapping)
	if err != nil {
		return "", ErrScopeUnverified
	}
	var canonical any
	if json.Unmarshal(mapping, &canonical) != nil {
		return "", ErrScopeUnverified
	}
	raw, _ := json.Marshal(struct {
		Revision      int64
		Backend, Type string
		Mapping       any
	}{e.SourceRevision, e.BackendLogicalID, e.SourceSystem, canonical})
	return Digest(raw), nil
}

// BindingScopeDigest is computed at collection/query time, before a fact can
// enter the archive. It is immutable provenance, not an archive-time grant.
func BindingScopeDigest(b Binding) string {
	encoded, _ := json.Marshal(b.ScopeMapping)
	var mapping any
	if json.Unmarshal(encoded, &mapping) != nil {
		return ""
	}
	raw, _ := json.Marshal(struct {
		Revision      int64
		Backend, Type string
		Mapping       any
	}{b.Revision, b.BackendLogicalID, b.SourceType, mapping})
	return Digest(raw)
}
