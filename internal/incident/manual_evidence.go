package incident

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"slices"
	"time"
)

// manualEvidence keeps the retained Evidence of a manual change inside the same
// authority that already fenced the Incident. The source revision, trusted
// observation window, archive verification and retention checks are unchanged;
// only the operator lock is now selected by the trusted identity source instead
// of always reading platform.role_bindings.
func manualEvidence(ctx context.Context, tx pgx.Tx, authority operationAuthority, i Incident, c Change) error {
	if len(c.EvidenceRefs) > 64 {
		return ErrTransition
	}
	refs := append([]string{}, c.EvidenceRefs...)
	slices.Sort(refs)
	refs = slices.Compact(refs)
	for _, ref := range refs {
		id, err := uuid.Parse(ref)
		if err != nil {
			return ErrTransition
		}
		e, err := (evidence.Repository{Pool: tx}).Get(ctx, authority.tenant, id, authority.scope)
		if err != nil {
			return graph.ErrScope
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_evidence_source($1,$2,$3)`, authority.tenant, e.SourceRegistrationID, e.SourceRevision).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return graph.ErrScope
		}
		if _, err = (evidence.Repository{Pool: tx}).Get(ctx, authority.tenant, id, authority.scope); err != nil {
			return graph.ErrScope
		}
		var ns string
		if err := tx.QueryRow(ctx, `SELECT namespace FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2`, authority.tenant, id).Scan(&ns); err != nil {
			return err
		}
		// The Evidence row is a related resource: the current authority must cover
		// its exact cluster/namespace/resource, and the legacy fence re-locks its
		// binding. A native request is covered by the rebuilt grant proven above.
		if err := authority.authorizeResource(ctx, tx, i.ClusterUID, e.ResourceCanonicalID, ns); err != nil {
			return err
		}
		if c.State == "resolved" && (e.ResourceCanonicalID != i.ResourceCanonicalID || e.ReplayState != "archived_verified" || e.ArchiveRef == nil || !e.TimeReliable || e.ObservedTo.Before(time.Now().Add(-5*time.Minute)) || e.ObservedTo.After(time.Now().Add(time.Minute))) {
			return ErrTransition
		}
		if err := evidence.Protect(ctx, tx, authority.tenant, id, uuid.MustParse(i.IncidentID), "incident", time.Now().Add(365*24*time.Hour), Active(c.State)); err != nil {
			return err
		}
	}
	return nil
}
