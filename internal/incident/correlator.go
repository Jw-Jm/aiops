package incident

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/finding"
	"time"
)

func LockSubject(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, fingerprint string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO incident.correlation_subjects VALUES($1,$2) ON CONFLICT DO NOTHING`, tenant, fingerprint); err != nil {
		return err
	}
	var locked string
	return tx.QueryRow(ctx, `SELECT fingerprint FROM incident.correlation_subjects WHERE tenant_id=$1 AND fingerprint=$2 FOR UPDATE`, tenant, fingerprint).Scan(&locked)
}

// Consume handles a replayed or gap event by re-reading the durable aggregate.
// No assumption about process or delivery order enters the resulting state.
func Consume(ctx context.Context, tx pgx.Tx, d finding.Delivery) error {
	var supplied finding.Finding
	if json.Unmarshal(d.Payload, &supplied) != nil || supplied.FindingID != d.FindingID || supplied.TenantID != d.TenantID.String() || supplied.AggregateRevision != d.Revision {
		return finding.ErrInvalid
	}
	var seen bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM incident.inbox WHERE tenant_id=$1 AND consumer_name='correlation/v1' AND event_id=$2)`, d.TenantID, d.EventID).Scan(&seen); err != nil {
		return err
	}
	if seen {
		return nil
	}
	f, err := finding.Load(ctx, tx, d.TenantID, d.FindingID)
	if err != nil {
		return err
	}
	if f.AggregateRevision < d.Revision {
		return finding.ErrInvalid
	}
	fingerprint := Fingerprint(f, PolicyVersion)
	// Lock existing subject of the occurrence, preserving policy/merge/split decisions.
	var linked, linkedFingerprint string
	err = tx.QueryRow(ctx, `SELECT l.incident_id,i.fingerprint FROM incident.finding_links l JOIN incident.records i USING(tenant_id,incident_id) WHERE l.tenant_id=$1 AND l.finding_id=$2`, d.TenantID, f.FindingID).Scan(&linked, &linkedFingerprint)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if linked != "" {
		fingerprint = linkedFingerprint
	}
	if err := LockSubject(ctx, tx, d.TenantID, fingerprint); err != nil {
		return err
	}
	// Re-read after subject serialization to catch a simultaneous first link.
	err = tx.QueryRow(ctx, `SELECT incident_id FROM incident.finding_links WHERE tenant_id=$1 AND finding_id=$2`, d.TenantID, f.FindingID).Scan(&linked)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var applied int64
	err = tx.QueryRow(ctx, `SELECT aggregate_revision FROM incident.applied_revisions WHERE tenant_id=$1 AND finding_id=$2`, d.TenantID, f.FindingID).Scan(&applied)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	disposition := "applied"
	if applied >= f.AggregateRevision {
		disposition = "stale"
	} else if f.AggregateRevision > d.Revision {
		disposition = "resynchronized"
	}
	var i Incident
	if disposition != "stale" {
		if linked != "" {
			i, err = Load(ctx, tx, d.TenantID, linked, true)
			if err != nil {
				return err
			}
		} else if f.State == "firing" {
			rows, err := tx.Query(ctx, selectIncident+` WHERE tenant_id=$1 AND fingerprint=$2 ORDER BY created_at DESC,incident_id DESC FOR UPDATE`, d.TenantID, fingerprint)
			if err != nil {
				return err
			}
			candidates := []Incident{}
			for rows.Next() {
				candidate, err := scanIncident(rows)
				if err != nil {
					rows.Close()
					return err
				}
				candidates = append(candidates, candidate)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, c := range candidates {
				if Active(c.State) && f.TimeReliable && !f.ObservedAt.Before(c.StartedAt.Add(-CorrelationWindow)) && f.ObservedAt.Sub(c.LastObservedAt) <= CorrelationWindow {
					i = c
					break
				}
				if c.State == "resolved" && c.ResolvedAt != nil && f.TimeReliable && f.StartsAt.After(*c.ResolvedAt) && f.StartsAt.Sub(*c.ResolvedAt) <= ReopenWindow {
					i = c
					i.State = "open"
					i.ResolvedAt = nil
					break
				}
			}
			if i.IncidentID == "" {
				var now time.Time
				if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
					return err
				}
				i = Incident{SchemaVersion: "incident/v2", TenantID: d.TenantID.String(), IncidentID: uuid.Must(uuid.NewV7()).String(), State: "open", Revision: 1, Fingerprint: fingerprint, PolicyVersion: PolicyVersion, ClusterUID: f.ClusterUID, ResourceCanonicalID: f.ResourceCanonicalID, Namespace: f.Namespace, StartedAt: f.StartsAt, LastObservedAt: f.ObservedAt, CreatedAt: now, UpdatedAt: now}
				_, err = tx.Exec(ctx, `INSERT INTO incident.records(tenant_id,incident_id,state,revision,fingerprint,policy_version,cluster_uid,resource_canonical_id,namespace,created_at,updated_at,started_at,last_observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,$11,$12)`, d.TenantID, i.IncidentID, i.State, i.Revision, i.Fingerprint, i.PolicyVersion, i.ClusterUID, i.ResourceCanonicalID, i.Namespace, now, f.StartsAt, f.ObservedAt)
				if err != nil {
					return err
				}
			}
		}
		if i.IncidentID != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO incident.finding_links(tenant_id,incident_id,finding_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, d.TenantID, i.IncidentID, f.FindingID); err != nil {
				return err
			}
			if err := protectLinked(ctx, tx, d.TenantID, i.IncidentID, Active(i.State)); err != nil {
				return err
			}
			var allResolved bool
			if err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2 AND f.lifecycle_state IS DISTINCT FROM 'resolved')`, d.TenantID, i.IncidentID).Scan(&allResolved); err != nil {
				return err
			}
			if f.State == "firing" {
				i.RecoveryKnownAt = nil
			} else if allResolved && i.RecoveryKnownAt == nil {
				var now time.Time
				if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
					return err
				}
				i.RecoveryKnownAt = &now
			}
			if f.ObservedAt.After(i.LastObservedAt) {
				i.LastObservedAt = f.ObservedAt
			}
			i.Revision++
			if err := Save(ctx, tx, i); err != nil {
				return err
			}
			if err := Append(ctx, tx, i, "finding", "correlation/v1", disposition, map[string]any{"findingId": f.FindingID, "aggregateRevision": f.AggregateRevision, "deliveredRevision": d.Revision, "eventId": d.EventID}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO incident.applied_revisions VALUES($1,$2,$3) ON CONFLICT(tenant_id,finding_id) DO UPDATE SET aggregate_revision=GREATEST(applied_revisions.aggregate_revision,EXCLUDED.aggregate_revision)`, d.TenantID, f.FindingID, f.AggregateRevision); err != nil {
			return err
		}
	}
	var incidentID any
	if i.IncidentID != "" {
		incidentID = i.IncidentID
	} else if linked != "" {
		incidentID = linked
	}
	_, err = tx.Exec(ctx, `INSERT INTO incident.inbox VALUES($1,'correlation/v1',$2,$3,$4,$5,$6)`, d.TenantID, d.EventID, f.FindingID, f.AggregateRevision, incidentID, disposition)
	return err
}

