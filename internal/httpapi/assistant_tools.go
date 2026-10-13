package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
)

// assistantGraphReader invokes the existing signed graph transport with a
// compiler/resolver narrowed scope. The public resource pagination is not used.
type assistantGraphReader struct {
	Handlers *SP04Handlers
	Job      investigation.Job
}

func (a assistantGraphReader) Query(ctx context.Context, q graph.Query) (graph.Result, error) {
	var out graph.Result
	h := a.Handlers
	if h == nil || h.Client == nil || h.EndpointAllowed == nil || !investigation.ScopeSubset(a.Job.Scope, q.Scope) {
		return out, investigation.ErrDenied
	}
	current, err := (graph.Authorization{Pool: h.Pool}).Effective(ctx, q.Scope.Tenant, a.Job.Subject, q.Scope.Cluster)
	if err != nil || a.Job.Assistant == nil || graph.ScopeDigest(current) != a.Job.Assistant.GrantSnapshotDigest {
		return out, investigation.ErrDenied
	}
	route, err := (graph.Repository{Pool: h.Pool}).Load(ctx, q.Scope.Tenant, q.Scope.Cluster)
	if err != nil || route.Epoch != q.ExpectedOwnerEpoch || !route.ExpiresAt.After(time.Now()) || !h.EndpointAllowed(route.Endpoint) {
		return out, graph.ErrNotReady
	}
	reply, err := h.callWithGrantDigest(ctx, route.Endpoint, "/internal/v1/graph:query", "platform-graph-query", a.Job.Subject, q.Scope, rawJSON(q), a.Job.Assistant.GrantSnapshotDigest)
	if err != nil {
		return out, err
	}
	if contract.Validate("https://ops.local/schemas/resource-graph/v2", reply) != nil || json.Unmarshal(reply, &out) != nil || out.GraphRevision.OwnerEpoch != route.Epoch || out.OwnerInstance != route.Instance {
		return out, graph.ErrNotReady
	}
	final, err := (graph.Repository{Pool: h.Pool}).Load(ctx, q.Scope.Tenant, q.Scope.Cluster)
	if err != nil || final.Epoch != route.Epoch || final.Instance != route.Instance || final.LeaseUID != route.LeaseUID || !final.ExpiresAt.After(time.Now()) {
		return graph.Result{}, graph.ErrNotReady
	}
	return out, nil
}
func rawJSON(v any) []byte { b, _ := json.Marshal(v); return b }

type AssistantTools struct{ Legacy InvestigationTools }

// Private in-process binding: HTTP callers cannot supply or widen this value.
type assistantEvidenceScopeKey struct{}
type assistantEvidenceScope struct {
	Scope       graph.Scope
	GrantDigest string
}

