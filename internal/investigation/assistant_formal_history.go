package investigation

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"slices"
)

// Historical reads use verified capture provenance plus current source scope.
// Live RCA commits continue to require rca.CheckGraphSources' exact revision.
func authorizeArchivedGraphTx(ctx context.Context, tx pgx.Tx, j Job, rev rca.Revision, verified []evidence.Evidence) error {
	known := map[string]bool{}
	for _, source := range rev.InputManifest.GraphSources {
		if known[source.SourceRegistrationID] {
			return ErrDenied
		}
		known[source.SourceRegistrationID] = true
		var current evidence.Binding
		var mapping []byte
		current.Tenant, current.SourceID = j.TenantID.String(), source.SourceRegistrationID
		if tx.QueryRow(ctx, `SELECT revision,source_type,backend_logical_id,data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND status='active'`, j.TenantID, source.SourceRegistrationID).Scan(&current.Revision, &current.SourceType, &current.BackendLogicalID, &mapping) != nil || json.Unmarshal(mapping, &current.ScopeMapping) != nil {
			return ErrDenied
		}
		var locked bool
		if tx.QueryRow(ctx, `SELECT platform.sp05_lock_evidence_source($1,$2,$3)`, j.TenantID, source.SourceRegistrationID, current.Revision).Scan(&locked) != nil || !locked || !archivedGraphSourceAllowed(source, current, rev.InputManifest.Graph, verified) {
			return ErrDenied
		}
	}
	for _, node := range rev.InputManifest.Graph.Nodes {
		for _, id := range node.SourceRefs {
			if !known[id] {
				return ErrDenied
			}
		}
	}
	for _, edge := range rev.InputManifest.Graph.Edges {
		if !known[edge.Provenance.SourceRegistrationID] {
			return ErrDenied
		}
	}
	return nil
}

func archivedGraphSourceAllowed(source graph.SourceAuthority, current evidence.Binding, result graph.Result, verified []evidence.Evidence) bool {
	if source.Revision < 1 || current.Revision < source.Revision || source.ScopeDigest == "" {
		return false
	}
	proven := false
	for _, e := range verified {
		if e.TenantID == current.Tenant && e.SourceRegistrationID == current.SourceID && e.SourceRevision == source.Revision && e.SourceScopeDigest == source.ScopeDigest && e.SourceSystem == current.SourceType && e.BackendLogicalID == current.BackendLogicalID && e.ReplayState == "archived_verified" && e.ArchiveRef != nil {
			proven = true
		}
	}
	if !proven || (current.Revision == source.Revision && evidence.BindingScopeDigest(current) != source.ScopeDigest) {
		return false
	}
	for _, node := range result.Nodes {
		if !slices.Contains(node.SourceRefs, source.SourceRegistrationID) {
			continue
		}
		id, err := resource.ParseCanonicalID(node.CanonicalID)
		if err != nil || id.Tenant != current.Tenant || !slices.Contains(current.ScopeMapping.Scopes["cluster"], id.Scope) || (node.Namespace != "" && !slices.Contains(current.ScopeMapping.Scopes["namespace"], node.Namespace)) {
			return false
		}
	}
	return true
}
