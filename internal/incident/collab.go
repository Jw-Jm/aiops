package incident

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
)

// Collaboration writes (assignment, comments, follow-ups, manual alert links)
// share the incident write path: one transaction, subject locks, authorizeTx and
// authorizeLinked. They never rewrite correlation facts. Assignment and
// comment/follow-up records deliberately keep the incident revision untouched:
// the revision is the currency of evidence-bearing facts (RCA base revision,
// investigation trigger revision, reopen windows), and who is handling an
// incident or what people say about it changes none of those. Manual alert
// links do change the evidence set, so they take the full CAS revision path.

// appendTimeline records one timeline row without an outbox snapshot. The
// outbox drives RCA re-checks; collaboration records that leave the frozen
// evidence untouched must not trigger one.
func appendTimeline(ctx context.Context, tx pgx.Tx, i Incident, kind, actor, reason string, payload any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	id := uuid.Must(uuid.NewV7())
	if _, err = tx.Exec(ctx, `INSERT INTO incident.timeline VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp())`, i.TenantID, i.IncidentID, id, kind, actor, i.PolicyVersion, reason, raw); err != nil {
		return nil, err
	}
	var created time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM incident.timeline WHERE tenant_id=$1 AND timeline_id=$2`, i.TenantID, id).Scan(&created); err != nil {
		return nil, err
	}
	return map[string]any{"timelineId": id, "kind": kind, "actor": actor, "policyVersion": i.PolicyVersion, "reason": reason, "payload": payload, "createdAt": created}, nil
}

// saveAssignment writes the assignee columns with a column-level compare on the
// previously loaded value, so two concurrent assignments cannot silently
// overwrite each other even though the incident revision stays unchanged.
func saveAssignment(ctx context.Context, tx pgx.Tx, i Incident, previous string) error {
	tag, err := tx.Exec(ctx, `UPDATE incident.records SET assignee_subject=$3,assigned_at=$4,updated_at=clock_timestamp() WHERE tenant_id=$1 AND incident_id=$2 AND assignee_subject IS NOT DISTINCT FROM $5`, i.TenantID, i.IncidentID, nullableSubject(i.AssigneeSubject), i.AssignedAt, nullableSubject(previous))
	if err == nil && tag.RowsAffected() != 1 {
		return ErrRevision
	}
	return err
}

func nullableSubject(subject string) any {
	if subject == "" {
		return nil
	}
	return subject
}

func (s Service) loadAuthorized(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, scope graph.Scope, subject, id string) (Incident, operationAuthority, error) {
	var i Incident
	var authority operationAuthority
	var err error
	i, err = Load(ctx, tx, tenant, id, false)
	if err != nil {
		return i, authority, err
	}
	if err := LockSubject(ctx, tx, tenant, i.Fingerprint); err != nil {
		return i, authority, err
	}
	i, err = Load(ctx, tx, tenant, id, true)
	if err != nil {
		return i, authority, err
	}
	authority, err = authorizeTx(ctx, tx, scope, subject, i)
	if err != nil {
		return i, authority, err
	}
	return i, authority, authorizeLinked(ctx, tx, authority, i)
}

// Claim assigns an open incident to the caller. Claiming an incident already
// assigned to the caller is an idempotent no-op; assigned to anyone else it is
// a conflict, not an overwrite.
func (s Service) Claim(ctx context.Context, scope graph.Scope, subject, id string, expectedRevision int64, note string) (Incident, error) {
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return Incident{}, graph.ErrScope
	}
	if len(note) > 1000 {
		return Incident{}, ErrTransition
	}
	var out Incident
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, _, err := s.loadAuthorized(ctx, tx, tenant, scope, subject, id)
		if err != nil {
			return err
		}
		if i.State == "closed" {
			return ErrTransition
		}
		if expectedRevision != 0 && expectedRevision != i.Revision {
			return ErrRevision
		}
		if i.AssigneeSubject == subject {
			out = i
			return nil
		}
		if i.AssigneeSubject != "" {
			return ErrAssigned
		}
		previous := i.AssigneeSubject
		now := time.Now().UTC()
		i.AssigneeSubject = subject
		i.AssignedAt = &now
		if err := saveAssignment(ctx, tx, i, previous); err != nil {
			return err
		}
		if _, err := appendTimeline(ctx, tx, i, "assignment", subject, "claim", map[string]any{"action": "claim", "to": subject, "note": note}); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(id), EntityKind: "incident", EventType: "incident.assignment", Subject: subject, Payload: map[string]any{"action": "claim", "to": subject, "revision": i.Revision}})
		out = i
		return err
	})
	return out, err
}

// Transfer hands the incident to another eligible operator. A transfer never
// changes state: 转交不等于恢复, and the state fields are asserted unchanged.
// The current assignee and platform administrators may transfer; everyone else
// is refused regardless of scope.
func (s Service) Transfer(ctx context.Context, scope graph.Scope, subject, id, toSubject, reason string, expectedRevision int64, adminOverride bool) (Incident, error) {
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return Incident{}, graph.ErrScope
	}
	toSubject = strings.TrimSpace(toSubject)
	if toSubject == "" || toSubject == subject || strings.TrimSpace(reason) == "" || len(reason) > 1000 {
		return Incident{}, ErrTransition
	}
	var out Incident
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, _, err := s.loadAuthorized(ctx, tx, tenant, scope, subject, id)
		if err != nil {
			return err
		}
		if i.State == "closed" {
			return ErrTransition
		}
		if expectedRevision != 0 && expectedRevision != i.Revision {
			return ErrRevision
		}
		if i.AssigneeSubject == "" || (i.AssigneeSubject != subject && !adminOverride) {
			return graph.ErrScope
		}
		if i.AssigneeSubject == toSubject {
			out = i
			return nil
		}
		if err := transferEligible(ctx, tx, tenant, i, toSubject); err != nil {
			return err
		}
		previous := i.AssigneeSubject
		before := struct {
			state           string
			resolvedAt      *time.Time
			suppressedUntil *time.Time
			recoveryKnownAt *time.Time
		}{i.State, i.ResolvedAt, i.SuppressedUntil, i.RecoveryKnownAt}
		now := time.Now().UTC()
		i.AssigneeSubject = toSubject
		i.AssignedAt = &now
		if err := saveAssignment(ctx, tx, i, previous); err != nil {
			return err
		}
		if i.State != before.state || i.ResolvedAt != before.resolvedAt || i.SuppressedUntil != before.suppressedUntil || i.RecoveryKnownAt != before.recoveryKnownAt {
			return ErrTransition // transfer must never mutate recovery or state
		}
		if _, err := appendTimeline(ctx, tx, i, "assignment", subject, reason, map[string]any{"action": "transfer", "from": previous, "to": toSubject}); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(id), EntityKind: "incident", EventType: "incident.assignment", Subject: subject, Payload: map[string]any{"action": "transfer", "from": previous, "to": toSubject, "revision": i.Revision, "reason": reason}})
		out = i
		return err
	})
	return out, err
}

// transferEligible proves the target subject can currently act on this
// incident. The whole decision runs inside the SECURITY DEFINER helper
// (migration 00073): a native recipient needs an active operator grant
// covering the incident cluster and namespace — the same persisted projection
// the platform uses, without inventing a session for someone who is not the
// caller — and a legacy recipient keeps the legacy operator binding fence.
func transferEligible(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, i Incident, toSubject string) error {
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT incident.transfer_eligible($1,$2,$3,$4,$5)`, tenant, toSubject, i.ClusterUID, i.ResourceCanonicalID, i.Namespace).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return ErrTransition
	}
	return nil
}