const selectIncident = `SELECT tenant_id,incident_id,state,revision,COALESCE(fingerprint,''),COALESCE(policy_version,''),COALESCE(cluster_uid,''),COALESCE(resource_canonical_id,''),namespace,resolved_at,recovery_known_at,current_rca_revision,suppressed_until,created_at,updated_at,COALESCE(started_at,created_at),COALESCE(last_observed_at,updated_at) FROM incident.records`

type scanner interface{ Scan(...any) error }

func scanIncident(row scanner) (Incident, error) {
	var i Incident
	err := row.Scan(&i.TenantID, &i.IncidentID, &i.State, &i.Revision, &i.Fingerprint, &i.PolicyVersion, &i.ClusterUID, &i.ResourceCanonicalID, &i.Namespace, &i.ResolvedAt, &i.RecoveryKnownAt, &i.CurrentRCARevision, &i.SuppressedUntil, &i.CreatedAt, &i.UpdatedAt, &i.StartedAt, &i.LastObservedAt)
	i.SchemaVersion = "incident/v2"
	return i, err
}
func Load(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string, lock bool) (Incident, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	return scanIncident(tx.QueryRow(ctx, selectIncident+` WHERE tenant_id=$1 AND incident_id=$2`+suffix, tenant, id))
}
func Save(ctx context.Context, tx pgx.Tx, i Incident) error {
	tag, err := tx.Exec(ctx, `UPDATE incident.records SET state=$3,revision=$4,resolved_at=$5,recovery_known_at=$6,suppressed_until=$7,updated_at=clock_timestamp(),last_observed_at=$8 WHERE tenant_id=$1 AND incident_id=$2 AND revision=$4-1`, i.TenantID, i.IncidentID, i.State, i.Revision, i.ResolvedAt, i.RecoveryKnownAt, i.SuppressedUntil, i.LastObservedAt)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrRevision
	}
	return err
}
func Append(ctx context.Context, tx pgx.Tx, i Incident, kind, actor, reason string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO incident.timeline VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp())`, i.TenantID, i.IncidentID, uuid.Must(uuid.NewV7()), kind, actor, i.PolicyVersion, reason, raw)
	if err != nil {
		return err
	}
	snapshot, _ := json.Marshal(i)
	_, err = tx.Exec(ctx, `INSERT INTO incident.outbox(tenant_id,event_id,incident_id,revision,payload) VALUES($1,$2,$3,$4,$5)`, i.TenantID, uuid.Must(uuid.NewV7()), i.IncidentID, i.Revision, snapshot)
	return err
}
