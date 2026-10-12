package httpapi

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
	"ops-platform/gen/api"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"time"
)

func sessionClusters(ctx context.Context, pool persistence.TxBeginner, a auth.RequestContext) ([]api.AuthorizedClusterV1, error) {
	out := []api.AuthorizedClusterV1{}
	if pool == nil {
		return out, nil
	}
	ids := append([]uuid.UUID{}, a.ClusterScopes...)
	for _, ns := range a.NamespaceScopes {
		ids = append(ids, ns.ClusterID)
	}
	err := persistence.WithTenantTx(ctx, pool, a.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT cluster_id,cluster_uid,display_name FROM platform.cluster_registrations WHERE tenant_id=$1 AND status='active' AND cluster_id=ANY($2::uuid[]) ORDER BY display_name LIMIT 100`, a.TenantID, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c api.AuthorizedClusterV1
			if err = rows.Scan(&c.ClusterId, &c.ClusterUid, &c.DisplayName); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}
func getAuthSession(w http.ResponseWriter, r *http.Request) { getAuthSessionWithPool(nil, w, r) }
func getAuthSessionWithPool(pool persistence.TxBeginner, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization, X-Tenant-ID")
	ctx, ok := auth.RequestContextFromContext(r.Context())
	if !ok {
		writeTenantError(w, 401, "UNAUTHENTICATED", "verified identity required", false, "")
		return
	}
	clusters, err := sessionClusters(r.Context(), pool, ctx)
	if err != nil {
		writeTenantError(w, 503, "UNAVAILABLE", "authorized cluster registry unavailable", true, ctx.RequestID)
		return
	}
	roles := make([]api.AuthSessionV1Roles, 0, len(ctx.Roles))
	for _, v := range ctx.Roles {
		roles = append(roles, api.AuthSessionV1Roles(v))
	}
	writeJSON(w, 200, map[string]any{"data": map[string]any{"authorizedClusters": clusters, "subject": ctx.Subject, "tenantId": ctx.TenantID, "tenantIds": append([]uuid.UUID{}, ctx.TenantIDs...), "roles": roles, "clusterScopes": append([]uuid.UUID{}, ctx.ClusterScopes...), "namespaceScopes": append([]auth.NamespaceScope{}, ctx.NamespaceScopes...), "tokenExpiresAt": ctx.TokenExpiresAt.Format(time.RFC3339), "acr": ctx.ACR}, "requestId": ctx.RequestID})
}