// Comment appends one operator remark to the timeline. Comments are allowed in
// any state (post-mortem notes are the point of a closed incident's record);
// they are append-only and touch neither the incident row nor its links. The
// reference form also carries the current business impact assessment, which is
// stored on the comment and displayed as the incident's latest impact.
func (s Service) Comment(ctx context.Context, scope graph.Scope, subject, id, body, impact string) (map[string]any, error) {
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return nil, graph.ErrScope
	}
	body = strings.TrimSpace(body)
	impact = strings.TrimSpace(impact)
	if body == "" || len(body) > 4000 || len(impact) > 1000 {
		return nil, ErrTransition
	}
	var out map[string]any
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, _, err := s.loadAuthorized(ctx, tx, tenant, scope, subject, id)
		if err != nil {
			return err
		}
		entry, err := appendTimeline(ctx, tx, i, "comment", subject, "manual_comment", map[string]any{"text": body, "impact": impact})
		if err != nil {
			return err
		}
		out = entry
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(id), EntityKind: "incident", EventType: "incident.comment", Subject: subject, Payload: map[string]any{"timelineId": entry["timelineId"]}})
		return err
	})
	return out, err
}

// CreateFollowUp records a retrospective todo. Follow-ups live beside the
// incident facts and never modify them; they remain actionable after closure.
func (s Service) CreateFollowUp(ctx context.Context, scope graph.Scope, subject, id, title, detail, ownerSubject string) (FollowUp, error) {
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return FollowUp{}, graph.ErrScope
	}
	title = strings.TrimSpace(title)
	detail = strings.TrimSpace(detail)
	ownerSubject = strings.TrimSpace(ownerSubject)
	if title == "" || len(title) > 200 || len(detail) > 4000 || len(ownerSubject) > 256 {
		return FollowUp{}, ErrTransition
	}
	var out FollowUp
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, _, err := s.loadAuthorized(ctx, tx, tenant, scope, subject, id)
		if err != nil {
			return err
		}
		if ownerSubject != "" && ownerSubject != subject {
			if err := transferEligible(ctx, tx, tenant, i, ownerSubject); err != nil {
				return err
			}
		}
		followUpID := uuid.Must(uuid.NewV7())
		owner := ownerSubject
		if owner == "" {
			owner = subject
		}
		if _, err := tx.Exec(ctx, `INSERT INTO incident.followups(tenant_id,incident_id,followup_id,title,detail,state,creator_subject,owner_subject) VALUES($1,$2,$3,$4,$5,'open',$6,NULLIF($7,''))`, tenant, i.IncidentID, followUpID, title, detail, subject, owner); err != nil {
			return err
		}
		if _, err := appendTimeline(ctx, tx, i, "followup", subject, "followup_created", map[string]any{"action": "created", "followUpId": followUpID, "title": title}); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(id), EntityKind: "incident", EventType: "incident.followup.create", Subject: subject, Payload: map[string]any{"followUpId": followUpID, "title": title}})
		out = FollowUp{SchemaVersion: "incident-followup/v1", FollowUpID: followUpID.String(), IncidentID: i.IncidentID, Title: title, Detail: detail, State: "open", CreatorSubject: subject, OwnerSubject: owner, CreatedAt: time.Now().UTC()}
		return err
	})
	return out, err
}

