package incident

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"time"
)

func (s Service) Split(ctx context.Context, scope graph.Scope, subject, id, reason string, expected int64, refs []string) (Incident, error) {
	var out Incident
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil || reason == "" || len(refs) == 0 || len(refs) > 100 {
		return out, ErrTransition
	}
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, err := Load(ctx, tx, tenant, id, false)
		if err != nil {
			return err
		}
		if err := LockSubject(ctx, tx, tenant, i.Fingerprint); err != nil {
			return err
		}
		i, err = Load(ctx, tx, tenant, id, true)
		if err != nil {
			return err
		}
		if err := Authorize(ctx, tx, scope, subject, i); err != nil {
			return err
		}
		if err := authorizeLinked(ctx, tx, scope, subject, i); err != nil {
			return err
		}
		if i.Revision != expected {
			return ErrRevision
		}
		if !Active(i.State) {
			return ErrTransition
		}
		var total int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM incident.finding_links WHERE tenant_id=$1 AND incident_id=$2`, tenant, id).Scan(&total); err != nil {
			return err
		}
		if len(refs) >= total {
			return ErrTransition
		}
		seen := map[string]bool{}
		for _, ref := range refs {
			if seen[ref] {
				return ErrTransition
			}
			seen[ref] = true
			var linked bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM incident.finding_links WHERE tenant_id=$1 AND incident_id=$2 AND finding_id=$3)`, tenant, id, ref).Scan(&linked); err != nil {
				return err
			}
			if !linked {
				return ErrTransition
			}
			f, err := finding.Load(ctx, tx, tenant, ref)
			if err != nil {
				return err
			}
			if !scope.Allows(f.ResourceCanonicalID, f.Namespace) {
				return graph.ErrScope
			}
		}
		first, err := finding.Load(ctx, tx, tenant, refs[0])
		if err != nil {
			return err
		}
		newID := uuid.Must(uuid.NewV7()).String()
		fp := finding.Hash([]string{i.Fingerprint, "manual-split", newID})
		now := time.Now().UTC()
		out = Incident{SchemaVersion: "incident/v2", TenantID: tenant.String(), IncidentID: newID, State: "open", Revision: 1, Fingerprint: fp, PolicyVersion: i.PolicyVersion, ClusterUID: i.ClusterUID, ResourceCanonicalID: first.ResourceCanonicalID, Namespace: first.Namespace, StartedAt: first.StartsAt, LastObservedAt: first.ObservedAt, CreatedAt: now, UpdatedAt: now}
		if _, err := tx.Exec(ctx, `INSERT INTO incident.correlation_subjects VALUES($1,$2)`, tenant, fp); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO incident.records(tenant_id,incident_id,state,revision,fingerprint,policy_version,cluster_uid,resource_canonical_id,namespace,started_at,last_observed_at) VALUES($1,$2,'open',1,$3,$4,$5,$6,$7,$8,$9)`, tenant, newID, fp, i.PolicyVersion, i.ClusterUID, out.ResourceCanonicalID, out.Namespace, out.StartedAt, out.LastObservedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE incident.finding_links SET incident_id=$4 WHERE tenant_id=$1 AND incident_id=$2 AND finding_id=ANY($3::uuid[])`, tenant, id, refs, newID); err != nil {
			return err
		}
		i.Revision++
		i.RecoveryKnownAt = nil
		for _, item := range []Incident{i, out} {
			if err := recomputeRecovery(ctx, tx, tenant, &item); err != nil {
				return err
			}
			if item.IncidentID == out.IncidentID {
				// The child is still being created in this transaction. Initialize
				// its clock without treating initial revision 1 as a subsequent CAS.
				if _, err := tx.Exec(ctx, `UPDATE incident.records SET recovery_known_at=$3 WHERE tenant_id=$1 AND incident_id=$2 AND revision=1`, tenant, item.IncidentID, item.RecoveryKnownAt); err != nil {
					return err
				}
				out = item
			} else if err := Save(ctx, tx, item); err != nil {
				return err
			}
			if err := Append(ctx, tx, item, "split", subject, reason, map[string]any{"sourceId": id, "targetId": newID, "findingIds": refs}); err != nil {
				return err
			}
		}
		if err := protectLinked(ctx, tx, tenant, newID, true); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(id), EntityKind: "incident", EventType: "incident.split", Subject: subject, Payload: map[string]any{"targetId": newID, "findingIds": refs, "reason": reason, "policyVersion": i.PolicyVersion}})
		return err
	})
	return out, err
}
