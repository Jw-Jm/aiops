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

func manualEvidence(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, scope graph.Scope, subject string, i Incident, c Change) error {
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
		e, err := (evidence.Repository{Pool: tx}).Get(ctx, tenant, id, scope)
		if err != nil {
			return graph.ErrScope
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_evidence_source($1,$2,$3)`, tenant, e.SourceRegistrationID, e.SourceRevision).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return graph.ErrScope
		}
		if _, err = (evidence.Repository{Pool: tx}).Get(ctx, tenant, id, scope); err != nil {
			return graph.ErrScope
		}
		var ns string
		if err := tx.QueryRow(ctx, `SELECT namespace FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id).Scan(&ns); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_operator_scope($1,$2,$3,$4)`, tenant, subject, i.ClusterUID, ns).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return graph.ErrScope
		}
		if c.State == "resolved" && (e.ResourceCanonicalID != i.ResourceCanonicalID || e.ReplayState != "archived_verified" || e.ArchiveRef == nil || !e.TimeReliable || e.ObservedTo.Before(time.Now().Add(-5*time.Minute)) || e.ObservedTo.After(time.Now().Add(time.Minute))) {
			return ErrTransition
		}
		if err := evidence.Protect(ctx, tx, tenant, id, uuid.MustParse(i.IncidentID), "incident", time.Now().Add(365*24*time.Hour), Active(c.State)); err != nil {
			return err
		}
	}
	return nil
}
