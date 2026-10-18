package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"time"
)

func (h *SP04Handlers) getOverview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeTenantError(w, 401, "UNAUTHENTICATED", "verified identity required", false, "")
		return
	}
	cluster := r.URL.Query().Get("clusterUid")
	if cluster == "" {
		clusters, e := sessionClusters(r.Context(), h.Pool, a)
		if e != nil || len(clusters) != 1 {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "select one authorized clusterUid", false, a.RequestID)
			return
		}
		cluster = clusters[0].ClusterUid
	}
	for key, values := range r.URL.Query() {
		if key != "clusterUid" || len(values) != 1 || len(values[0]) > 512 {
			writeTenantError(w, 400, "INVALID_ARGUMENT", "overview only accepts one clusterUid", false, a.RequestID)
			return
		}
	}
	scope, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), a.TenantID.String(), a.Subject, cluster)
	if err != nil {
		writeTenantError(w, 403, "FORBIDDEN", "authorized cluster required", false, a.RequestID)
		return
	}
	req := r.Clone(r.Context())
	req.URL = &url.URL{Path: "/api/v1/resources", RawQuery: url.Values{"clusterUid": {cluster}, "limit": {"1"}, "includeDomains": {"true"}}.Encode()}
	response := &boundedResponse{header: http.Header{}}
	h.ServeHTTP(response, req)
	var resources map[string]any
	var resourceError any
	if response.status == 200 {
		if json.Unmarshal(response.b.Bytes(), &resources) != nil {
			writeTenantError(w, 503, "UNAVAILABLE", "resource summary invalid", true, a.RequestID)
			return
		}
	} else {
		var e map[string]any
		json.Unmarshal(response.b.Bytes(), &e)
		resourceError = e
	}
	states := map[string]any{}
	bounded, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	counts, priorityIncidents, err := h.overviewRecords(bounded, a, scope)
	if err != nil {
		writeTenantError(w, 503, "UNAVAILABLE", "authorized overview query unavailable", true, a.RequestID)
		return
	}
	meta, _ := resources["meta"].(map[string]any)
	summary, _ := meta["summary"].(map[string]any)
	counts["cluster"], counts["node"], counts["server"] = 1, nil, nil
	if summary != nil {
		counts["resource"] = summary["total"]
		kinds, _ := summary["byKind"].(map[string]any)
		counts["node"] = kinds["Node"]
		if counts["node"] == nil {
			counts["node"] = 0
		}
		counts["server"] = kinds["PhysicalServer"]

		states["health"] = summary["byHealth"]
		states["resourceKinds"] = summary["byKind"]
		states["domains"] = summary["byDomain"]
	} else {
		counts["resource"] = nil
	}
	latest, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), a.TenantID.String(), a.Subject, cluster)
	if err != nil || graph.ScopeDigest(latest) != graph.ScopeDigest(scope) {
		writeTenantError(w, 409, "CONFLICT", "authorization changed during overview read", false, a.RequestID)
		return
	}
	writeJSON(w, 200, map[string]any{"requestId": a.RequestID, "data": map[string]any{"clusterUid": cluster, "counts": counts, "priorityIncidents": priorityIncidents, "distribution": states, "resourceObservation": meta, "resourceError": resourceError, "observedAt": time.Now().UTC(), "scope": scope}})
}