// CompleteFollowUp closes an open todo. The creator, the current assignee and
// platform administrators may complete it.
func (s Service) CompleteFollowUp(ctx context.Context, scope graph.Scope, subject, id, followUpID, note string, adminOverride bool) (FollowUp, error) {
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return FollowUp{}, graph.ErrScope
	}
	note = strings.TrimSpace(note)
	if note == "" || len(note) > 1000 {
		return FollowUp{}, ErrTransition
	}
	fid, err := uuid.Parse(followUpID)
	if err != nil {
		return FollowUp{}, ErrTransition
	}
	var out FollowUp
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, _, err := s.loadAuthorized(ctx, tx, tenant, scope, subject, id)
		if err != nil {
			return err
		}
		var f FollowUp
		var completedAt *time.Time
		var completer string
		var owner sql.NullString
		if err := tx.QueryRow(ctx, `SELECT followup_id::text,incident_id,title,detail,state,creator_subject,COALESCE(owner_subject,''),created_at,completed_at,COALESCE(completer_subject,''),resolution_note FROM incident.followups WHERE tenant_id=$1 AND incident_id=$2 AND followup_id=$3 FOR UPDATE`, tenant, i.IncidentID, fid).Scan(&f.FollowUpID, &f.IncidentID, &f.Title, &f.Detail, &f.State, &f.CreatorSubject, &owner, &f.CreatedAt, &completedAt, &completer, &f.ResolutionNote); err != nil {
			return err
		}
		f.OwnerSubject = owner.String
		if f.State != "open" {
			return ErrTransition
		}
		if subject != f.CreatorSubject && subject != f.OwnerSubject && subject != i.AssigneeSubject && !adminOverride {
			return graph.ErrScope
		}
		now := time.Now().UTC()
		tag, err := tx.Exec(ctx, `UPDATE incident.followups SET state='done',completed_at=$4,completer_subject=$5,resolution_note=$6 WHERE tenant_id=$1 AND incident_id=$2 AND followup_id=$3 AND state='open'`, tenant, i.IncidentID, fid, now, subject, note)
		if err == nil && tag.RowsAffected() != 1 {
			return ErrTransition
		}
		if err != nil {
			return err
		}
		if _, err := appendTimeline(ctx, tx, i, "followup", subject, "followup_completed", map[string]any{"action": "completed", "followUpId": fid, "note": note}); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(id), EntityKind: "incident", EventType: "incident.followup.complete", Subject: subject, Payload: map[string]any{"followUpId": fid}})
		out = FollowUp{SchemaVersion: "incident-followup/v1", FollowUpID: f.FollowUpID, IncidentID: f.IncidentID, Title: f.Title, Detail: f.Detail, State: "done", CreatorSubject: f.CreatorSubject, OwnerSubject: f.OwnerSubject, CreatedAt: f.CreatedAt, CompletedAt: &now, CompleterSubject: subject, ResolutionNote: note}
		return err
	})
	return out, err
}

