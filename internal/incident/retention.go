package incident

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"time"
)

// Retention changes use the SP04 lock and invalidation path, including merge,
// split and recovery. Direct reference-table updates can race backend cleanup.
func protectLinked(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string, active bool) error {
	rows, err := tx.Query(ctx, `SELECT DISTINCT e.evidence_id FROM incident.finding_links l JOIN finding.evidence_refs e USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2 ORDER BY e.evidence_id`, tenant, id)
	if err != nil {
		return err
	}
	refs := []uuid.UUID{}
	for rows.Next() {
		var ref uuid.UUID
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if err := evidence.Protect(ctx, tx, tenant, ref, uuid.MustParse(id), "incident", time.Now().Add(365*24*time.Hour), active); err != nil {
			return err
		}
	}
	return nil
}
func releaseReferences(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string) error {
	rows, err := tx.Query(ctx, `SELECT evidence_id,retain_until FROM platform.evidence_retention_references WHERE tenant_id=$1 AND reference_kind='incident' AND reference_id=$2 ORDER BY evidence_id`, tenant, id)
	if err != nil {
		return err
	}
	type ref struct {
		id    uuid.UUID
		until time.Time
	}
	refs := []ref{}
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.id, &r.until); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range refs {
		if err := evidence.Protect(ctx, tx, tenant, r.id, uuid.MustParse(id), "incident", r.until, false); err != nil {
			return err
		}
	}
	return nil
}
func authorizeLinked(ctx context.Context, tx pgx.Tx, authority operationAuthority, i Incident) error {
	rows, err := tx.Query(ctx, `SELECT f.resource_canonical_id,f.namespace FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2 ORDER BY f.finding_id`, i.TenantID, i.IncidentID)
	if err != nil {
		return err
	}
	type linked struct {
		resource, namespace string
	}
	items := []linked{}
	for rows.Next() {
		var f linked
		if err := rows.Scan(&f.resource, &f.namespace); err != nil {
			rows.Close()
			return err
		}
		items = append(items, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Every linked resource must still be inside the authority proven for this
	// transaction. A historical Finding whose namespace or cluster left the grant
	// blocks the whole mutation, exactly like the primary Incident.
	for _, f := range items {
		if err := authority.authorizeResource(ctx, tx, i.ClusterUID, f.resource, f.namespace); err != nil {
			return err
		}
	}
	return nil
}

// AuthorizeAll fences the primary and every linked resource on mutation replay.
func AuthorizeAll(ctx context.Context, tx pgx.Tx, scope graph.Scope, subject string, i Incident) error {
	authority, err := authorizeTx(ctx, tx, scope, subject, i)
	if err != nil {
		return err
	}
	return authorizeLinked(ctx, tx, authority, i)
}
