package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/deepflow"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
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
	// A persisted native task carries its own authority; without a browser session
	// the pre-check must re-prove that task instead of reading the empty legacy
	// binding table. An unknown source or a task without a persisted id fails
	// closed rather than degrading to legacy.
	authorityContext := ctx
	proof := nativeTaskProof(a.Job)
	if a.Job.Assistant != nil {
		switch a.Job.Assistant.AuthorizationSource {
		case investigation.AuditSourceNative:
			if proof == nil {
				return out, investigation.ErrDenied
			}
			authorityContext = graph.WithTaskAuthority(ctx, a.Job.JobID)
		case investigation.AuditSourceLegacy, "":
			if proof != nil {
				return out, investigation.ErrDenied
			}
		default:
			return out, investigation.ErrDenied
		}
	}
	current, err := (graph.Authorization{Pool: h.Pool}).Effective(authorityContext, q.Scope.Tenant, a.Job.Subject, q.Scope.Cluster)
	if err != nil || a.Job.Assistant == nil || graph.ScopeDigest(current) != a.Job.Assistant.GrantSnapshotDigest {
		return out, investigation.ErrDenied
	}
	route, err := (graph.Repository{Pool: h.Pool}).Load(ctx, q.Scope.Tenant, q.Scope.Cluster)
	if err != nil || route.Epoch != q.ExpectedOwnerEpoch || !route.ExpiresAt.After(time.Now()) || !h.EndpointAllowed(route.Endpoint) {
		return out, graph.ErrNotReady
	}
	reply, err := h.callWithGrantDigest(authorityContext, route.Endpoint, "/internal/v1/graph:query", "platform-graph-query", a.Job.Subject, q.Scope, rawJSON(q), a.Job.Assistant.GrantSnapshotDigest, proof)
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

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

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
			if (input.FaultChainRequest != nil) != (j.Assistant.ExecutionMode == "evidence_only") || input.FaultChainRequest != nil && graph.ScopeDigest(*input.FaultChainRequest) != j.Assistant.FaultChainRequestDigest {
				return investigation.ErrDenied
			}
			return nil
		})
	}
	switch name {
	case "query_deepflow":
		var p struct {
			Operation  string                    `json:"operation"`
			ResourceID string                    `json:"resourceCanonicalId"`
			TimeRange  *analytics.QueryTimeRange `json:"timeRange"`
		}
		if json.Unmarshal(args, &p) != nil {
			return out, investigation.ErrInvalid
		}
		if p.Operation != "get dependencies" {
			return a.Legacy.Invoke(ctx, j, name, args)
		}
		if err := load(); err != nil {
			return out, err
		}
		var namespace string
		err := persistence.WithTenantTx(ctx, h.Pool, j.TenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, p.ResourceID).Scan(&namespace)
		})
		if err != nil {
			return out, investigation.ErrDenied
		}
		j.Scope, err = faultChainRelationScope(input, j, p.ResourceID, p.TimeRange, namespace)
		if err != nil {
			return out, err
		}
		result, err := a.Legacy.Invoke(ctx, j, name, args)
		if err != nil || result.State != "succeeded" && result.State != "partial" {
			return result, err
		}
		var observations evidence.Result
		if json.Unmarshal(rawJSON(result.Data), &observations) != nil {
			return out, investigation.ErrInvalid
		}
		edges, err := deepflow.NetworkRelations(observations)
		if err != nil {
			return out, err
		}
		// Detail rows are observations, not additional edges or population counts.
		// Retain the latest original timestamp for each directed pair.
		unique := []resource.Relation{}
		pairs := map[[2]string]int{}
		for _, edge := range edges {
			key := [2]string{edge.From, edge.To}
			if i, ok := pairs[key]; ok {
				if edge.ObservedAt.After(unique[i].ObservedAt) {
					unique[i] = edge
				}
			} else {
				pairs[key] = len(unique)
				unique = append(unique, edge)
			}
		}
		edges = unique
		nodes := []map[string]string{}
		refs := []map[string]any{}
		seen := map[string]bool{}
		for _, edge := range edges {
			for _, canonical := range []string{edge.From, edge.To} {
				if seen[canonical] {
					continue
				}
				parsed, err := resource.ParseCanonicalID(canonical)
				if err != nil || parsed.Kind != "Pod" || !j.Scope.Allows(canonical, namespace) {
					return out, investigation.ErrDenied
				}
				seen[canonical] = true
				nodes = append(nodes, map[string]string{"canonicalId": canonical, "namespace": namespace, "kind": "Pod"})
			}
			refs = append(refs, map[string]any{"from": edge.From, "to": edge.To, "kind": edge.Kind, "relationDigest": graph.ScopeDigest(edge), "observedAt": edge.ObservedAt})
		}
		data := map[string]any{}
		_ = json.Unmarshal(rawJSON(result.Data), &data)
		data["nodes"], data["edges"], data["relationReferences"] = nodes, edges, refs
		result.Data = data
		// A bounded prefix can establish individual relations, never coverage.
		result.State = "succeeded"
		return result, nil
	case "evaluate_fault_chain":
		if err := load(); err != nil {
			return out, err
		}
		request, err := faultChainEvaluationRequest(input, j)
		if err != nil {
			return out, err
		}
		context, err := a.evaluateFaultChain(ctx, j, request, args)
		if err != nil {
			return out, err
		}
		out.Data = map[string]any{"context": context, "contentDigest": graph.ScopeDigest(context), "queryResultRefs": context.QueryResultRefs}
		out.EvidenceRefs = context.EvidenceRefs
		out.Partial = context.Status != "complete"

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
		if j.Assistant.TaskKind == "data_query" {
			catalog, err := analytics.Catalog()
			if err != nil {
				return out, err
			}
			datasets := []map[string]string{}
			for _, dataset := range catalog.Datasets {
				if dataset.AuthorizationDomain == j.Assistant.AuthorizationDomain {
					datasets = append(datasets, map[string]string{"datasetId": dataset.ID, "name": dataset.Name, "description": dataset.Description})
				}
			}
			data["analysisDatasets"] = datasets
		}
		if j.Assistant.AuthorizationDomain == "operational" {
			details, err := h.AssistantCapabilityDetails(ctx, j)
			data["sourceCapabilities"] = []analytics.SourceCapability{}
			data["networkCapabilities"] = []evidence.CapabilitySet{}
			if err == nil {
				data["sourceCapabilities"] = details.Capabilities
				data["networkCapabilities"] = details.Network
			}
			data["sourceCapabilityStatus"] = "available"
			if err != nil {
				data["sourceCapabilities"] = []analytics.SourceCapability{}
				data["sourceCapabilityStatus"] = "unavailable"
				// The committed history still answers the turn; record why the
				// auxiliary capability read degraded instead of hiding it.
				slog.Warn("assistant context capability unavailable", "error", err.Error())
			}
		}
		if request, err := faultChainEvaluationRequest(input, j); err == nil {
			data["faultChainEvaluationRequest"] = request
		}
		for i := len(input.History) - 1; i >= 0; i-- {
			if input.History[i].Role != "assistant" {
				continue
			}
			var prior investigation.AssistantAnswer
			if json.Unmarshal(input.History[i].Content, &prior) != nil || prior.FaultChainContextRef == nil {
				continue
			}
			err := persistence.WithTenantTx(ctx, h.Pool, j.TenantID, func(tx pgx.Tx) error {
				readJob, err := investigation.AssistantReadJobTx(ctx, tx, j.TenantID, j.Subject, j.Assistant.ConversationID, input.History[i].TurnID, j.Scope)
				if err != nil {
					return err
				}
				historical, err := investigation.ReadFaultChainContextTx(ctx, tx, readJob, *prior.FaultChainContextRef)
				if err != nil {
					return err
				}
				stored := []investigation.StoredQueryResult{}
				bytes := 0
				status := "historical"
				for _, id := range historical.QueryResultRefs {
					q, err := investigation.ReadAssistantQueryResultTx(ctx, tx, readJob, id)
					if err != nil {
						return err
					}
					size := len(rawJSON(q))
					if len(stored) >= 8 || bytes+size > 24<<10 {
						status = "partial"
						break
					}
					bytes += size
					stored = append(stored, q)
				}
				data["historicalFaultChainContext"] = historical
				data["historicalFaultChainQueryResults"] = stored
				data["historicalFaultChainQueryResultsStatus"] = status
				return nil
			})
			if err != nil {
				data["historicalFaultChainContextStatus"] = "unavailable"
			} else {
				data["historicalFaultChainContextStatus"] = "historical"
			}
			break
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
			Intent      json.RawMessage `json:"intent"`
			CanonicalID string          `json:"canonicalId"`
			WindowRole  string          `json:"windowRole"`
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
		queryScope := j.Scope
		if p.CanonicalID != "" {
			var ns string
			err := persistence.WithTenantTx(ctx, h.Pool, j.TenantID, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, p.CanonicalID).Scan(&ns)
			})
			if err != nil || !j.Scope.Allows(p.CanonicalID, ns) {
				return out, investigation.ErrDenied
			}
			queryScope.Resources = []string{p.CanonicalID}
			queryScope.ClusterScoped = ns == ""
			queryScope.Namespaces = []string{}
			if ns != "" {
				queryScope.Namespaces = []string{ns}
			}
		}
		if p.WindowRole != "" {
			w, err := resourceWindow(input, j, p.WindowRole)
			if err != nil {
				return out, err
			}
			if intent.TimeRange == nil || *intent.TimeRange != w {
				return out, investigation.ErrDenied
			}
		}
		frozen := input.FaultChainRequest
		if frozen == nil {
			frozen = input.FrozenCorrelationRequest
		}
		if frozen != nil {
			if !frozen.Allows(intent.TimeRange) || intent.ComparisonTimeRange != nil && !frozen.Allows(intent.ComparisonTimeRange) {
				return out, investigation.ErrInvalid
			}
		} else if intent.DatasetID != "resources.current" && input.RequestedTimeRange != nil {
			window := input.RequestedTimeRange
			if intent.TimeRange == nil || !intent.TimeRange.From.Equal(window.From) || !intent.TimeRange.To.Equal(window.To) || intent.TimeRange.Timezone != window.Timezone {
				return out, investigation.ErrInvalid
			}
		}
		if strings.HasPrefix(intent.DatasetID, "metrics.") || strings.HasPrefix(intent.DatasetID, "logs.") || strings.HasPrefix(intent.DatasetID, "network.") {
			if j.Assistant.AuthorizationDomain != "operational" {
				return out, investigation.ErrDenied
			}
			route, err := (graph.Repository{Pool: h.Pool}).Load(ctx, j.TenantID.String(), j.Scope.Cluster)
			if err != nil || !route.ExpiresAt.After(time.Now()) || h.EndpointAllowed == nil || !h.EndpointAllowed(route.Endpoint) {
				return out, graph.ErrNotReady
			}
			payload := rawJSON(map[string]any{"intent": intent, "ownerEpoch": route.Epoch})
			reply, err := h.callWithGrantDigest(ctx, route.Endpoint, "/internal/v1/analysis:query", "platform-analysis-query", j.Subject, queryScope, payload, j.Assistant.GrantSnapshotDigest, nativeTaskProof(j))
			if err != nil {
				return out, err
			}
			var answer struct {
				Result       analytics.QueryResult `json:"result"`
				Scope        graph.Scope           `json:"scope"`
				EvidenceRefs []string              `json:"evidenceRefs"`
			}
			if json.Unmarshal(reply, &answer) != nil || contract.Validate("https://ops.local/schemas/query-result/v1", rawJSON(answer.Result)) != nil || answer.Result.SourceManifest == nil || answer.Result.DatasetID != intent.DatasetID || answer.Result.CatalogDigest != j.Assistant.DatasetCatalogDigest || !graph.ContainsScope(queryScope, answer.Scope) || answer.Result.ScopeDigest != graph.ScopeDigest(answer.Scope) {
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
			if !sameStringSet(answer.EvidenceRefs, answer.Result.DerivationEvidenceRefs) {
				return out, investigation.ErrDenied
			}
			out.EvidenceRefs = append(out.EvidenceRefs, stored.Result.DerivationEvidenceRefs...)
			data := map[string]any{"storedQueryResult": stored, "queryResultRefs": []uuid.UUID{stored.QueryResultID}, "scope": answer.Scope, "filters": intent.Filters}
			if p.CanonicalID != "" {
				data["canonicalId"] = p.CanonicalID
			}
			out.Data = data
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
	case "resource_findings", "resource_history", "resource_changes":
		if err := load(); err != nil {
			return out, err
		}
		return a.resourceHistory(ctx, j, name, args, input)
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
		if name == "get_dependencies" && j.Assistant.CatalogVersion == "assistant-tools/v2" {
			parsed, err := resource.ParseCanonicalID(id)
			if err == nil && parsed.Kind == "PersistentVolumeClaim" {
				q.QueryKind = "diagnostic"
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
