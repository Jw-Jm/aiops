package finding

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/source"
	"time"
)

func verifyEvidence(ctx context.Context, tx pgx.Tx, b source.BoundSourceContext, e Envelope) ([]uuid.UUID, error) {
	scope := graph.Scope{Tenant: b.TenantID.String(), Cluster: b.ClusterUID, ClusterScoped: e.Namespace == "", Namespaces: []string{e.Namespace}}
	if actor, ok := auth.RequestContextFromContext(ctx); ok {
		var err error
		scope, err = (graph.Authorization{Pool: tx}).Effective(ctx, b.TenantID.String(), actor.Subject, b.ClusterUID)
		if err != nil {
			return nil, ErrUnauthorized
		}
	}
	refs := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	for _, raw := range e.EvidenceRefs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, ErrInvalid
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		metadata, err := (evidence.Repository{Pool: tx}).Get(ctx, b.TenantID, id, scope)
		if err != nil {
			return nil, ErrUnauthorized
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_evidence_source($1,$2,$3)`, b.TenantID, metadata.SourceRegistrationID, metadata.SourceRevision).Scan(&valid); err != nil {
			return nil, err
		}
		if !valid {
			return nil, ErrUnauthorized
		}
		// Re-evaluate mappings after acquiring the source revocation fence.
		if _, err = (evidence.Repository{Pool: tx}).Get(ctx, b.TenantID, id, scope); err != nil {
			return nil, ErrUnauthorized
		}
		if actor, ok := auth.RequestContextFromContext(ctx); ok {
			var ns string
			if err = tx.QueryRow(ctx, `SELECT namespace FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2`, b.TenantID, id).Scan(&ns); err != nil {
				return nil, err
			}
			if err = tx.QueryRow(ctx, `SELECT platform.sp05_lock_operator_scope($1,$2,$3,$4)`, b.TenantID, actor.Subject, b.ClusterUID, ns).Scan(&valid); err != nil {
				return nil, err
			}
			if !valid {
				return nil, ErrUnauthorized
			}
		}
		refs = append(refs, id)
	}
	return refs, nil
}
func retainEvidence(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string, refs []uuid.UUID) error {
	for _, ref := range refs {
		if _, err := tx.Exec(ctx, `INSERT INTO finding.evidence_refs VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, id, ref); err != nil {
			return err
		}
		if err := evidence.Protect(ctx, tx, tenant, ref, uuid.MustParse(id), "finding", time.Now().Add(365*24*time.Hour), false); err != nil {
			return err
		}
	}
	return nil
}
