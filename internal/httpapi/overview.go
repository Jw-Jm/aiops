package httpapi

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
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
	req.URL = &url.URL{Path: "/api/v1/resources", RawQuery: url.Values{"clusterUid": {cluster}, "limit": {"1"}}.Encode()}
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
	counts := map[string]any{}
	states := map[string]any{}
	bounded, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	err = persistence.WithTenantTx(bounded, h.Pool, a.TenantID, func(tx pgx.Tx) error {
		var findings, incidents int64
		if err := tx.QueryRow(bounded, `SELECT count(*) FROM finding.records WHERE tenant_id=$1 AND cluster_uid=$2 AND (($3 AND namespace='') OR namespace=ANY($4::text[]))`, a.TenantID, cluster, scope.ClusterScoped, scope.Namespaces).Scan(&findings); err != nil {
			return err
		}
		if err := tx.QueryRow(bounded, `SELECT count(*) FROM incident.records t WHERE tenant_id=$1 AND cluster_uid=$2 AND (($3 AND namespace='') OR namespace=ANY($4::text[])) AND NOT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=t.tenant_id AND l.incident_id=t.incident_id AND NOT (($3 AND f.namespace='') OR f.namespace=ANY($4::text[])))`, a.TenantID, cluster, scope.ClusterScoped, scope.Namespaces).Scan(&incidents); err != nil {
			return err
		}
		counts["finding"] = findings
		counts["incident"] = incidents
		return nil
	})
	if err != nil {
		writeTenantError(w, 503, "UNAVAILABLE", "authorized overview query unavailable", true, a.RequestID)
		return
	}
	meta, _ := resources["meta"].(map[string]any)
	summary, _ := meta["summary"].(map[string]any)
	if summary != nil {
		counts["resource"] = summary["total"]
		counts["cluster"] = 1
		kinds, _ := summary["byKind"].(map[string]any)
		counts["node"] = kinds["Node"]
		if counts["node"] == nil {
			counts["node"] = 0
		}
		// Hardware inventory is not established by the Kubernetes-only graph.
		counts["server"] = nil

		states["health"] = summary["byHealth"]
		states["resourceKinds"] = summary["byKind"]
	} else {
		counts["resource"] = nil
	}
	latest, err := (graph.Authorization{Pool: h.Pool}).Effective(r.Context(), a.TenantID.String(), a.Subject, cluster)
	if err != nil || graph.ScopeDigest(latest) != graph.ScopeDigest(scope) {
		writeTenantError(w, 409, "CONFLICT", "authorization changed during overview read", false, a.RequestID)
		return
	}
	writeJSON(w, 200, map[string]any{"requestId": a.RequestID, "data": map[string]any{"clusterUid": cluster, "counts": counts, "distribution": states, "resourceObservation": meta, "resourceError": resourceError, "observedAt": time.Now().UTC(), "scope": scope}})
}
