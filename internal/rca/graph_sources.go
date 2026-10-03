package rca

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"slices"
	"strings"
)

// The caller holds an incident transaction. Narrow SECURITY DEFINER share locks
// fence revoke/rotate without granting runtime UPDATE on source registrations.
func CheckGraphSources(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, sources []graph.SourceAuthority, result graph.Result) error {
	ordered := append([]graph.SourceAuthority{}, sources...)
	slices.SortFunc(ordered, func(a, b graph.SourceAuthority) int {
		return strings.Compare(a.SourceRegistrationID, b.SourceRegistrationID)
	})
	known := map[string]bool{}
	for _, source := range ordered {
		if known[source.SourceRegistrationID] || source.Revision < 1 || source.ScopeDigest == "" {
			return ErrStale
		}
		known[source.SourceRegistrationID] = true
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_evidence_source($1,$2,$3)`, tenant, source.SourceRegistrationID, source.Revision).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrStale
		}
		var raw []byte
		binding := evidence.Binding{Tenant: tenant.String(), SourceID: source.SourceRegistrationID, Revision: source.Revision}
		if err := tx.QueryRow(ctx, `SELECT source_type,backend_logical_id,data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND revision=$3 AND status='active'`, tenant, source.SourceRegistrationID, source.Revision).Scan(&binding.SourceType, &binding.BackendLogicalID, &raw); err != nil {
			return ErrStale
		}
		if json.Unmarshal(raw, &binding.ScopeMapping) != nil || evidence.BindingScopeDigest(binding) != source.ScopeDigest {
			return ErrStale
		}
	}
	for _, node := range result.Nodes {
		for _, id := range node.SourceRefs {
			if !known[id] {
				return ErrStale
			}
		}
	}
	for _, edge := range result.Edges {
		if !known[edge.Provenance.SourceRegistrationID] {
			return ErrStale
		}
	}
	return nil
}

// Freeze only sources actually contributing nodes or paths to this bounded query.
func SelectGraphSources(known []graph.SourceAuthority, result graph.Result) []graph.SourceAuthority {
	used := map[string]bool{}
	for _, node := range result.Nodes {
		for _, id := range node.SourceRefs {
			used[id] = true
		}
	}
	for _, edge := range result.Edges {
		used[edge.Provenance.SourceRegistrationID] = true
	}
	out := []graph.SourceAuthority{}
	for _, source := range known {
		if used[source.SourceRegistrationID] {
			out = append(out, source)
		}
	}
	slices.SortFunc(out, func(a, b graph.SourceAuthority) int {
		return strings.Compare(a.SourceRegistrationID, b.SourceRegistrationID)
	})
	return out
}
