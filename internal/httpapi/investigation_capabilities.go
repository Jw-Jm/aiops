package httpapi

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	"ops-platform/internal/auth"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"time"
)

// Capabilities report observed degradation without requiring model readiness.
// The aggregate is limited to the authenticated subject; no foreign Job IDs or
// source data are exposed and API readiness remains a mainline property.
func (h *InvestigationHandlers) ServeCapabilities(w http.ResponseWriter, r *http.Request) {
	a, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		investigationError(w, auth.ErrUnauthenticated, "")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")
	status, reason := "disabled", "not-configured"
	if h != nil {
		status, reason = "unverified", "configured-awaiting-successful-job"
		var state, code string
		var id string
		err := persistence.WithTenantTx(r.Context(), h.Repository.Pool, a.TenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT job_id::text,state,error_code FROM investigation.jobs WHERE tenant_id=$1 AND requesting_subject=$2 AND schema_version='investigation-job/v2' ORDER BY updated_at DESC LIMIT 1`, a.TenantID, a.Subject).Scan(&id, &state, &code)
		})
		if err == nil {
			if _, currentErr := h.Repository.Get(r.Context(), a.TenantID, uuid.MustParse(id)); currentErr != nil {
				state, code = "", ""
			}
			if state == "failed" {
				status, reason = "degraded", code
			} else if state == "succeeded" || state == "partial" {
				status, reason = "unverified", "last-job-completed; model-readiness-not-probed"
			}
		}
	}
	writeJSON(w, 200, map[string]any{"requestId": a.RequestID, "data": map[string]any{"schemaVersion": "platform-capabilities/v1", "apiReady": true, "graphReady": nil, "sourceDegraded": nil, "investigation": map[string]any{"enabled": h != nil, "status": status, "reason": reason, "readOnly": true}, "kubevirt": map[string]string{"status": "disabled", "verification": "unverified", "reason": "ADR0008-development-deferred"}, "cdi": map[string]string{"status": "disabled", "verification": "unverified", "reason": "ADR0008-development-deferred"}, "pyrca": map[string]string{"status": "disabled", "reason": "ADR0020-not-admitted"}, "commandExecution": map[string]string{"status": "disabled", "reason": "SP07-not-authorized"}}})
}

// A capability observation uses the same bounded, currently authorized Graph
// read as the resource API. No model dependency enters API readiness.
func (h *SP04Handlers) ServeCapabilities(w http.ResponseWriter, r *http.Request) {
	actor, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		investigationError(w, auth.ErrUnauthenticated, "")
		return
	}
	for key, values := range r.URL.Query() {
		if key != "clusterUid" || len(values) != 1 || len(values[0]) > 512 {
			investigationError(w, investigation.ErrInvalid, actor.RequestID)
			return
		}
	}
	base := &boundedResponse{header: http.Header{}}
	h.Investigation.ServeCapabilities(base, r)
	var envelope map[string]any
	if json.Unmarshal(base.b.Bytes(), &envelope) != nil {
		investigationError(w, investigation.ErrInvalid, actor.RequestID)
		return
	}
	data := envelope["data"].(map[string]any)
	ready := persistence.WithTenantTx(r.Context(), h.Pool, actor.TenantID, func(tx pgx.Tx) error { var one int; return tx.QueryRow(r.Context(), "SELECT 1").Scan(&one) }) == nil
	data["apiReady"] = ready
	if cluster := r.URL.Query().Get("clusterUid"); cluster != "" {
		bounded, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		query := r.Clone(bounded)
		query.URL = &url.URL{Path: "/api/v1/resources", RawQuery: url.Values{"clusterUid": {cluster}, "limit": {"1"}}.Encode()}
		output := &boundedResponse{header: http.Header{}}
		h.ServeHTTP(output, query)
		if output.status == 403 {
			investigationError(w, investigation.ErrDenied, actor.RequestID)
			return
		}
		var response struct {
			Meta struct {
				Freshness string   `json:"freshness"`
				Partial   bool     `json:"partial"`
				Degraded  []string `json:"degradedSources"`
			} `json:"meta"`
		}
		graphOK := output.status == 200 && json.Unmarshal(output.b.Bytes(), &response) == nil
		data["graphReady"] = graphOK && response.Meta.Freshness == "fresh" && !response.Meta.Partial
		data["sourceDegraded"] = !graphOK || response.Meta.Partial || len(response.Meta.Degraded) > 0
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")
	writeJSON(w, 200, envelope)
}
