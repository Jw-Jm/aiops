package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"net/url"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
	"ops-platform/internal/rca"
	"slices"
	"strconv"
	"strings"
	"time"
)

// InvestigationTools invokes the existing authorized domain handlers in process.
// It never constructs a source client or accepts a caller-provided endpoint.
type InvestigationTools struct {
	SP04 *SP04Handlers
	SP05 *SP05Handlers
}

func (a InvestigationTools) CheckScope(ctx context.Context, j investigation.Job, name string, args json.RawMessage) error {
	if err := tools.CheckArguments(j, name, args); err != nil {
		return err
	}
	if a.SP04 == nil || a.SP04.Pool == nil {
		return investigation.ErrDenied
	}
	var p struct {
		CanonicalID string `json:"canonicalId"`
		ResourceID  string `json:"resourceCanonicalId"`
	}
	if json.Unmarshal(args, &p) != nil {
		return investigation.ErrInvalid
	}
	id := p.CanonicalID
	if id == "" {
		id = p.ResourceID
	}
	if id == "" {
		return nil
	}
	return persistence.WithTenantTx(ctx, a.SP04.Pool, j.TenantID, func(tx pgx.Tx) error {
		var namespace string
		if err := tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, id).Scan(&namespace); err != nil {
			return investigation.ErrDenied
		}
		if !j.Scope.Allows(id, namespace) {
			return investigation.ErrDenied
		}
		return nil
	})
}

type boundedResponse struct {
	header http.Header
	status int
	b      bytes.Buffer
	err    error
}

