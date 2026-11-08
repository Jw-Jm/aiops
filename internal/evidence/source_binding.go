package evidence

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"regexp"
	"slices"
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

func historicalArchiveBindingTx(ctx context.Context, tx pgx.Tx, e Evidence, namespace string) bool {
	var current Binding
	var mapping []byte
	current.Tenant, current.SourceID = e.TenantID, e.SourceRegistrationID
	err := tx.QueryRow(ctx, `SELECT s.revision,s.source_type,s.backend_logical_id,s.data_scope_mapping FROM platform.source_registrations s JOIN platform.tenants t USING(tenant_id) LEFT JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE s.tenant_id=$1 AND s.source_id=$2 AND s.status='active' AND t.status='active' AND (c.cluster_id IS NULL OR c.status='active')`, e.TenantID, e.SourceRegistrationID).Scan(&current.Revision, &current.SourceType, &current.BackendLogicalID, &mapping)
	return err == nil && json.Unmarshal(mapping, &current.ScopeMapping) == nil && historicalArchiveAllowed(e, namespace, current)
}

// Historical reads retain capture provenance; current source and caller scope
// still fence access. This never qualifies a new query or an unverified archive.
func historicalArchiveAllowed(e Evidence, namespace string, current Binding) bool {
	canonical, err := resource.ParseCanonicalID(e.ResourceCanonicalID)
	ref := e.ArchiveRef
	if err != nil || canonical.Domain != "k8s" || canonical.Tenant != e.TenantID || current.Tenant != e.TenantID || current.SourceID != e.SourceRegistrationID || current.SourceType != e.SourceSystem || current.BackendLogicalID != e.BackendLogicalID || e.SourceRevision < 1 || current.Revision <= e.SourceRevision || ref == nil || e.ReplayState != "archived_verified" {
		return false
	}
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(e.SourceScopeDigest) || ref.PlaintextDigest != e.ContentDigest || ref.Object.TenantID.String() != e.TenantID || ref.Object.ObjectID.String() != e.EvidenceID || ref.Object.Category != "evidence" || ref.Object.VersionID == "" || ref.EncryptionKeyVersion == "" || ref.CiphertextDigest == "" || ref.Object.Digest != ref.CiphertextDigest {
		return false
	}
	mapping := current.ScopeMapping
	if mapping.NativeTenant != "" || mapping.RequiredLabels["tenant"] != "" && mapping.RequiredLabels["tenant"] != e.TenantID || !slices.Contains(mapping.Scopes["cluster"], canonical.Scope) || namespace != "" && !slices.Contains(mapping.Scopes["namespace"], namespace) || e.EffectiveScope.Tenant != e.TenantID || e.EffectiveScope.Cluster != canonical.Scope || !e.EffectiveScope.Allows(e.ResourceCanonicalID, namespace) {
		return false
	}
	for dimension := range mapping.Scopes {
		if dimension != "cluster" && dimension != "namespace" {
			return false
		}
	}
	return true
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