// ReopenFollowUp returns a completed or cancelled todo to the open state. The
// reference task list allows toggling; reopening keeps the original owner and
// clears completion facts so the item can be worked again.
func (s Service) ReopenFollowUp(ctx context.Context, scope graph.Scope, subject, id, followUpID string, adminOverride bool) (FollowUp, error) {
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return FollowUp{}, graph.ErrScope
	}
	fid, err := uuid.Parse(followUpID)
	if err != nil {
		return FollowUp{}, ErrTransition
	}
	var out FollowUp
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, _, err := s.loadAuthorized(ctx, tx, tenant, scope, subject, id)
		if err != nil {
			return err
		}
		var f FollowUp
		var owner, completer sql.NullString
		var completedAt sql.NullTime
		if err := tx.QueryRow(ctx, `SELECT followup_id::text,incident_id,title,detail,state,creator_subject,COALESCE(owner_subject,''),created_at,completed_at,COALESCE(completer_subject,''),resolution_note FROM incident.followups WHERE tenant_id=$1 AND incident_id=$2 AND followup_id=$3 FOR UPDATE`, tenant, i.IncidentID, fid).Scan(&f.FollowUpID, &f.IncidentID, &f.Title, &f.Detail, &f.State, &f.CreatorSubject, &owner, &f.CreatedAt, &completedAt, &completer, &f.ResolutionNote); err != nil {
			return err
		}
		f.OwnerSubject = owner.String
		if f.State == "open" {
			return ErrTransition
		}
		if subject != f.CreatorSubject && subject != f.OwnerSubject && subject != i.AssigneeSubject && !adminOverride {
			return graph.ErrScope
		}
		tag, err := tx.Exec(ctx, `UPDATE incident.followups SET state='open',completed_at=NULL,completer_subject=NULL,resolution_note='' WHERE tenant_id=$1 AND incident_id=$2 AND followup_id=$3 AND state<>'open'`, tenant, i.IncidentID, fid)
		if err == nil && tag.RowsAffected() != 1 {
			return ErrTransition
		}
		if err != nil {
			return err
		}
		if _, err := appendTimeline(ctx, tx, i, "followup", subject, "followup_reopened", map[string]any{"action": "reopened", "followUpId": fid}); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(id), EntityKind: "incident", EventType: "incident.followup.reopen", Subject: subject, Payload: map[string]any{"followUpId": fid}})
		out = FollowUp{SchemaVersion: "incident-followup/v1", FollowUpID: f.FollowUpID, IncidentID: f.IncidentID, Title: f.Title, Detail: f.Detail, State: "open", CreatorSubject: f.CreatorSubject, OwnerSubject: f.OwnerSubject, CreatedAt: f.CreatedAt}
		return err
	})
	return out, err
}