func (w *boundedResponse) Header() http.Header { return w.header }
func (w *boundedResponse) WriteHeader(n int) {
	if w.status == 0 {
		w.status = n
	}
}
func (w *boundedResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.b.Len()+len(b) > 64<<10 {
		w.err = investigation.ErrBudget
		return 0, w.err
	}
	return w.b.Write(b)
}
func (a InvestigationTools) read(ctx context.Context, j investigation.Job, path string, q url.Values, body any) (json.RawMessage, error) {
	if a.SP04 == nil || a.SP05 == nil {
		return nil, graph.ErrNotReady
	}
	actor := auth.RequestContext{TenantID: j.TenantID, Subject: j.Subject, Roles: []auth.Role{auth.Operator}, RequestID: j.JobID.String()}
	method := "GET"
	var payload io.Reader
	if body != nil {
		method = "POST"
		b, _ := json.Marshal(body)
		payload = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(auth.WithRequestContext(ctx, actor), method, path+"?"+q.Encode(), payload)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Idempotency-Key", graph.ScopeDigest(struct {
		Job  string
		Path string
		Body any
	}{j.JobID.String(), path, body}))
	w := &boundedResponse{header: http.Header{}}
	if strings.HasPrefix(path, "/api/v1/incidents") || strings.HasPrefix(path, "/api/v1/findings") {
		a.SP05.ServeHTTP(w, r)
	} else {
		a.SP04.ServeHTTP(w, r)
	}
	if w.err != nil {
		return nil, w.err
	}
	var envelope struct {
		Data      json.RawMessage `json:"data"`
		ErrorCode string          `json:"code"`
	}
	if json.Unmarshal(w.b.Bytes(), &envelope) != nil {
		return nil, investigation.ErrInvalid
	}
	if w.status != 200 {
		if w.status == 403 || w.status == 401 {
			return nil, investigation.ErrDenied
		}
		if w.status == 404 {
			return nil, pgx.ErrNoRows
		}
		return nil, graph.ErrNotReady
	}
	return envelope.Data, nil
}
func (a InvestigationTools) Invoke(ctx context.Context, j investigation.Job, name string, args json.RawMessage) (tools.Result, error) {
	out := tools.Result{SchemaVersion: "tool-response/v2", Tool: name, State: "succeeded", Data: map[string]any{}, EvidenceRefs: []string{}, DegradedSources: []string{}}
	if err := a.CheckScope(ctx, j, name, args); err != nil {
		return out, err
	}
	var p struct {
		IncidentID    string `json:"incidentId"`
		CanonicalID   string `json:"canonicalId"`
		ResourceID    string `json:"resourceCanonicalId"`
		Depth         int    `json:"depth"`
		Type          string `json:"type"`
		Operation     string `json:"operation"`
		Component     string `json:"component"`
		QueryTemplate string `json:"queryTemplate"`
		TimeRange     *struct {
			From time.Time `json:"from"`
			To   time.Time `json:"to"`
		} `json:"timeRange"`
		Budget *struct {
			MaxBytes  int `json:"maxBytes"`
			TimeoutMs int `json:"timeoutMs"`
		} `json:"budget"`
	}
	if json.Unmarshal(args, &p) != nil {
		return out, investigation.ErrInvalid
	}
	if p.IncidentID != "" && p.IncidentID != j.IncidentID.String() {
		return out, investigation.ErrDenied
	}
	id := p.CanonicalID
	if id == "" {
		id = p.ResourceID
	}
	if id != "" {
		var ns string
		err := persistence.WithTenantTx(ctx, a.SP04.Pool, j.TenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, id).Scan(&ns)
		})
		if err != nil || !j.Scope.Allows(id, ns) {
			return out, investigation.ErrDenied
		}
	}
	if name == "query_kubernetes" && !strings.HasPrefix(id, "k8s+v1://") {
		return out, investigation.ErrInvalid
	}
	q := url.Values{"clusterUid": {j.Scope.Cluster}, "limit": {"100"}}
	if id != "" {
		q.Set("canonicalId", id)
	}
	var data json.RawMessage
	var err error
	incidentPath := "/api/v1/incidents/" + j.IncidentID.String()
	switch name {
	case "get_incident_context":
		data, err = a.read(ctx, j, incidentPath, q, nil)
		if err == nil {
			var i any
			_ = json.Unmarshal(data, &i)
			f, e := a.findings(ctx, j, "")
			if e != nil {
				return out, e
			}
			out.Data = map[string]any{"incident": i, "findings": f}
			data = nil
		}
	case "get_findings":
		out.Data, err = a.findings(ctx, j, id)
	case "get_resource_context", "get_resource_health", "query_kubernetes":
		data, err = a.read(ctx, j, "/api/v1/resources/by-canonical-id", q, nil)
	case "get_dependencies":
		q.Set("depth", strconv.Itoa(p.Depth))
		q.Set("direction", "both")
		data, err = a.read(ctx, j, "/api/v1/resources/neighbors", q, nil)
	case "get_impact_scope":
		data, err = a.read(ctx, j, "/api/v1/resources/impact-scope", q, nil)
	case "get_ranked_evidence", "get_evidence_conflicts", "get_root_cause_candidates":
		data, err = a.read(ctx, j, incidentPath+"/rca", q, nil)
		if errors.Is(err, pgx.ErrNoRows) {
			out.Partial = true
			out.State = "partial"
			out.Data = map[string]any{"items": []any{}, "reason": "rca_not_available"}
			err = nil
		} else if err == nil {
			var view struct {
				Revision rca.Revision `json:"revision"`
				Eligible bool         `json:"currentEligible"`
				Degraded []string     `json:"currentDegradedSources"`
			}
			if json.Unmarshal(data, &view) != nil {
				return out, investigation.ErrInvalid
			}
			out.Partial = !view.Eligible
			out.DegradedSources = view.Degraded
			if out.Partial {
				out.State = "partial"
			}
			switch name {
			case "get_ranked_evidence":
				items := []map[string]any{}
				known := map[string]bool{}
				for _, e := range view.Revision.InputManifest.Evidence {
					known[e.EvidenceID] = true
				}
				for _, ranked := range view.Revision.Result.Bundle.Ranked {
					entryRaw, _ := json.Marshal(ranked)
					entry := map[string]any{}
					_ = json.Unmarshal(entryRaw, &entry)
					if known[ranked.NodeID] {
						entry["evidenceId"] = ranked.NodeID
						out.EvidenceRefs = append(out.EvidenceRefs, ranked.NodeID)
					}
					items = append(items, entry)
				}
				out.Data = map[string]any{"items": items}
			case "get_evidence_conflicts":
				out.Data = map[string]any{"items": view.Revision.Result.Bundle.Conflicts}
			case "get_root_cause_candidates":
				out.Data = map[string]any{"items": view.Revision.Result.Candidates, "recipeVersion": view.Revision.Result.RecipeVersion, "recipeDigest": view.Revision.Result.RecipeDigest, "currentEligible": view.Eligible, "rcaRevision": view.Revision.Revision}
			}
			data = nil

		}
	case "get_recent_changes":
		data, err = a.read(ctx, j, incidentPath+"/evidence", q, nil)
		if err == nil {
			var items []evidence.Evidence
			if json.Unmarshal(data, &items) != nil {
				return out, investigation.ErrInvalid
			}
			changes := []evidence.Evidence{}
			for _, e := range items {
				if (e.Type == "change" || e.Type == "change_event") && p.TimeRange != nil && !e.ObservedFrom.Before(p.TimeRange.From) && !e.ObservedTo.After(p.TimeRange.To) {
					changes = append(changes, e)
					out.EvidenceRefs = append(out.EvidenceRefs, e.EvidenceID)
				}
			}
			out.Data = map[string]any{"items": changes}
			data = nil
		}
	case "query_metrics", "query_logs":
		if p.Budget == nil || p.TimeRange == nil {
			return out, investigation.ErrInvalid
		}
		data, err = a.read(ctx, j, "/api/v1/evidence:query", q, map[string]any{"resourceCanonicalId": id, "type": p.Type, "timeRange": p.TimeRange, "queryTemplate": p.QueryTemplate, "budget": map[string]int{"maxBytes": min(p.Budget.MaxBytes, 32<<10), "timeoutMs": min(p.Budget.TimeoutMs, 2500)}, "limit": 100})
	case "query_hardware":
		if !strings.HasPrefix(id, "hardware+v1://") {
			return out, investigation.ErrInvalid
		}
		if p.Component == "bmc events" {
			out.State = "degraded"
			out.Partial = true
			out.DegradedSources = []string{"redfish-bmc-events"}
			out.ErrorCode = "CAPABILITY_UNAVAILABLE"
			out.Data = map[string]any{"component": p.Component, "reason": "BMC event collection is not an admitted source capability"}
			return out, nil
		}
		now := time.Now().UTC()
		data, err = a.read(ctx, j, "/api/v1/evidence:query", q, map[string]any{"resourceCanonicalId": id, "type": "hardware", "timeRange": map[string]time.Time{"from": now.Add(-5 * time.Minute), "to": now}, "queryTemplate": "hardware-health/v1", "budget": map[string]int{"maxBytes": 32 << 10, "timeoutMs": 2500}, "limit": 100})
	case "query_deepflow":
		if p.TimeRange == nil || id == "" {
			return out, investigation.ErrInvalid
		}
		templates := map[string]string{"get network health": "GetResourceNetworkHealth", "get dependencies": "GetNetworkDependencies", "find retransmission": "FindTCPRetransmission", "find packet loss": "FindPacketLoss", "find connection failure": "FindConnectionFailure", "get network path": "GetNetworkPath"}
		data, err = a.read(ctx, j, "/api/v1/evidence:query", q, map[string]any{"resourceCanonicalId": id, "type": "deepflow", "timeRange": p.TimeRange, "queryTemplate": templates[p.Operation], "budget": map[string]int{"maxBytes": 32 << 10, "timeoutMs": 2500}, "limit": 100})
	default:
		return out, investigation.ErrDenied
	}
	if err != nil {
		if errors.Is(err, investigation.ErrDenied) {
			return out, err
		}
		out.State = "degraded"
		out.Partial = true
		out.DegradedSources = []string{name}
		out.ErrorCode = "SOURCE_DEGRADED"
		return out, nil
	}
	if data != nil {
		var v any
		if json.Unmarshal(data, &v) != nil {
			return out, investigation.ErrInvalid
		}
		out.Data = v
		var result struct {
			Partial         bool                `json:"partial"`
			DegradedSources []string            `json:"degradedSources"`
			Evidence        []evidence.Evidence `json:"evidence"`
		}
		_ = json.Unmarshal(data, &result)
		out.Partial = result.Partial
		out.DegradedSources = result.DegradedSources
		if out.Partial {
			out.State = "partial"
		}
		if name == "query_hardware" {
			hardwareProjection(&out, p.Component, result.Evidence)
		}
		for _, e := range result.Evidence {
			if !slices.Contains(j.Budget.AllowedDataClasses, e.DataClass) {
				return out, investigation.ErrDenied
			}
			out.EvidenceRefs = append(out.EvidenceRefs, e.EvidenceID)
		}
	}
	encoded, _ := json.Marshal(out.Data)
	var value any
	_ = json.Unmarshal(encoded, &value)
	refs := map[string]bool{}
	for _, ref := range out.EvidenceRefs {
		refs[ref] = true
	}
	var collect func(any)
	collect = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if k == "evidenceId" {
					if id, ok := v.(string); ok && !refs[id] {
						refs[id] = true
						out.EvidenceRefs = append(out.EvidenceRefs, id)
					}
				}
				if k == "evidenceRefs" {
					if ids, ok := v.([]any); ok {
						for _, id := range ids {
							if s, ok := id.(string); ok && !refs[s] {
								refs[s] = true
								out.EvidenceRefs = append(out.EvidenceRefs, s)
							}
						}
					}
				}
				collect(v)
			}
		case []any:
			for _, v := range x {
				collect(v)
			}
		}
	}
	collect(value)
	return out, nil
}
func (a InvestigationTools) findings(ctx context.Context, j investigation.Job, canonical string) (any, error) {
	out := []finding.Finding{}
	err := persistence.WithTenantTx(ctx, a.SP05.Pool, j.TenantID, func(tx pgx.Tx) error {
		var rows pgx.Rows
		var e error
		if canonical != "" {
			rows, e = tx.Query(ctx, `SELECT finding_id::text FROM finding.records WHERE tenant_id=$1 AND resource_canonical_id=$2 ORDER BY finding_id LIMIT 101`, j.TenantID, canonical)
		} else {
			rows, e = tx.Query(ctx, `SELECT finding_id::text FROM incident.finding_links WHERE tenant_id=$1 AND incident_id=$2 ORDER BY finding_id LIMIT 101`, j.TenantID, j.IncidentID)
		}
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(ids) > 100 {
			return investigation.ErrBudget
		}
		for _, id := range ids {
			f, e := finding.Load(ctx, tx, j.TenantID, id)
			if e != nil {
				return e
			}
			if !j.Scope.Allows(f.ResourceCanonicalID, f.Namespace) {
				return investigation.ErrDenied
			}
			out = append(out, f)
		}
		return nil
	})
	return map[string]any{"items": out}, err
}

// Project presentation facts without modifying an immutable Evidence payload or
// its content digest. References still resolve to the original authorized read.
func hardwareProjection(out *tools.Result, component string, source []evidence.Evidence) {
	kinds := map[string]string{"server health": "PhysicalServer", "cpu": "CPU", "memory": "DIMM", "nic": "NIC", "disk": "Disk", "psu": "PSU", "fan": "Fan"}
	facts := []any{}
	for _, e := range source {
		var raw struct {
			Entities []map[string]any `json:"entities"`
		}
		if json.Unmarshal(e.FactSlice, &raw) != nil {
			continue
		}
		for _, entity := range raw.Entities {
			if entity["kind"] == kinds[component] {
				facts = append(facts, entity)
			}
		}
	}
	out.Data = map[string]any{"component": component, "facts": facts}
	if len(facts) == 0 {
		out.Partial = true
		out.State = "partial"
		out.DegradedSources = append(out.DegradedSources, "hardware-component-not-observed")
	}
}
