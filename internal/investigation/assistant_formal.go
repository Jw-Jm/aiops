package investigation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/rca"
	"slices"
)

type FormalReference struct {
	Kind          string     `json:"kind"`
	IncidentID    uuid.UUID  `json:"incidentId"`
	Revision      int64      `json:"revision"`
	ActionPlanID  *uuid.UUID `json:"actionPlanId,omitempty"`
	ContentDigest string     `json:"contentDigest"`
}

type FormalRead struct {
	FormalReferences   []FormalReference `json:"formalReferences"`
	RCA                json.RawMessage   `json:"rca,omitempty"`
	Plans              []map[string]any  `json:"plans"`
	Historical         bool              `json:"historical"`
	CurrentRCARevision int64             `json:"currentRcaRevision"`
}

func authorizeFormalIncidentTx(ctx context.Context, tx pgx.Tx, j Job, id uuid.UUID) (int64, error) {
	if j.Assistant == nil || j.Assistant.AuthorizationDomain != "operational" || j.Assistant.TaskKind != "incident_followup" || id != j.IncidentID {
		return 0, ErrDenied
	}
	var current int64
	var cluster, ns string
	if tx.QueryRow(ctx, `SELECT cluster_uid,namespace,current_rca_revision FROM incident.records WHERE tenant_id=$1 AND incident_id=$2`, j.TenantID, id).Scan(&cluster, &ns, &current) != nil || cluster != j.Scope.Cluster || (ns == "" && !j.Scope.ClusterScoped) || (ns != "" && !slices.Contains(j.Scope.Namespaces, ns)) {
		return 0, ErrDenied
	}
	var hidden bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2 AND NOT COALESCE(f.cluster_uid=$3 AND (f.namespace=ANY($4::text[]) OR (f.namespace='' AND $5)) AND (cardinality($6::text[])=0 OR f.resource_canonical_id=ANY($6::text[])),false))`, j.TenantID, id, j.Scope.Cluster, append([]string{}, j.Scope.Namespaces...), j.Scope.ClusterScoped, append([]string{}, j.Scope.Resources...)).Scan(&hidden) != nil || hidden {
		return 0, ErrDenied
	}
	return current, nil
}

func authorizeFormalRCATx(ctx context.Context, tx pgx.Tx, j Job, raw []byte) error {
	var rev rca.Revision
	if json.Unmarshal(raw, &rev) != nil || rev.TenantID != j.TenantID.String() || rev.IncidentID != j.IncidentID.String() {
		return ErrInvalid
	}
	if rca.CheckGraphSources(ctx, tx, j.TenantID, rev.InputManifest.GraphSources, rev.InputManifest.Graph) != nil {
		return ErrDenied
	}
	for _, node := range rev.InputManifest.Graph.Nodes {
		if !j.Scope.Allows(node.CanonicalID, node.Namespace) {
			return ErrDenied
		}
	}
	for _, ref := range rev.InputManifest.Evidence {
		id, err := uuid.Parse(ref.EvidenceID)
		if err != nil {
			return ErrInvalid
		}
		e, err := evidence.GetTx(ctx, tx, j.TenantID, id, j.Scope)
		if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, e.DataClass) {
			return ErrDenied
		}
	}
	return nil
}

// Historical RCA is explicitly identified. Plans are referenced by their exact
// stored revision and digest; a changed plan is never substituted with latest.
func ReadFormalReferencesTx(ctx context.Context, tx pgx.Tx, j Job, revision int64) (FormalRead, []string, error) {
	out := FormalRead{FormalReferences: []FormalReference{}, Plans: []map[string]any{}}
	refs := []string{}
	current, err := authorizeFormalIncidentTx(ctx, tx, j, j.IncidentID)
	if err != nil {
		return out, refs, err
	}
	if revision == 0 {
		revision = current
	}
	out.CurrentRCARevision = current
	out.Historical = revision != current
	if revision < 1 {
		return out, refs, nil
	}
	var raw json.RawMessage
	if tx.QueryRow(ctx, `SELECT result FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND revision=$3`, j.TenantID, j.IncidentID, revision).Scan(&raw) != nil {
		return out, refs, ErrDenied
	}
	if err := authorizeFormalRCATx(ctx, tx, j, raw); err != nil {
		return out, refs, err
	}
	out.FormalReferences = append(out.FormalReferences, FormalReference{Kind: "rca", IncidentID: j.IncidentID, Revision: revision, ContentDigest: graph.ScopeDigest(raw)})
	var rev rca.Revision
	json.Unmarshal(raw, &rev)
	out.RCA = raw
	for _, e := range rev.InputManifest.Evidence {
		refs = append(refs, e.EvidenceID)
	}
	rows, err := tx.Query(ctx, `SELECT action_plan_id,revision,content,state FROM action.plans WHERE tenant_id=$1 AND incident_id=$2 AND (content->>'rcaRevision')::bigint=$3 ORDER BY action_plan_id LIMIT 10`, j.TenantID, j.IncidentID, revision)
	if err != nil {
		return out, refs, err
	}
	type storedPlan struct {
		id      uuid.UUID
		version int64
		content json.RawMessage
		state   string
	}
	plans := []storedPlan{}
	for rows.Next() {
		var p storedPlan
		if err = rows.Scan(&p.id, &p.version, &p.content, &p.state); err != nil {
			rows.Close()
			return out, refs, err
		}
		plans = append(plans, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, refs, err
	}
	for _, p := range plans {
		var plan map[string]any
		if json.Unmarshal(p.content, &plan) != nil {
			return out, refs, ErrInvalid
		}
		target, _ := plan["targetCanonicalId"].(string)
		var ns string
		if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, target).Scan(&ns) != nil || !j.Scope.Allows(target, ns) {
			continue
		}
		id := p.id
		out.FormalReferences = append(out.FormalReferences, FormalReference{Kind: "action_plan", IncidentID: j.IncidentID, Revision: p.version, ActionPlanID: &id, ContentDigest: graph.ScopeDigest(p.content)})
		safe := map[string]any{"actionPlanId": p.id, "revision": p.version, "state": p.state}
		for _, key := range []string{"rcaRevision", "iteration", "targetCanonicalId", "reason", "expectedImpact", "preconditions", "riskSummary", "postCheckInstructions", "rollbackSuggestion"} {
			if value, ok := plan[key]; ok {
				safe[key] = value
			}
		}
		out.Plans = append(out.Plans, safe)
	}
	return out, refs, nil
}

func AuthorizeFormalReferenceTx(ctx context.Context, tx pgx.Tx, j Job, ref FormalReference) error {
	if ref.Revision < 1 {
		return ErrInvalid
	}
	if _, err := authorizeFormalIncidentTx(ctx, tx, j, ref.IncidentID); err != nil {
		return err
	}
	var content json.RawMessage
	switch ref.Kind {
	case "rca":
		if ref.ActionPlanID != nil {
			return ErrInvalid
		}
		if tx.QueryRow(ctx, `SELECT result FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND revision=$3`, j.TenantID, ref.IncidentID, ref.Revision).Scan(&content) != nil {
			return ErrDenied
		}
		if authorizeFormalRCATx(ctx, tx, j, content) != nil {
			return ErrDenied
		}
	case "action_plan":
		if ref.ActionPlanID == nil {
			return ErrInvalid
		}
		if tx.QueryRow(ctx, `SELECT content FROM action.plans WHERE tenant_id=$1 AND incident_id=$2 AND action_plan_id=$3 AND revision=$4`, j.TenantID, ref.IncidentID, *ref.ActionPlanID, ref.Revision).Scan(&content) != nil {
			return ErrDenied
		}
		var target string
		var plan struct {
			Target      string `json:"targetCanonicalId"`
			RCARevision int64  `json:"rcaRevision"`
		}
		if json.Unmarshal(content, &plan) != nil {
			return ErrInvalid
		}
		target = plan.Target
		var ns string
		if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, target).Scan(&ns) != nil || !j.Scope.Allows(target, ns) {
			return ErrDenied
		}
		var rcadata []byte
		if tx.QueryRow(ctx, `SELECT result FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND revision=$3`, j.TenantID, ref.IncidentID, plan.RCARevision).Scan(&rcadata) != nil || authorizeFormalRCATx(ctx, tx, j, rcadata) != nil {
			return ErrDenied
		}
	default:
		return ErrInvalid
	}
	if graph.ScopeDigest(content) != ref.ContentDigest {
		return ErrDenied
	}
	return nil
}
