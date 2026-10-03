package incident

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"slices"
	"strings"
	"time"
)

type Service struct{ Pool persistence.TxBeginner }
type Change struct {
	ExpectedRevision int64      `json:"expectedRevision"`
	State            string     `json:"state"`
	TargetState      string     `json:"targetState,omitempty"`
	Reason           string     `json:"reason"`
	EvidenceRefs     []string   `json:"evidenceRefs"`
	SuppressedUntil  *time.Time `json:"suppressedUntil"`
}

// Caller must supply a current, verified operator scope. Authorization is checked
// again against persistent bindings inside the mutating transaction.
func Authorize(ctx context.Context, tx pgx.Tx, scope graph.Scope, subject string, i Incident) error {
	if subject == "" || scope.Tenant != i.TenantID || scope.Cluster != i.ClusterUID || !scope.Allows(i.ResourceCanonicalID, i.Namespace) {
		return graph.ErrScope
	}
	var current bool
	err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_operator_scope($1,$2,$3,$4)`, i.TenantID, subject, i.ClusterUID, i.Namespace).Scan(&current)
	if err != nil {
		return err
	}
	if !current {
		return graph.ErrScope
	}
	return nil
}
func (s Service) Change(ctx context.Context, scope graph.Scope, subject, id string, c Change) (Incident, error) {
	var out Incident
	if c.State == "" {
		c.State = c.TargetState
	} else if c.TargetState != "" {
		return out, ErrTransition
	}
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return out, graph.ErrScope
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
		if c.ExpectedRevision != i.Revision {
			return ErrRevision
		}
		if !CanTransition(i.State, c.State) || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 1000 {
			return ErrTransition
		}
		if c.State == "open" && i.State == "resolved" {
			return ErrTransition
		} // only a new trustworthy occurrence may reopen
		if c.State == "suppressed" && (c.SuppressedUntil == nil || !c.SuppressedUntil.After(time.Now()) || c.SuppressedUntil.Sub(time.Now()) > 30*24*time.Hour) {
			return ErrTransition
		}
		if c.State == "resolved" && len(c.EvidenceRefs) == 0 {
			return ErrTransition
		}
		if err := manualEvidence(ctx, tx, tenant, scope, subject, i, c); err != nil {
			return err
		}
		previous := i.State
		i.State = c.State
		i.Revision++
		i.SuppressedUntil = c.SuppressedUntil
		if c.State != "suppressed" {
			i.SuppressedUntil = nil
		}
		if c.State == "resolved" {
			now := time.Now().UTC()
			i.ResolvedAt = &now
		}
		if err := Save(ctx, tx, i); err != nil {
			return err
		}
		if !Active(i.State) {
			if err := releaseReferences(ctx, tx, tenant, i.IncidentID); err != nil {
				return err
			}
		}
		if err := Append(ctx, tx, i, "manual_override", subject, c.Reason, map[string]any{"from": previous, "to": i.State, "evidenceRefs": c.EvidenceRefs}); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(id), EntityKind: "incident", EventType: "incident.override", Subject: subject, Payload: map[string]any{"reason": c.Reason, "policyVersion": i.PolicyVersion, "from": previous, "to": i.State, "revision": i.Revision}})
		out = i
		return err
	})
	return out, err
}

// Recovery is clocked from when the platform durably knew all occurrences were
// resolved. Unknown source/graph health blocks recovery, never proves healthy.
func (s Service) RecoveryPass(ctx context.Context, tenant uuid.UUID, healthy func(string) bool) error {
	return persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectIncident+` WHERE tenant_id=$1 AND (state IN('open','acknowledged','mitigating') AND recovery_known_at IS NOT NULL OR state='suppressed' AND suppressed_until<=clock_timestamp()) ORDER BY fingerprint,incident_id`, tenant)
		if err != nil {
			return err
		}
		items := []Incident{}
		for rows.Next() {
			i, err := scanIncident(rows)
			if err != nil {
				rows.Close()
				return err
			}
			items = append(items, i)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := LockSubject(ctx, tx, tenant, item.Fingerprint); err != nil {
				return err
			}
			i, err := Load(ctx, tx, tenant, item.IncidentID, true)
			if err != nil {
				return err
			}
			if i.State == "suppressed" && i.SuppressedUntil != nil && !time.Now().Before(*i.SuppressedUntil) {
				i.State = "open"
				i.SuppressedUntil = nil
				i.Revision++
				if err := Save(ctx, tx, i); err != nil {
					return err
				}
				if err := Append(ctx, tx, i, "suppression_expired", "correlation/v1", "suppression_expired", map[string]any{}); err != nil {
					return err
				}
				continue
			}
			if i.RecoveryKnownAt == nil || i.State == "resolved" || i.State == "closed" || i.State == "suppressed" {
				continue
			}
			var recovered, sourceHealthy bool
			if err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2 AND f.lifecycle_state IS DISTINCT FROM 'resolved'),NOT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) JOIN platform.source_registrations s USING(tenant_id,source_id) WHERE l.tenant_id=$1 AND l.incident_id=$2 AND (s.status<>'active' OR s.revision IS DISTINCT FROM (f.payload->>'sourceRevision')::bigint))`, tenant, i.IncidentID).Scan(&recovered, &sourceHealthy); err != nil {
				return err
			}
			if !RecoveryReady(time.Now(), *i.RecoveryKnownAt, recovered, sourceHealthy && healthy != nil && healthy(i.ClusterUID)) {
				continue
			}
			i.State = "resolved"
			i.Revision++
			now := time.Now().UTC()
			i.ResolvedAt = &now
			if err := Save(ctx, tx, i); err != nil {
				return err
			}
			if err := Append(ctx, tx, i, "resolved", "correlation/v1", "settle_window_and_sources_healthy", map[string]any{"settleSeconds": 300}); err != nil {
				return err
			}
			if err := releaseReferences(ctx, tx, tenant, i.IncidentID); err != nil {
				return err
			}
		}
		return nil
	})
}

