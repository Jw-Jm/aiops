package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"ops-platform/internal/analytics"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

type metricRequest struct {
	CanonicalID string          `json:"canonicalId"`
	Intent      json.RawMessage `json:"intent"`
}

func decodeMetricRequest(raw []byte) (metricRequest, error) {
	var q metricRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&q) != nil || decoder.Decode(new(any)) != io.EOF {
		return q, analytics.ErrInvalid
	}
	intent, err := analytics.DecodeIntent(q.Intent)
	if err != nil {
		return q, analytics.ErrInvalid
	}
	if intent.DatasetID != "metrics.pod-cpu" && intent.DatasetID != "metrics.pod-memory" && intent.DatasetID != "metrics.pod-restarts" && intent.DatasetID != "metrics.pod-phase" {
		return q, analytics.ErrUnsupported
	}
	if intent.Operation != "trend" && intent.Operation != "aggregate" && intent.Operation != "average" {
		return q, analytics.ErrUnsupported
	}
	return q, nil
}

// Interactive metrics reuse signed owner routing and native proof without creating an AI Job.
func (h *SP04Handlers) serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, _ := auth.RequestContextFromContext(r.Context())
	proof := nativeProofFromContext(r.Context())
	fail := func(status int, code string) {
		writeSP04Error(w, status, code, "指标查询不可用", status >= 500, actor.RequestID)
	}
	if proof == nil || !auth.HasRole(r.Context(), auth.Operator) {
		fail(403, "FORBIDDEN")
		return
	}
	cluster := r.URL.Query().Get("clusterUid")
	grant, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), actor.TenantID.String(), actor.Subject, cluster)
	if err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	canonical := r.URL.Query().Get("canonicalId")
	body := map[string]any{}
	switch {
	case r.URL.Path == "/api/v1/metrics/capabilities" && r.Method == "GET":
		body["capabilities"] = true
	case r.URL.Path == "/api/v1/metrics/query" && r.Method == "POST":
		raw, e := io.ReadAll(io.LimitReader(r.Body, 65537))
		if e != nil || len(raw) > 65536 {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		q, e := decodeMetricRequest(raw)
		if e != nil {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		canonical = q.CanonicalID
		body["intent"] = q.Intent
	default:
		fail(405, "INVALID_ARGUMENT")
		return
	}
	parsed, e := resource.ParseCanonicalID(canonical)
	if e != nil || parsed.Domain != "k8s" || parsed.APIGroup != "core" || parsed.Kind != "Pod" || parsed.Tenant != grant.Tenant || parsed.Scope != cluster {
		fail(403, "FORBIDDEN")
		return
	}
	scope, err := analytics.RestrictScope(grant, analytics.Selection{CanonicalIDs: []string{canonical}})
	if err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	owner, err := (graph.Repository{Pool: h.Pool}).Load(r.Context(), grant.Tenant, cluster)
	if err != nil || !owner.ExpiresAt.After(time.Now()) || h.EndpointAllowed == nil || !h.EndpointAllowed(owner.Endpoint) || h.Client == nil {
		fail(503, "GRAPH_NOT_READY")
		return
	}
	body["ownerEpoch"] = owner.Epoch
	reply, err := h.callWithGrantDigest(r.Context(), owner.Endpoint, "/internal/v1/analysis:query", "platform-analysis-query", actor.Subject, scope, rawJSON(body), graph.ScopeDigest(grant), proof)
	if err != nil {
		h.writeCallError(w, err, actor.RequestID)
		return
	}
	var out struct {
		Result             *analytics.QueryResult       `json:"result"`
		SourceCapabilities []analytics.SourceCapability `json:"sourceCapabilities"`
		ScopeDigest        string                       `json:"scopeDigest"`
	}
	if json.Unmarshal(reply, &out) != nil {
		fail(503, "SOURCE_CONTRACT_INVALID")
		return
	}
	if body["capabilities"] == true {
		if out.ScopeDigest != graph.ScopeDigest(scope) {
			fail(503, "SOURCE_CONTRACT_INVALID")
			return
		}
		selected := []analytics.SourceCapability{}
		for _, c := range out.SourceCapabilities {
			if c.SourceType == "victoriametrics" {
				selected = append(selected, c)
			}
		}
		writeJSON(w, 200, map[string]any{"requestId": actor.RequestID, "data": map[string]any{"sourceCapabilities": selected}})
		return
	}
	if out.Result == nil || out.Result.SourceManifest == nil || out.Result.ScopeDigest != graph.ScopeDigest(scope) {
		fail(503, "SOURCE_CONTRACT_INVALID")
		return
	}
	writeJSON(w, 200, map[string]any{"requestId": actor.RequestID, "data": out.Result})
}