// AlertLinks manually links or unlinks findings (alerts) on one incident. This
// changes the evidence set, so it takes the full revision CAS path, recomputes
// the recovery clock and re-protects the linked findings' retention references.
// A finding already linked to another incident must be unlinked there first,
// and an incident always keeps at least one linked finding.
func (s Service) AlertLinks(ctx context.Context, scope graph.Scope, subject, id, action string, findingIDs []string, reason string, expectedRevision int64) (Incident, error) {
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return Incident{}, graph.ErrScope
	}
	if action != "link" && action != "unlink" {
		return Incident{}, ErrTransition
	}
	if strings.TrimSpace(reason) == "" || len(reason) > 1000 || len(findingIDs) == 0 || len(findingIDs) > 100 {
		return Incident{}, ErrTransition
	}
	seen := map[string]bool{}
	for _, fid := range findingIDs {
		if _, err := uuid.Parse(fid); err != nil || seen[fid] {
			return Incident{}, ErrTransition
		}
		seen[fid] = true
	}
	var out Incident
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		i, authority, err := s.loadAuthorized(ctx, tx, tenant, scope, subject, id)
		if err != nil {
			return err
		}
		if i.State == "closed" {
			return ErrTransition
		}
		if expectedRevision != i.Revision {
			return ErrRevision
		}
		linkedNow, err := linkedFindings(ctx, tx, tenant, i.IncidentID)
		if err != nil {
			return err
		}
		for _, fid := range findingIDs {
			f, err := finding.Load(ctx, tx, tenant, fid)
			if err != nil {
				return err
			}
			if f.ClusterUID != i.ClusterUID {
				return ErrTransition
			}
			if !scope.Allows(f.ResourceCanonicalID, f.Namespace) {
				return graph.ErrScope
			}
			if err := authority.authorizeResource(ctx, tx, i.ClusterUID, f.ResourceCanonicalID, f.Namespace); err != nil {
				return err
			}
			var elsewhere bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM incident.finding_links WHERE tenant_id=$1 AND finding_id=$2 AND incident_id<>$3)`, tenant, fid, i.IncidentID).Scan(&elsewhere); err != nil {
				return err
			}
			_, onThis := linkedNow[fid]
			if action == "link" {
				if elsewhere {
					return ErrTransition
				}
				if !onThis {
					if _, err := tx.Exec(ctx, `INSERT INTO incident.finding_links VALUES($1,$2,$3)`, tenant, i.IncidentID, fid); err != nil {
						return err
					}
					linkedNow[fid] = true
				}
			} else if onThis {
				if len(linkedNow) <= 1 {
					return ErrTransition // an incident keeps at least one finding
				}
				if _, err := tx.Exec(ctx, `DELETE FROM incident.finding_links WHERE tenant_id=$1 AND incident_id=$2 AND finding_id=$3`, tenant, i.IncidentID, fid); err != nil {
					return err
				}
				delete(linkedNow, fid)
			}
		}
		i.Revision++
		if err := recomputeRecovery(ctx, tx, tenant, &i); err != nil {
			return err
		}
		if err := Save(ctx, tx, i); err != nil {
			return err
		}
		if action == "link" {
			if err := protectLinked(ctx, tx, tenant, i.IncidentID, Active(i.State)); err != nil {
				return err
			}
		}
		if err := Append(ctx, tx, i, "alert_link", subject, reason, map[string]any{"action": action, "findingIds": findingIDs}); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(id), EntityKind: "incident", EventType: "incident.alert_link", Subject: subject, Payload: map[string]any{"action": action, "findingIds": findingIDs, "reason": reason, "revision": i.Revision}})
		out = i
		return err
	})
	return out, err
}

func linkedFindings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, incidentID string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT finding_id::text FROM incident.finding_links WHERE tenant_id=$1 AND incident_id=$2`, tenant, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var fid string
		if err := rows.Scan(&fid); err != nil {
			return nil, err
		}
		out[fid] = true
	}
	return out, rows.Err()
}