func (a AssistantTools) CheckScope(ctx context.Context, j investigation.Job, name string, args json.RawMessage) error {
	if j.Assistant == nil {
		return a.Legacy.CheckScope(ctx, j, name, args)
	}
	catalog, err := analytics.Catalog()
	if err != nil || graph.ScopeDigest(catalog) != j.Assistant.DatasetCatalogDigest {
		return investigation.ErrDenied
	}
	if !j.Assistant.Valid(j) {
		return investigation.ErrDenied
	}
	if j.Assistant.AuthorizationDomain == "audit" && name != "get_assistant_context" && name != "describe_analysis_dataset" && name != "execute_analysis_query" {
		return investigation.ErrDenied
	}
	return a.Legacy.CheckScope(ctx, j, name, args)
}
func (a AssistantTools) Invoke(ctx context.Context, j investigation.Job, name string, args json.RawMessage) (tools.Result, error) {
	out := tools.Result{SchemaVersion: "tool-response/v3", Tool: name, State: "succeeded", Data: map[string]any{}, EvidenceRefs: []string{}, DegradedSources: []string{}}
	if j.Assistant == nil {
		return a.Legacy.Invoke(ctx, j, name, args)
	}
	if err := a.CheckScope(ctx, j, name, args); err != nil {
		return out, err
	}
	h := a.Legacy.SP04
	if h == nil {
		return out, investigation.ErrDenied
	}
	var input assistant.TurnInput
	storedResult := func(result analytics.QueryResult) (investigation.StoredQueryResult, error) {
		var stored investigation.StoredQueryResult
		err := persistence.WithTenantTx(ctx, h.Pool, j.TenantID, func(tx pgx.Tx) error {
			if err := investigation.CheckAssistantJobTx(ctx, tx, j); err != nil {
				return err
			}
			var err error
			stored, err = investigation.ReadAssistantQueryByPlanTx(ctx, tx, j, result.PlanDigest)
			if errors.Is(err, pgx.ErrNoRows) {
				stored = investigation.BuildStoredQueryResult(result)
				return nil
			}
			return err
		})
		return stored, err
	}
	load := func() error {
		return persistence.WithTenantTx(ctx, h.Pool, j.TenantID, func(tx pgx.Tx) error {
			if err := investigation.CheckAssistantJobTx(ctx, tx, j); err != nil {
				return err
			}
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT input FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2 AND input_digest=$3`, j.TenantID, j.Assistant.TurnID, j.Assistant.InputDigest).Scan(&raw); err != nil {
				return err
			}
			if investigation.ValidateAssistantHistoryTx(ctx, tx, j, raw) != nil || json.Unmarshal(raw, &input) != nil || graph.ScopeDigest(input) != j.Assistant.InputDigest {
				return investigation.ErrDenied
			}
			return nil
		})
	}
	switch name {
	case "get_formal_references":
		var params struct {
			Revision int64 `json:"rcaRevision"`
		}
		if json.Unmarshal(args, &params) != nil {
			return out, investigation.ErrInvalid
		}
		err := persistence.WithTenantTx(ctx, h.Pool, j.TenantID, func(tx pgx.Tx) error {
			if investigation.CheckAssistantJobTx(ctx, tx, j) != nil {
				return investigation.ErrDenied
			}
			data, refs, err := investigation.ReadFormalReferencesTx(ctx, tx, j, params.Revision)
			if err == nil {
				out.Data = data
				out.EvidenceRefs = refs
			}
			return err
		})
		if err != nil {
			return out, err
		}
	case "get_assistant_context":
		if err := load(); err != nil {
			return out, err
		}
		data := map[string]any{}
		if json.Unmarshal(rawJSON(input), &data) != nil {
			return out, investigation.ErrInvalid
		}
		if j.Assistant.AuthorizationDomain == "operational" {
			sources, err := h.assistantSourceCapabilities(ctx, j)
			data["sourceCapabilities"] = sources
			data["sourceCapabilityStatus"] = "available"
			if err != nil {
				data["sourceCapabilities"] = []analytics.SourceCapability{}
				data["sourceCapabilityStatus"] = "unavailable"
			}
		}
		out.Data = data
	case "describe_analysis_dataset":
		var p struct {
			DatasetID string `json:"datasetId"`
		}
		if json.Unmarshal(args, &p) != nil {
			return out, analytics.ErrUnsupported
		}
		catalog, err := analytics.Catalog()
		if err != nil {
			return out, err
		}
		filtered := []analytics.Dataset{}
		for _, dataset := range catalog.Datasets {
			if dataset.ID == p.DatasetID && dataset.AuthorizationDomain == j.Assistant.AuthorizationDomain {
				filtered = append(filtered, dataset)
			}
		}
		if len(filtered) == 0 {
			return out, analytics.ErrUnsupported
		}
		catalog.Datasets = filtered
		out.Data = catalog
	case "execute_analysis_query":

		var p struct {
			Intent json.RawMessage `json:"intent"`
		}
		if json.Unmarshal(args, &p) != nil {
			return out, investigation.ErrInvalid
		}
		if err := load(); err != nil {
			return out, err
		}
		intent, err := analytics.DecodeIntent(p.Intent)
		if err != nil {
			return out, err
		}
		if intent.DatasetID != "resources.current" && input.RequestedTimeRange != nil {
			window := input.RequestedTimeRange
			if intent.TimeRange == nil || !intent.TimeRange.From.Equal(window.From) || !intent.TimeRange.To.Equal(window.To) || intent.TimeRange.Timezone != window.Timezone {
				return out, investigation.ErrInvalid
			}
		}
		if strings.HasPrefix(intent.DatasetID, "metrics.") || strings.HasPrefix(intent.DatasetID, "logs.") {
			if j.Assistant.AuthorizationDomain != "operational" {
				return out, investigation.ErrDenied
			}
			route, err := (graph.Repository{Pool: h.Pool}).Load(ctx, j.TenantID.String(), j.Scope.Cluster)
			if err != nil || !route.ExpiresAt.After(time.Now()) || h.EndpointAllowed == nil || !h.EndpointAllowed(route.Endpoint) {
				return out, graph.ErrNotReady
			}
			payload := rawJSON(map[string]any{"intent": intent, "ownerEpoch": route.Epoch})
			reply, err := h.callWithGrantDigest(ctx, route.Endpoint, "/internal/v1/analysis:query", "platform-analysis-query", j.Subject, j.Scope, payload, j.Assistant.GrantSnapshotDigest)
			if err != nil {
				return out, err
			}
			var answer struct {
				Result analytics.QueryResult `json:"result"`
				Scope  graph.Scope           `json:"scope"`
			}
			if json.Unmarshal(reply, &answer) != nil || contract.Validate("https://ops.local/schemas/query-result/v1", rawJSON(answer.Result)) != nil || answer.Result.SourceManifest == nil || answer.Result.DatasetID != intent.DatasetID || answer.Result.CatalogDigest != j.Assistant.DatasetCatalogDigest || !graph.ContainsScope(j.Scope, answer.Scope) || answer.Result.ScopeDigest != graph.ScopeDigest(answer.Scope) {
				return out, investigation.ErrDenied
			}
			final, err := (graph.Repository{Pool: h.Pool}).Load(ctx, j.TenantID.String(), j.Scope.Cluster)
			if err != nil || final.Epoch != route.Epoch || final.Instance != route.Instance || final.LeaseUID != route.LeaseUID || !final.ExpiresAt.After(time.Now()) {
				return out, graph.ErrNotReady
			}
			stored, err := storedResult(answer.Result)
			if err != nil {
				return out, err
			}
			out.Data = map[string]any{"storedQueryResult": stored, "queryResultRefs": []uuid.UUID{stored.QueryResultID}, "scope": answer.Scope}
			out.Partial = !stored.Result.Exact
			out.DegradedSources = stored.Result.DegradedSources
			return out, nil
		}
		if intent.DatasetID != "resources.current" {
			binding := analytics.Binding{Subject: j.Subject, Grant: j.Assistant.GrantSnapshot, Selection: analytics.Selection{Namespaces: j.Scope.Namespaces, CanonicalIDs: j.Scope.Resources, IncludeClusterScoped: j.Scope.ClusterScoped}}
			plan, err := analytics.CompileControlPlane(intent, binding, j.Assistant.AuthorizationDomain)
			if err != nil {
				return out, err
			}
			executor := analytics.ControlPlaneExecutor{Pool: h.Pool, AuthorizeTx: func(ctx context.Context, tx pgx.Tx, subject, domain string, scope graph.Scope, digest string) error {
				var current graph.Scope
				var err error
				if domain == "audit" {
					current, err = investigation.AuditScopeTx(ctx, tx, j.TenantID, subject)
				} else {
					current, err = graph.EffectiveTx(ctx, tx, j.TenantID.String(), subject, scope.Cluster, true)
				}
				if err != nil || graph.ScopeDigest(current) != digest || !graph.ContainsScope(current, scope) {
					return investigation.ErrDenied
				}
				return nil
			}}
			result, err := executor.Execute(ctx, plan)
			if err != nil {
				return out, err
			}
			stored, err := storedResult(result)
			if err != nil {
				return out, err
			}
			out.Data = map[string]any{"storedQueryResult": stored, "queryResultRefs": []uuid.UUID{stored.QueryResultID}, "scope": plan.ExecutionScope()}
			out.Partial = !stored.Result.Exact
			return out, nil
		}
		if j.Assistant.AuthorizationDomain != "operational" {
			return out, analytics.ErrUnsupported
		}
		route, err := (graph.Repository{Pool: h.Pool}).Load(ctx, j.TenantID.String(), j.Scope.Cluster)
		if err != nil {
			return out, err
		}
		plan, err := analytics.Compile(intent, analytics.Binding{Subject: j.Subject, Grant: j.Assistant.GrantSnapshot, Selection: analytics.Selection{Namespaces: j.Scope.Namespaces, CanonicalIDs: j.Scope.Resources, IncludeClusterScoped: j.Scope.ClusterScoped}, OwnerEpoch: route.Epoch})
		if err != nil {
			return out, err
		}
		result, err := (analytics.Executor{Authorization: graph.Authorization{Pool: h.Pool}, Graph: assistantGraphReader{h, j}}).Execute(ctx, plan)
		if err != nil {
			return out, err
		}
		stored, err := storedResult(result)
		if err != nil {
			return out, err
		}
		out.Data = map[string]any{"storedQueryResult": stored, "queryResultRefs": []uuid.UUID{stored.QueryResultID}, "scope": plan.ExecutionScope()}
		out.Partial = !stored.Result.Exact
		out.DegradedSources = stored.Result.DegradedSources
	case "get_resource_investigation_context":
		if err := load(); err != nil {
			return out, err
		}
		if input.Binding.ResourceContext == nil {
			return out, analytics.ErrUnsupported
		}
		ids := []string{}
		for _, resource := range input.Binding.ResourceContext.Resources {
			ids = append(ids, resource.CanonicalID)
		}
		related, truncated, err := a.relatedIncidents(ctx, j, ids)
		if err != nil {
			return out, err
		}
		out.Data = map[string]any{"context": input.Binding.ResourceContext, "relatedIncidents": related, "relatedIncidentsTruncated": truncated}
		out.Partial = truncated
	case "resolve_resources":
		var p struct {
			Selector assistant.ResourceSelector `json:"selector"`
		}
		if json.Unmarshal(args, &p) != nil {
			return out, investigation.ErrInvalid
		}
		route, err := (graph.Repository{Pool: h.Pool}).Load(ctx, j.TenantID.String(), j.Scope.Cluster)
		if err != nil {
			return out, err
		}
		resolution, err := (assistant.ResourceResolver{Authorization: graph.Authorization{Pool: h.Pool}, Graph: assistantGraphReader{h, j}}).Resolve(ctx, analytics.Binding{Subject: j.Subject, Grant: j.Assistant.GrantSnapshot, Selection: analytics.Selection{Namespaces: j.Scope.Namespaces, CanonicalIDs: j.Scope.Resources, IncludeClusterScoped: j.Scope.ClusterScoped}, OwnerEpoch: route.Epoch}, p.Selector)
		if err != nil {
			return out, err
		}
		out.Data = resolution
	case "resource_findings":
		var p struct {
			CanonicalID string `json:"canonicalId"`
		}
		if json.Unmarshal(args, &p) != nil {
			return out, investigation.ErrInvalid
		}
		records := []map[string]any{}
		err := persistence.WithTenantTx(ctx, h.Pool, j.TenantID, func(tx pgx.Tx) error {
			if err := investigation.CheckAssistantJobTx(ctx, tx, j); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `SELECT finding_id,lifecycle_state,COALESCE(payload->>'severity','unknown'),observed_at FROM finding.records WHERE tenant_id=$1 AND resource_canonical_id=$2 ORDER BY observed_at DESC,finding_id LIMIT 50`, j.TenantID, p.CanonicalID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				var state, severity string
				var at time.Time
				if err := rows.Scan(&id, &state, &severity, &at); err != nil {
					return err
				}
				records = append(records, map[string]any{"findingId": id, "state": state, "severity": severity, "observedAt": at})
			}
			return rows.Err()
		})
		if err != nil {
			return out, err
		}
		out.Data = map[string]any{"findings": records, "limit": 50}
	case "get_resource_context", "get_resource_health", "query_kubernetes", "get_dependencies", "get_impact_scope":
		var p struct {
			CanonicalID string `json:"canonicalId"`
			ResourceID  string `json:"resourceCanonicalId"`
			Depth       int    `json:"depth"`
		}
		if json.Unmarshal(args, &p) != nil {
			return out, investigation.ErrInvalid
		}
		id := p.CanonicalID
		if id == "" {
			id = p.ResourceID
		}
		route, err := (graph.Repository{Pool: h.Pool}).Load(ctx, j.TenantID.String(), j.Scope.Cluster)
		if err != nil {
			return out, err
		}
		q := graph.Query{QueryKind: "entity", Scope: j.Scope, CanonicalID: id, ExpectedOwnerEpoch: route.Epoch, MaxDepth: 1, MaxNodes: 200, MaxEdges: 400, RelationKinds: []string{}}
		if name == "get_dependencies" {
			q.QueryKind = "neighbors"
			q.Direction = "both"
			q.MaxDepth = p.Depth
			if q.MaxDepth < 1 {
				q.MaxDepth = 1
			}
		}
		if name == "get_impact_scope" {
			q.QueryKind = "impact"
			q.MaxDepth = 2
		}
		result, err := (assistantGraphReader{h, j}).Query(ctx, q)
		if err != nil {
			return out, err
		}
		data := map[string]any{}
		if json.Unmarshal(rawJSON(result), &data) != nil {
			return out, investigation.ErrInvalid
		}
		relations := []map[string]any{}
		for _, relation := range result.Edges {
			relations = append(relations, map[string]any{"from": relation.From, "to": relation.To, "kind": relation.Kind, "relationDigest": graph.ScopeDigest(relation), "observedAt": relation.ObservedAt})
		}
		data["relationReferences"] = relations
		out.Data = data
		out.Partial = result.Partial
		out.DegradedSources = result.DegradedSources
	default:
		return a.Legacy.Invoke(ctx, j, name, args)
	}
	return out, nil
}
