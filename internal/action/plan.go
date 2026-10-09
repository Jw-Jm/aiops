package action

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/persistence"
	"time"
)

// PersistPlanTx is called only after the existing Go investigation Validator
// accepts the suggestion. It stores a recommendation and grants no authority.
func PersistPlanTx(ctx context.Context, tx pgx.Tx, tenant, incident uuid.UUID, raw []byte) error {
	if contract.Validate("https://ops.local/schemas/action-plan/v2", raw) != nil {
		return ErrInvalid
	}
	var p struct {
		ID       uuid.UUID `json:"actionPlanId"`
		Tenant   uuid.UUID `json:"tenantId"`
		Incident uuid.UUID `json:"incidentId"`
		State    string    `json:"state"`
		Command  string    `json:"suggestedCommand"`
		Shell    string    `json:"shell"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Tenant != tenant || p.Incident != incident || p.State != "suggested" || ValidateCommand(p.Command, p.Shell) != nil {
		return ErrInvalid
	}
	tag, err := tx.Exec(ctx, `INSERT INTO action.plans(tenant_id,action_plan_id,incident_id,content,state) VALUES($1,$2,$3,$4,'suggested') ON CONFLICT DO NOTHING`, tenant, p.ID, incident, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
func (s Service) GetPlan(ctx context.Context, a auth.RequestContext, id uuid.UUID) (json.RawMessage, error) {
	var out json.RawMessage
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT content FROM action.plans WHERE tenant_id=$1 AND action_plan_id=$2`, a.TenantID, id).Scan(&out); err != nil {
			return err
		}
		var p struct {
			Target string `json:"targetCanonicalId"`
		}
		if json.Unmarshal(out, &p) != nil {
			return ErrInvalid
		}
		var cluster, ns string
		if err := tx.QueryRow(ctx, `SELECT c.cluster_uid,e.namespace FROM platform.resource_entities e JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE e.tenant_id=$1 AND e.canonical_id=$2`, a.TenantID, p.Target).Scan(&cluster, &ns); err != nil {
			return err
		}
		b := Binding{Target: p.Target, ClusterUID: cluster}
		if ns != "" {
			b.Namespace = &ns
		}
		_, err := effectiveRead(ctx, tx, a, b)
		return err
	})
	return out, err
}

func (s Service) CreatePlan(ctx context.Context, a auth.RequestContext, raw json.RawMessage, key string) (json.RawMessage, error) {
	var out json.RawMessage
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		var p map[string]any
		if json.Unmarshal(raw, &p) != nil || p["tenantId"] != a.TenantID.String() || p["state"] != "suggested" {
			return ErrInvalid
		}
		target, ok := p["targetCanonicalId"].(string)
		if !ok {
			return ErrInvalid
		}
		incident, err := uuid.Parse(fmt.Sprint(p["incidentId"]))
		if err != nil {
			return ErrInvalid
		}
		var cluster, ns string
		if err = tx.QueryRow(ctx, `SELECT c.cluster_uid,e.namespace FROM platform.resource_entities e JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE e.tenant_id=$1 AND e.canonical_id=$2 AND e.deleted_at IS NULL AND c.status='active'`, a.TenantID, target).Scan(&cluster, &ns); err != nil {
			return ErrDenied
		}
		b := Binding{Target: target, ClusterUID: cluster}
		if ns != "" {
			b.Namespace = &ns
		}
		if _, err = effectiveRead(ctx, tx, a, b); err != nil {
			return err
		}
		var exists bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM incident.records i JOIN incident.finding_links l USING(tenant_id,incident_id) JOIN finding.records f USING(tenant_id,finding_id) WHERE i.tenant_id=$1 AND i.incident_id=$2 AND f.cluster_uid=$3)`, a.TenantID, incident, cluster).Scan(&exists) != nil || !exists {
			return ErrDenied
		}
		// This route records operator-authored suggestions only. Model suggestions
		// must pass the existing investigation Go Validator before PersistPlanTx.
		p["generatedBy"] = map[string]any{"type": "operator", "name": a.Subject}
		p["createdAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		out = Canonical(p)
		d, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: "create-action-plan"}, key, persistence.Digest(Digest(raw)))
		if err != nil {
			return err
		}
		if d.Kind == persistence.DecisionReplay {
			out = d.Response.Body
			return nil
		}
		if d.Kind != persistence.DecisionProceed {
			return ErrConflict
		}
		if err = PersistPlanTx(ctx, tx, a.TenantID, incident, out); err != nil {
			return err
		}
		id, err := uuid.Parse(fmt.Sprint(p["actionPlanId"]))
		if err != nil {
			return ErrInvalid
		}
		if err = appendAudit(ctx, tx, a, id, "action_plan.suggested", map[string]any{"incidentId": incident, "suggestionDigest": Digest([]byte(fmt.Sprint(p["suggestedCommand"])))}); err != nil {
			return err
		}
		return complete(ctx, tx, d, json.RawMessage(out), 201)
	})
	return out, err
}
func (s Service) SetPlanState(ctx context.Context, a auth.RequestContext, id uuid.UUID, state, key string) (json.RawMessage, error) {
	if state != "accepted" && state != "dismissed" {
		return nil, ErrInvalid
	}
	var out json.RawMessage
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		var raw []byte
		var current string
		if err := tx.QueryRow(ctx, `SELECT content,state FROM action.plans WHERE tenant_id=$1 AND action_plan_id=$2 FOR UPDATE`, a.TenantID, id).Scan(&raw, &current); err != nil {
			return err
		}
		var p map[string]any
		if json.Unmarshal(raw, &p) != nil {
			return ErrInvalid
		}
		var cluster, ns string
		if tx.QueryRow(ctx, `SELECT c.cluster_uid,e.namespace FROM platform.resource_entities e JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE e.tenant_id=$1 AND e.canonical_id=$2 AND e.deleted_at IS NULL AND c.status='active'`, a.TenantID, p["targetCanonicalId"]).Scan(&cluster, &ns) != nil {
			return ErrDenied
		}
		b := Binding{Target: fmt.Sprint(p["targetCanonicalId"]), ClusterUID: cluster}
		if ns != "" {
			b.Namespace = &ns
		}
		if _, err := effectiveRead(ctx, tx, a, b); err != nil {
			return err
		}
		d, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: "action-plan-" + state}, key, persistence.Digest(Digest(Canonical(id))))
		if err != nil {
			return err
		}
		if d.Kind == persistence.DecisionReplay {
			out = d.Response.Body
			return nil
		}
		if d.Kind != persistence.DecisionProceed || current != "suggested" {
			return ErrConflict
		}
		p["state"] = state
		out = Canonical(p)
		if _, err = tx.Exec(ctx, `UPDATE action.plans SET state=$3,content=$4,revision=revision+1 WHERE tenant_id=$1 AND action_plan_id=$2`, a.TenantID, id, state, out); err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, a, id, "action_plan."+state, map[string]any{"executionAuthorityGranted": false}); err != nil {
			return err
		}
		return complete(ctx, tx, d, json.RawMessage(out), 200)
	})
	return out, err
}
