package graph

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"log/slog"
	"net/http"
	"ops-platform/gen/internalgraph"
	"ops-platform/internal/auth"
	platformcontract "ops-platform/internal/contract"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
	"slices"
	"sort"
	"strings"
	"time"
)

func ScopeDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type Authorization struct{ Pool persistence.TxBeginner }

func (a Authorization) Effective(ctx context.Context, tenant, subject, cluster string) (Scope, error) {
	out := Scope{Tenant: tenant, Cluster: cluster, Namespaces: []string{}, Resources: []string{}}
	tid, err := uuid.Parse(tenant)
	if err != nil || subject == "" {
		return out, ErrScope
	}
	err = persistence.WithTenantTx(ctx, a.Pool, tid, func(tx pgx.Tx) error {
		var clusterID uuid.UUID
		var clusterRevision, tenantRevision int64
		if err := tx.QueryRow(ctx, `SELECT c.cluster_id,c.revision,t.revision FROM platform.cluster_registrations c JOIN platform.tenants t USING(tenant_id) WHERE c.tenant_id=$1 AND c.cluster_uid=$2 AND c.status='active' AND t.status='active'`, tid, cluster).Scan(&clusterID, &clusterRevision, &tenantRevision); err != nil {
			return ErrScope
		}
		rows, err := tx.Query(ctx, `SELECT binding_id,revision,cluster_scopes,namespace_scopes FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2 AND status='active' AND role_name='operator' ORDER BY binding_id`, tid, subject)
		if err != nil {
			return err
		}
		defer rows.Close()
		versions := []any{clusterRevision, tenantRevision}
		for rows.Next() {
			var id uuid.UUID
			var revision int64
			var clustersRaw, namespacesRaw []byte
			if err := rows.Scan(&id, &revision, &clustersRaw, &namespacesRaw); err != nil {
				return err
			}
			var clusters []uuid.UUID
			var namespaces []auth.NamespaceScope
			if json.Unmarshal(clustersRaw, &clusters) != nil || json.Unmarshal(namespacesRaw, &namespaces) != nil {
				return ErrScope
			}
			versions = append(versions, id.String(), revision, clusters, namespaces)
			if slices.Contains(clusters, clusterID) {
				out.ClusterScoped = true
			}
			for _, ns := range namespaces {
				if ns.ClusterID == clusterID && !slices.Contains(out.Namespaces, ns.Namespace) {
					out.Namespaces = append(out.Namespaces, ns.Namespace)
				}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if !out.ClusterScoped && len(out.Namespaces) == 0 {
			return ErrScope
		}
		sort.Strings(out.Namespaces)
		rows.Close()
		sourceRows, err := tx.Query(ctx, `SELECT source_id,revision,status,source_type,backend_logical_id,data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND (cluster_id=$2 OR data_scope_mapping->'scopes'->'cluster' ? $3) ORDER BY source_id`, tid, clusterID, cluster)
		if err != nil {
			return err
		}
		defer sourceRows.Close()
		for sourceRows.Next() {
			var id, status, sourceType, backend string
			var revision int64
			var mapping []byte
			if err := sourceRows.Scan(&id, &revision, &status, &sourceType, &backend, &mapping); err != nil {
				return err
			}
			versions = append(versions, id, revision, status, sourceType, backend, json.RawMessage(mapping))
		}
		if err := sourceRows.Err(); err != nil {
			return err
		}
		out.AuthorizationRevision = ScopeDigest(versions)
		return nil
	})
	return out, err
}

type Claims struct {
	Audience    string    `json:"audience"`
	Subject     string    `json:"subject"`
	Scope       Scope     `json:"scope"`
	Deadline    time.Time `json:"deadline"`
	QueryDigest string    `json:"queryDigest"`
}

func SignContext(key ed25519.PrivateKey, claims Claims) (string, error) {
	if len(key) != ed25519.PrivateKeySize {
		return "", ErrScope
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw)), nil
}
func VerifyContext(key ed25519.PublicKey, token, audience string, payload []byte) (Claims, error) {
	var claims Claims
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 32<<10 || len(key) != ed25519.PublicKeySize {
		return claims, ErrScope
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, ErrScope
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !ed25519.Verify(key, raw, sig) || json.Unmarshal(raw, &claims) != nil {
		return claims, ErrScope
	}
	now := time.Now()
	if claims.Audience != audience || claims.Subject == "" || !now.Before(claims.Deadline) || claims.Deadline.After(now.Add(30*time.Second)) || claims.QueryDigest != ScopeDigest(json.RawMessage(payload)) {
		return claims, ErrScope
	}
	return claims, nil
}

type InternalHandler struct {
	SourceAuthorities []SourceAuthority
	Graph             *Graph
	Lease             *Lease
	Key               ed25519.PublicKey
	Authorization     Authorization
	Trust             auth.WorkloadTrust
	ValidateSources   func(context.Context) error
	Metrics           *observability.Metrics
	Logger            *slog.Logger
}

// Frozen collection authority is process configuration, not a caller grant.
// It accompanies RCA inputs without changing the frozen Graph response wire shape.
type SourceAuthority struct {
	SourceRegistrationID string `json:"sourceRegistrationId"`
	Revision             int64  `json:"revision"`
	ScopeDigest          string `json:"scopeDigest"`
}

func (h InternalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"code": code})
	}
	if r.Method != "POST" || r.URL.Path != "/internal/v1/graph:query" {
		fail(404, "NOT_FOUND")
		return
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		fail(403, "FORBIDDEN")
		return
	}
	if _, err := auth.VerifyWorkload(auth.WithWorkloadTrust(r.Context(), h.Trust), r.TLS.PeerCertificates[0]); err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	claims, err := VerifyContext(h.Key, r.Header.Get("X-Graph-Context"), "platform-graph-query", raw)
	if err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	// Consume the independently generated internal contract before projection.
	var contract internalgraph.GraphQuery
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&contract) != nil || platformcontract.Validate("https://ops.local/schemas/internal-graph-query/v1", raw) != nil {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	var q Query
	if json.Unmarshal(raw, &q) != nil || ScopeDigest(q.Scope) != ScopeDigest(claims.Scope) {
		fail(403, "FORBIDDEN")
		return
	}
	current, err := h.Authorization.Effective(r.Context(), claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster)
	if err != nil || ScopeDigest(current) != ScopeDigest(claims.Scope) {
		fail(409, "STALE_CONTEXT")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), claims.Deadline)
	defer cancel()
	unavailable := func(stage string) {
		if h.Logger != nil {
			h.Logger.WarnContext(ctx, "Graph request unavailable", "stage", stage)
		}
		fail(503, "GRAPH_NOT_READY")
	}
	if h.Lease == nil || h.Lease.Confirm(ctx, q.ExpectedOwnerEpoch) != nil {
		unavailable("lease_before_query")
		return
	}
	if h.ValidateSources != nil && h.ValidateSources(ctx) != nil {
		fail(503, "SOURCE_SCOPE_UNVERIFIED")
		return
	}
	result, err := h.Graph.Query(ctx, q)
	if err != nil {
		switch {
		case errors.Is(err, ErrScope):
			fail(403, "FORBIDDEN")
		case errors.Is(err, ErrStale):
			fail(409, "STALE_CONTEXT")
		default:
			stage := "graph_query_error"
			if errors.Is(err, ErrNotReady) {
				stage = h.Graph.availabilityFailure(q.ExpectedOwnerEpoch)
			} else if errors.Is(err, context.DeadlineExceeded) {
				stage = "query_deadline"
			} else if err.Error() == "INVALID_ARGUMENT" {
				stage = "query_argument"
			}
			unavailable(stage)
		}
		return
	}
	current, err = h.Authorization.Effective(ctx, claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster)
	if err != nil || ScopeDigest(current) != ScopeDigest(claims.Scope) {
		fail(409, "STALE_CONTEXT")
		return
	}
	if h.Lease.Confirm(ctx, q.ExpectedOwnerEpoch) != nil {
		unavailable("lease_after_query")
		return
	}
	if err := h.Graph.RevalidateResponse(&result); err != nil {
		if errors.Is(err, ErrStale) {
			fail(409, "STALE_CONTEXT")
		} else {
			unavailable("response_fence")
		}
		return
	}
	if h.ValidateSources != nil && h.ValidateSources(ctx) != nil {
		fail(503, "SOURCE_SCOPE_UNVERIFIED")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	encoded, err := json.Marshal(result)
	if err != nil || platformcontract.Validate("https://ops.local/schemas/resource-graph/v2", encoded) != nil {
		unavailable("response_contract")
		return
	}
	h.Metrics.ObserveQuery("graph", result.Partial || len(result.DegradedSources) > 0, result.Freshness)
	_, _ = w.Write(encoded)
}
