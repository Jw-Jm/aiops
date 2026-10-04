package httpapi

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"ops-platform/internal/rca"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (h *SP05Handlers) authorizeIncidentRead(ctx context.Context, actor auth.RequestContext, scope graph.Scope, i incident.Incident) error {
	return persistence.WithTenantTx(ctx, h.Pool, actor.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT f.resource_canonical_id,f.namespace FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2`, actor.TenantID, i.IncidentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, ns string
			if err := rows.Scan(&id, &ns); err != nil {
				return err
			}
			if !scope.Allows(id, ns) {
				return graph.ErrScope
			}
		}
		return rows.Err()
	})
}
func (h *SP05Handlers) authorizeRCARead(ctx context.Context, actor auth.RequestContext, scope graph.Scope, rev rca.Revision) error {
	if err := persistence.WithTenantTx(ctx, h.Pool, actor.TenantID, func(tx pgx.Tx) error {
		return rca.CheckGraphSources(ctx, tx, actor.TenantID, rev.InputManifest.GraphSources, rev.InputManifest.Graph)
	}); err != nil {
		return graph.ErrScope
	}
	for _, e := range rev.InputManifest.Evidence {
		id, err := uuid.Parse(e.EvidenceID)
		if err != nil {
			return graph.ErrScope
		}
		// This rechecks actual source revision/mapping and actual namespace, rather
		// than trusting the archived manifest's cached scope or source status.
		if _, err := (evidence.Repository{Pool: h.Pool}).Get(ctx, actor.TenantID, id, scope); err != nil {
			return graph.ErrScope
		}
	}
	for _, n := range rev.InputManifest.Graph.Nodes {
		if !scope.Allows(n.CanonicalID, n.Namespace) {
			return graph.ErrScope
		}
	}
	return nil
}
func (h *SP05Handlers) incidentPage(ctx context.Context, actor auth.RequestContext, scope graph.Scope, i incident.Incident, r *http.Request, kind string, limit int) (any, string, error) {
	query := r.URL.Query()
	query.Del("cursor")
	hash := finding.Hash([]string{r.URL.Path, query.Encode()})
	digest := graph.ScopeDigest(scope)
	after := ""
	if token := r.URL.Query().Get("cursor"); token != "" {
		raw, err := base64.RawURLEncoding.DecodeString(token)
		var c sp05Cursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Query != hash || c.ScopeDigest != digest {
			return nil, "", rca.ErrStale
		}
		after = c.After
	}
	sql := ""
	switch kind {
	case "timeline":
		sql = `SELECT timeline_id::text,jsonb_build_object('timelineId',timeline_id,'kind',kind,'actor',actor,'policyVersion',policy_version,'reason',reason,'payload',payload,'createdAt',created_at) FROM incident.timeline WHERE tenant_id=$1 AND incident_id=$2 AND ($3='' OR timeline_id::text>$3) AND ($5='' OR kind=$5) AND ($6::timestamptz IS NULL OR created_at>=$6) AND ($7::timestamptz IS NULL OR created_at<=$7) ORDER BY timeline_id LIMIT $4`
	case "evidence":
		sql = `SELECT DISTINCT e.evidence_id::text,e.metadata FROM incident.finding_links l JOIN finding.evidence_refs r USING(tenant_id,finding_id) JOIN platform.evidence_metadata e USING(tenant_id,evidence_id) WHERE l.tenant_id=$1 AND l.incident_id=$2 AND NOT e.deleting AND ($3='' OR e.evidence_id::text>$3) ORDER BY e.evidence_id::text LIMIT $4`
	case "rca/revisions":
		if after != "" {
			n, err := strconv.ParseInt(after, 10, 64)
			if err != nil || n < 1 {
				return nil, "", finding.ErrInvalid
			}
		}
		sql = `SELECT revision::text,result FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND ($3='' OR revision>NULLIF($3,'')::bigint) ORDER BY revision LIMIT $4`
	default:
		return nil, "", pgx.ErrNoRows
	}
	items := []json.RawMessage{}
	ids := []string{}
	err := persistence.WithTenantTx(ctx, h.Pool, actor.TenantID, func(tx pgx.Tx) error {
		args := []any{actor.TenantID, i.IncidentID, after, limit + 1}
		if kind == "timeline" {
			var from, to *time.Time
			for _, key := range []string{"from", "to"} {
				if value := r.URL.Query().Get(key); value != "" {
					clock, err := time.Parse(time.RFC3339Nano, value)
					if err != nil {
						return finding.ErrInvalid
					}
					if key == "from" {
						from = &clock
					} else {
						to = &clock
					}
				}
			}
			if from != nil && to != nil && from.After(*to) {
				return finding.ErrInvalid
			}
			args = append(args, r.URL.Query().Get("eventType"), from, to)
		}
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				return err
			}
			items = append(items, json.RawMessage(raw))
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	// Authorize every returned reference before releasing any partial page.
	for _, raw := range items {
		if kind == "evidence" {
			var e evidence.Evidence
			if json.Unmarshal(raw, &e) != nil {
				return nil, "", graph.ErrScope
			}
			id, err := uuid.Parse(e.EvidenceID)
			if err != nil {
				return nil, "", graph.ErrScope
			}
			if _, err = (evidence.Repository{Pool: h.Pool}).Get(ctx, actor.TenantID, id, scope); err != nil {
				return nil, "", graph.ErrScope
			}
		}
		if kind == "rca/revisions" {
			var rev rca.Revision
			if json.Unmarshal(raw, &rev) != nil {
				return nil, "", graph.ErrScope
			}
			if err := h.authorizeRCARead(ctx, actor, scope, rev); err != nil {
				return nil, "", err
			}
		}
	}
	next := ""
	if len(items) > limit {
		raw, _ := json.Marshal(sp05Cursor{After: ids[limit-1], ScopeDigest: digest, Query: hash})
		next = base64.RawURLEncoding.EncodeToString(raw)
		items = items[:limit]
	}
	return items, next, nil
}

func currentRCAView(i incident.Incident, rev rca.Revision) any {
	eligible := !rev.Superseded && rev.BaseIncidentRevision == i.Revision
	reasons := []string{}
	if !eligible {
		reasons = append(reasons, "incident-revision-changed")
	}
	for _, e := range rev.InputManifest.Evidence {
		if !e.TimeReliable || e.ObservedTo.Before(time.Now().Add(-5*time.Minute)) {
			eligible = false
			reasons = append(reasons, "expired-required-evidence")
		}
	}
	// A historical frozen result is preserved; current eligibility is explicitly
	// separate and cannot recycle its old freshness as current causal evidence.
	return map[string]any{"schemaVersion": "current-rca/v2", "revision": rev, "currentEligible": false, "baseEligible": eligible, "currentDegradedSources": append(reasons, "current-runtime-unverified"), "semantics": "frozen-revision-with-current-reference-authorization"}
}

var _ = strings.Compare

// Current eligibility requires actual runtime checks. Historical archive reads
// preserve the immutable evaluation but never recycle its old Graph freshness.
func (h *SP05Handlers) currentRCAView(ctx context.Context, actor auth.RequestContext, scope graph.Scope, i incident.Incident, rev rca.Revision) (data any, readErr error) {
	defer func() {
		if readErr == nil && h.authorizeRCARead(ctx, actor, scope, rev) != nil {
			data = nil
			readErr = graph.ErrScope
		}
	}()
	view := currentRCAView(i, rev).(map[string]any)
	baseEligible := view["baseEligible"].(bool)
	delete(view, "baseEligible")
	reasons := view["currentDegradedSources"].([]string)
	fail := func(reason string) (any, error) {
		view["currentEligible"] = false
		view["currentDegradedSources"] = append(reasons, reason)
		return view, nil
	}
	if !baseEligible {
		return view, nil
	}
	currentFindings, err := (rca.Repository{Pool: h.Pool}).FreezeFindings(ctx, actor.TenantID, i.IncidentID)
	if err != nil || rev.InputManifest.SchemaVersion != "rca-input/v2" || finding.Hash(currentFindings) != finding.Hash(rev.InputManifest.FindingRevisions) {
		return fail("current-finding-revisions-changed-or-unapplied")
	}
	recipeCurrent := false
	err = persistence.WithTenantTx(ctx, h.Pool, actor.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM incident.rca_revisions r JOIN platform.registry_versions v ON v.tenant_id=r.tenant_id AND v.version_id=r.recipe_version_id JOIN platform.cluster_registrations c ON c.tenant_id=r.tenant_id AND c.cluster_uid=$4 WHERE r.tenant_id=$1 AND r.incident_id=$2 AND r.revision=$3 AND v.retired_at IS NULL AND v.version_id=(SELECT a.version_id FROM platform.registry_activations a WHERE a.tenant_id=r.tenant_id AND a.kind='recipe' AND a.logical_name=v.logical_name AND (a.scope_type='tenant' OR (a.cluster_id=c.cluster_id AND (a.scope_type='cluster' OR a.namespace=$5))) ORDER BY CASE a.scope_type WHEN 'namespace' THEN 3 WHEN 'cluster' THEN 2 ELSE 1 END DESC LIMIT 1))`, actor.TenantID, i.IncidentID, rev.Revision, i.ClusterUID, i.Namespace).Scan(&recipeCurrent)
	})
	if err != nil || !recipeCurrent {
		return fail("current-recipe-unavailable-or-replaced")
	}
	api := h.GraphAPI
	if api == nil || api.Client == nil || len(api.SigningKey) != ed25519.PrivateKeySize || api.EndpointAllowed == nil {
		return fail("current-graph-unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	route, err := (graph.Repository{Pool: h.Pool}).Load(bounded, scope.Tenant, scope.Cluster)
	if err != nil || route.Endpoint == "" || !time.Now().Before(route.ExpiresAt) || !api.EndpointAllowed(route.Endpoint) {
		return fail("current-graph-owner-unavailable")
	}
	query := graph.Query{QueryKind: "diagnostic", CanonicalID: i.ResourceCanonicalID, Scope: scope, ExpectedOwnerEpoch: route.Epoch, MaxDepth: 2, MaxNodes: 200, MaxEdges: 400, RelationKinds: []string{}}
	raw, _ := json.Marshal(query)
	reply, err := api.call(bounded, route.Endpoint, "/internal/v1/graph:query", "platform-graph-query", actor.Subject, scope, raw)
	var current graph.Result
	if err != nil || contract.Validate("https://ops.local/schemas/resource-graph/v2", reply) != nil || json.Unmarshal(reply, &current) != nil {
		return fail("current-graph-unavailable")
	}
	finalRoute, err := (graph.Repository{Pool: h.Pool}).Load(bounded, scope.Tenant, scope.Cluster)
	if err != nil || finalRoute.Epoch != route.Epoch || finalRoute.Instance != route.Instance || finalRoute.LeaseUID != route.LeaseUID || !time.Now().Before(finalRoute.ExpiresAt) || current.OwnerInstance != route.Instance || current.GraphRevision != rev.InputManifest.Graph.GraphRevision {
		return fail("current-graph-revision-changed")
	}
	if current.Partial || current.Freshness != "fresh" || len(current.DegradedSources) > 0 {
		return fail("current-graph-required-source-degraded")
	}
	if h.authorizeRCARead(bounded, actor, scope, rev) != nil {
		return fail("current-reference-authorization-changed")
	}
	reasons = slices.DeleteFunc(reasons, func(reason string) bool { return reason == "current-runtime-unverified" })
	view["currentEligible"] = true
	view["currentGraphRevision"] = current.GraphRevision
	view["currentDegradedSources"] = reasons
	return view, nil
}

// CurrentInvestigationRCA reuses the existing deterministic current-runtime read gate.
func (h *SP05Handlers) CurrentInvestigationRCA(ctx context.Context, j investigation.Job) (int64, error) {
	actor := auth.RequestContext{TenantID: j.TenantID, Subject: j.Subject, Roles: []auth.Role{auth.Operator}}
	var i incident.Incident
	if err := persistence.WithTenantTx(ctx, h.Pool, j.TenantID, func(tx pgx.Tx) error {
		var err error
		i, err = incident.Load(ctx, tx, j.TenantID, j.IncidentID.String(), false)
		return err
	}); err != nil {
		return 0, err
	}
	rev, err := (rca.Repository{Pool: h.Pool}).Read(ctx, j.TenantID, j.IncidentID.String(), i.CurrentRCARevision)
	if err != nil {
		return 0, err
	}
	value, err := h.currentRCAView(ctx, actor, j.Scope, i, rev)
	if err != nil {
		return 0, err
	}
	view, ok := value.(map[string]any)
	if !ok || view["currentEligible"] != true {
		return 0, investigation.ErrDenied
	}
	return rev.Revision, nil
}
