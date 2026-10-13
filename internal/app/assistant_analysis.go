package app

import (
	"crypto/ed25519"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"ops-platform/internal/analytics"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
)

func serveWorkerAnalysis(w http.ResponseWriter, r *http.Request, trust auth.WorkloadTrust, key ed25519.PublicKey, pool *pgxpool.Pool, adapters map[string]*analytics.Victoria, owners map[string]graph.InternalHandler) {
	fail := func(status int, code string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"code": code})
	}
	if r.Method != "POST" || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		fail(403, "FORBIDDEN")
		return
	}
	if _, err := auth.VerifyWorkload(auth.WithWorkloadTrust(r.Context(), trust), r.TLS.PeerCertificates[0]); err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	claims, err := graph.VerifyContext(key, r.Header.Get("X-Graph-Context"), "platform-analysis-query", raw)
	if err != nil || claims.GrantSnapshotDigest == "" {
		fail(403, "FORBIDDEN")
		return
	}
	var q struct {
		Intent     json.RawMessage `json:"intent"`
		OwnerEpoch int64           `json:"ownerEpoch"`
	}
	if json.Unmarshal(raw, &q) != nil {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	intent, err := analytics.DecodeIntent(q.Intent)
	if err != nil {
		fail(422, "UNSUPPORTED_QUERY")
		return
	}
	current, err := (graph.Authorization{Pool: pool}).Effective(r.Context(), claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster)
	if err != nil || !claims.AllowsCurrent(current) {
		fail(403, "FORBIDDEN")
		return
	}
	owner, exists := owners[claims.Scope.Tenant+"|"+claims.Scope.Cluster]
	if !exists || owner.Lease == nil || owner.Lease.Confirm(r.Context(), q.OwnerEpoch) != nil {
		fail(409, "STALE_CONTEXT")
		return
	}
	binding := analytics.Binding{Subject: claims.Subject, Grant: current, Selection: analytics.Selection{Namespaces: claims.Scope.Namespaces, CanonicalIDs: claims.Scope.Resources, IncludeClusterScoped: claims.Scope.ClusterScoped}}
	var selected *analytics.Victoria
	var plan analytics.VictoriaPlan
	for _, adapter := range adapters {
		candidate, err := adapter.Compile(intent, binding)
		if err != nil {
			continue
		}
		if selected != nil {
			fail(422, "AMBIGUOUS_SOURCE")
			return
		}
		selected = adapter
		plan = candidate
	}
	if selected == nil {
		fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
		return
	}
	result, err := selected.Execute(r.Context(), plan)
	if err != nil {
		fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
		return
	}
	current, err = (graph.Authorization{Pool: pool}).Effective(r.Context(), claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster)
	if err != nil || !claims.AllowsCurrent(current) || owner.Lease.Confirm(r.Context(), q.OwnerEpoch) != nil {
		fail(409, "STALE_CONTEXT")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Result analytics.QueryResult `json:"result"`
		Scope  graph.Scope           `json:"scope"`
	}{result, plan.ExecutionScope()})
}