// Merge and split are deliberate operator decisions with sorted subject/record
// locks. They preserve occurrence identity and append an audit/timeline record.
func (s Service) Merge(ctx context.Context, scope graph.Scope, subject, targetID, sourceID, reason string, targetRevision, sourceRevision int64) (Incident, error) {
	var result Incident
	if targetID == sourceID || reason == "" {
		return result, ErrTransition
	}
	tenant, err := uuid.Parse(scope.Tenant)
	if err != nil {
		return result, graph.ErrScope
	}
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		items := map[string]Incident{}
		for _, id := range []string{targetID, sourceID} {
			i, err := Load(ctx, tx, tenant, id, false)
			if err != nil {
				return err
			}
			items[id] = i
		}
		fingerprints := []string{items[targetID].Fingerprint, items[sourceID].Fingerprint}
		slices.Sort(fingerprints)
		for _, fp := range slices.Compact(fingerprints) {
			if err := LockSubject(ctx, tx, tenant, fp); err != nil {
				return err
			}
		}
		ids := []string{targetID, sourceID}
		slices.Sort(ids)
		for _, id := range ids {
			i, err := Load(ctx, tx, tenant, id, true)
			if err != nil {
				return err
			}
			if err := Authorize(ctx, tx, scope, subject, i); err != nil {
				return err
			}
			if err := authorizeLinked(ctx, tx, scope, subject, i); err != nil {
				return err
			}
			items[id] = i
		}
		target, source := items[targetID], items[sourceID]
		if target.Revision != targetRevision || source.Revision != sourceRevision {
			return ErrRevision
		}
		if !Active(target.State) || !Active(source.State) || target.ClusterUID != source.ClusterUID {
			return ErrTransition
		}
		if _, err := tx.Exec(ctx, `UPDATE incident.finding_links SET incident_id=$3 WHERE tenant_id=$1 AND incident_id=$2`, tenant, sourceID, targetID); err != nil {
			return err
		}
		source.State = "closed"
		source.Revision++
		target.Revision++
		target.RecoveryKnownAt = nil
		for _, i := range []Incident{source, target} {
			if err := Save(ctx, tx, i); err != nil {
				return err
			}
			if err := Append(ctx, tx, i, "merge", subject, reason, map[string]any{"targetId": targetID, "sourceId": sourceID}); err != nil {
				return err
			}
		}
		if err := protectLinked(ctx, tx, tenant, targetID, true); err != nil {
			return err
		}
		if err := releaseReferences(ctx, tx, tenant, sourceID); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: uuid.MustParse(targetID), EntityKind: "incident", EventType: "incident.merge", Subject: subject, Payload: map[string]any{"sourceId": sourceID, "reason": reason, "policyVersion": target.PolicyVersion}})
		result = target
		return err
	})
	return result, err
}

var _ = errors.Is
