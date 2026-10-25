package app

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net/http"
	"ops-platform/internal/analytics"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/deepflow"
	"strings"
	"time"
)

type networkAnalysisRuntime struct {
	Adapters map[string]*analytics.Network
	Archive  *evidence.ArchiveService
}

func serveWorkerAnalysis(w http.ResponseWriter, r *http.Request, trust auth.WorkloadTrust, key ed25519.PublicKey, pool *pgxpool.Pool, adapters map[string]*analytics.Victoria, owners map[string]graph.InternalHandler, evidenceAdapters map[string]evidence.Adapter, networkRuntime ...networkAnalysisRuntime) {
	fail := func(status int, code string) {
		// A rejected internal analysis call must stay attributable: the caller
		// only surfaces a generic public code, so record the exact rejection.
		slog.Warn("internal analysis query rejected", "status", status, "code", code)
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
		Intent       json.RawMessage `json:"intent"`
		OwnerEpoch   int64           `json:"ownerEpoch"`
		Capabilities bool            `json:"capabilities,omitempty"`
	}
	if json.Unmarshal(raw, &q) != nil {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	current, err := (graph.Authorization{Pool: pool}).EffectiveClaim(r.Context(), claims)
	if err != nil || !claims.AllowsCurrent(current) {
		fail(403, "FORBIDDEN")
		return
	}
	owner, exists := owners[claims.Scope.Tenant+"|"+claims.Scope.Cluster]
	if !exists || owner.Lease == nil || owner.Lease.Confirm(r.Context(), q.OwnerEpoch) != nil {
		fail(409, "STALE_CONTEXT")
		return
	}
	if q.Capabilities {
		if len(q.Intent) > 0 {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		capabilities := []analytics.SourceCapability{}
		for _, adapter := range adapters {
			entries, err := adapter.Capabilities(r.Context(), claims.Scope)
			if err == nil {
				capabilities = append(capabilities, entries...)
			}
		}
		if len(networkRuntime) > 0 {
			for _, adapter := range networkRuntime[0].Adapters {
				entries, err := adapter.Capabilities(r.Context(), claims.Scope)
				if err != nil {
					slog.Warn("Network capabilities unavailable", "error", err.Error(), "namespaces", claims.Scope.Namespaces)
					continue
				}
				capabilities = append(capabilities, entries...)
			}
		}
		refreshed, err := (graph.Authorization{Pool: pool}).EffectiveClaim(r.Context(), claims)
		if err != nil || !claims.AllowsCurrent(refreshed) || owner.Lease.Confirm(r.Context(), q.OwnerEpoch) != nil {
			fail(409, "STALE_CONTEXT")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		network := []evidence.CapabilitySet{}
		for _, adapter := range evidenceAdapters {
			if scoped, ok := adapter.(interface {
				ScopedCapabilities(context.Context, graph.Scope) (evidence.CapabilitySet, error)
			}); ok {
				if capabilities, err := scoped.ScopedCapabilities(r.Context(), claims.Scope); err == nil {
					network = append(network, capabilities)
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"sourceCapabilities": capabilities, "networkCapabilities": network, "scopeDigest": graph.ScopeDigest(claims.Scope)})
		return
	}
	intent, err := analytics.DecodeIntent(q.Intent)
	if err != nil {
		fail(422, "UNSUPPORTED_QUERY")
		return
	}
	binding := analytics.Binding{Subject: claims.Subject, Grant: current, Selection: analytics.Selection{Namespaces: claims.Scope.Namespaces, CanonicalIDs: claims.Scope.Resources, IncludeClusterScoped: claims.Scope.ClusterScoped}}
	if strings.HasPrefix(intent.DatasetID, "network.") {
		if len(networkRuntime) == 0 || networkRuntime[0].Archive == nil {
			slog.Warn("Network analysis unavailable", "stage", "runtime", "dataset", intent.DatasetID, "operation", intent.Operation)
			fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
			return
		}
		var selected *analytics.Network
		var plan analytics.NetworkPlan
		for _, adapter := range networkRuntime[0].Adapters {
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
			slog.Warn("Network analysis unavailable", "stage", "compile", "operation", intent.Operation, "dataset", intent.DatasetID)
			fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
			return
		}
		result, facts, err := selected.Execute(r.Context(), plan)
		if err != nil {
			slog.Warn("Network analysis unavailable", "stage", "execute", "operation", intent.Operation, "scopeUnverified", errors.Is(err, evidence.ErrScopeUnverified), "deadlineExceeded", errors.Is(err, context.DeadlineExceeded), "capabilityDisabled", errors.Is(err, deepflow.ErrDisabled))
			fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
			return
		}
		refs := []string{}
		for _, fact := range facts {
			var summary deepflow.NetworkSummary
			if json.Unmarshal(fact.Data, &summary) != nil || summary.Namespace == "" {
				fail(503, "SOURCE_CONTRACT_INVALID")
				return
			}
			if err := networkRuntime[0].Archive.Capture(r.Context(), fact, summary.Namespace, fact.EvaluatedAt.Add(181*24*time.Hour)); err != nil {
				slog.Warn("Network analysis unavailable", "stage", "archive", "operation", intent.Operation, "deadlineExceeded", errors.Is(err, context.DeadlineExceeded), "invalidArgument", errors.Is(err, evidence.ErrArgument))
				fail(503, "ARCHIVE_UNAVAILABLE")
				return
			}
			refs = append(refs, fact.EvidenceID)
		}
		current, err = (graph.Authorization{Pool: pool}).EffectiveClaim(r.Context(), claims)
		if err != nil || !claims.AllowsCurrent(current) || owner.Lease.Confirm(r.Context(), q.OwnerEpoch) != nil {
			fail(409, "STALE_CONTEXT")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Result       analytics.QueryResult `json:"result"`
			Scope        graph.Scope           `json:"scope"`
			EvidenceRefs []string              `json:"evidenceRefs"`
		}{result, plan.ExecutionScope(), refs})
		return
	}
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
		slog.Warn("Victoria analysis unavailable", "stage", "compile", "dataset", intent.DatasetID, "operation", intent.Operation)
		fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
		return
	}
	result, err := selected.Execute(r.Context(), plan)
	if err != nil {
		slog.Warn("Victoria analysis unavailable", "stage", "execute", "dataset", intent.DatasetID, "operation", intent.Operation, "deadlineExceeded", errors.Is(err, context.DeadlineExceeded))
		fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
		return
	}
	current, err = (graph.Authorization{Pool: pool}).EffectiveClaim(r.Context(), claims)
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
