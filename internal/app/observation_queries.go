package app

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"ops-platform/internal/analytics"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/observationquery"
	"ops-platform/internal/resource"
	"slices"
	"sync"
	"time"
)

func observationService(pool *pgxpool.Pool, adapters map[string]*analytics.Victoria, owner graph.InternalHandler, cluster SP04Cluster) observationquery.Service {
	return observationquery.Service{Repository: observationquery.Repository{Pool: pool}, Adapters: adapters, CheckResource: func(ctx context.Context, target observationquery.Resource) error {
		meta, err := readObservationIdentity(ctx, owner.Lease.Client, "/api/v1/namespaces/"+url.PathEscape(*target.NamespaceScope.Namespace)+"/pods/"+url.PathEscape(target.Name))
		if err != nil {
			return err
		}
		if meta.UID != target.UID || meta.Name != target.Name || meta.Namespace != *target.NamespaceScope.Namespace {
			return observationquery.ErrDenied
		}
		return nil
	}, Resources: func(ctx context.Context, scope graph.Scope) ([]observationquery.Resource, error) {
		if err := owner.ValidateSources(ctx); err != nil {
			return nil, err
		}
		out := []observationquery.Resource{}
		namespaceUIDs := map[string]string{}
		var pods []observationIdentity
		client := owner.Lease.Client
		if cluster.ClusterWatch && len(scope.Namespaces) > 1 {
			namespaces, err := listObservationIdentities(ctx, client, "/api/v1/namespaces")
			if err != nil {
				return nil, err
			}
			for _, ns := range namespaces {
				if slices.Contains(cluster.Namespaces, ns.Name) && slices.Contains(scope.Namespaces, ns.Name) {
					namespaceUIDs[ns.Name] = ns.UID
				}
			}
			pods, err = listObservationIdentities(ctx, client, "/api/v1/pods")
			if err != nil {
				return nil, err
			}
		} else {
			for _, ns := range scope.Namespaces {
				if !slices.Contains(cluster.Namespaces, ns) {
					continue
				}
				path := "/api/v1/namespaces/" + url.PathEscape(ns)
				namespace, err := readObservationIdentity(ctx, client, path)
				if err != nil {
					return nil, err
				}
				if namespace.Name != ns {
					return nil, observationquery.ErrUnavailable
				}
				namespaceUIDs[ns] = namespace.UID
				items, err := listObservationIdentities(ctx, client, path+"/pods")
				if err != nil {
					return nil, err
				}
				for _, pod := range items {
					if pod.Namespace != ns {
						return nil, observationquery.ErrUnavailable
					}
				}
				pods = append(pods, items...)
			}
		}
		for _, pod := range pods {
			nsuid, inScope := namespaceUIDs[pod.Namespace]
			if !inScope {
				continue
			}
			canonical := resource.CanonicalID{Domain: "k8s", Tenant: scope.Tenant, Scope: scope.Cluster, APIGroup: "core", Kind: "Pod", StableID: pod.UID}.String()
			if !scope.Allows(canonical, pod.Namespace) {
				continue
			}
			out = append(out, observationquery.Resource{Name: pod.Name, CanonicalID: canonical, UID: pod.UID, ClusterUID: scope.Cluster, NamespaceScope: observationquery.NamespaceScope{Kind: "namespaced", Namespace: &pod.Namespace, NamespaceUID: &nsuid}})
			if len(out) > 200 {
				return nil, observationquery.ErrUnavailable
			}
		}
		if err := owner.ValidateSources(ctx); err != nil {
			return nil, err
		}
		return out, nil

	}}
}
func startObservationQueries(ctx context.Context, pool *pgxpool.Pool, c *SP04Config, adapters map[string]*analytics.Victoria, owners map[string]graph.InternalHandler, group *sync.WaitGroup) {
	for _, cluster := range c.Clusters {
		owner := owners[cluster.Tenant+"|"+cluster.ClusterUID]
		service := observationService(pool, adapters, owner, cluster)
		configureNativeLogs(&service, owner, cluster)
		group.Add(1)
		go func() {
			defer group.Done()
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					// One bounded read at a time per configured cluster; the persisted lease
					// fences other Workers and survives process restarts.
					if err := service.RunOne(ctx, uuid.MustParse(cluster.Tenant), cluster.ClusterUID); err != nil && !errors.Is(err, observationquery.ErrNotFound) && ctx.Err() == nil {
						slog.Warn("manual query pass unavailable", "cluster", cluster.ClusterUID, "error", err)
					}
				}
			}
		}()
	}
}
func serveObservationRequest(w http.ResponseWriter, r *http.Request, trust auth.WorkloadTrust, key ed25519.PublicKey, pool *pgxpool.Pool, adapters map[string]*analytics.Victoria, owners map[string]graph.InternalHandler, logClusters map[string]SP04Cluster) {
	fail := func(err error) {
		code := err.Error()
		status := 503
		switch {
		case errors.Is(err, observationquery.ErrInvalid):
			status = 400
		case errors.Is(err, observationquery.ErrDenied):
			status = 403
		case errors.Is(err, observationquery.ErrConflict):
			status = 409
		case errors.Is(err, observationquery.ErrExpired):
			status = 410
		case errors.Is(err, observationquery.ErrNotFound):
			status = 404
		default:
			code = "SOURCE_CAPABILITY_UNAVAILABLE"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
	}
	if r.Method != "POST" || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		fail(observationquery.ErrDenied)
		return
	}
	if _, err := auth.VerifyWorkload(auth.WithWorkloadTrust(r.Context(), trust), r.TLS.PeerCertificates[0]); err != nil {
		fail(observationquery.ErrDenied)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		fail(observationquery.ErrInvalid)
		return
	}
	claims, err := graph.VerifyContext(key, r.Header.Get("X-Graph-Context"), "platform-observability-query", raw)
	if err != nil || claims.Source != graph.SourceNativeInteractive {
		fail(observationquery.ErrDenied)
		return
	}
	current, err := (graph.Authorization{Pool: pool}).EffectiveClaim(r.Context(), claims)
	if err != nil {
		fail(observationquery.ErrDenied)
		return
	}
	var body struct {
		RequestKey  uuid.UUID       `json:"requestKey"`
		Action      string          `json:"action"`
		Cursor      uuid.UUID       `json:"cursor"`
		Limit       int             `json:"limit"`
		QueryID     uuid.UUID       `json:"queryId"`
		OwnerEpoch  int64           `json:"ownerEpoch"`
		Request     json.RawMessage `json:"request"`
		Cancel      json.RawMessage `json:"cancel"`
		CanonicalID string          `json:"canonicalId"`
	}
	if json.Unmarshal(raw, &body) != nil {
		fail(observationquery.ErrInvalid)
		return
	}
	owner, ok := owners[claims.Scope.Tenant+"|"+claims.Scope.Cluster]
	if !ok || owner.Lease == nil || owner.Lease.Confirm(r.Context(), body.OwnerEpoch) != nil {
		fail(observationquery.ErrUnavailable)
		return
	}
	cluster := logClusters[claims.Scope.Tenant+"|"+claims.Scope.Cluster]
	service := observationService(pool, adapters, owner, cluster)
	configureNativeLogs(&service, owner, cluster)
	var data any
	switch body.Action {
	case "capabilities":
		resources, e := service.Resources(r.Context(), claims.Scope)
		if e != nil {
			fail(e)
			return
		}
		if body.CanonicalID != "" {
			found := false
			for _, res := range resources {
				if res.CanonicalID == body.CanonicalID {
					claims.Scope, err = analytics.RestrictScope(current, analytics.Selection{Namespaces: []string{*res.NamespaceScope.Namespace}, CanonicalIDs: []string{res.CanonicalID}})
					found = true
					break
				}
			}
			if !found || err != nil {
				fail(observationquery.ErrDenied)
				return
			}
			claims.GrantSnapshotDigest = graph.ScopeDigest(current)
		}
		sources, e := service.Capabilities(r.Context(), claims)
		if e != nil {
			fail(e)
			return
		}
		display := make([]map[string]any, 0, len(resources))
		for _, res := range resources {
			display = append(display, map[string]any{"canonicalId": res.CanonicalID, "uid": res.UID, "clusterUid": res.ClusterUID, "namespaceScope": res.NamespaceScope, "displayName": res.Name})
		}
		data = map[string]any{"schemaVersion": "ui-v12-observability-capabilities/v1", "resources": display, "sources": sources, "templates": []string{"logs.records/v1"}, "budget": map[string]int{"maxRows": 20, "maxBytes": 131072, "maxDurationSeconds": 8, "maxSourceCalls": 2}}
	case "create":
		if body.RequestKey == uuid.Nil {
			fail(observationquery.ErrInvalid)
			return
		}
		request, e := observationquery.Decode(body.Request, claims.Scope.Tenant)
		if e != nil {
			fail(e)
			return
		}
		data, err = service.Create(r.Context(), claims, request, body.RequestKey)
	case "list":
		if body.Limit == 0 {
			data, err = service.List(r.Context(), claims)
		} else {
			data, err = service.ListPage(r.Context(), claims, body.Cursor, body.Limit)
		}
	case "get", "results", "cancel":
		if body.QueryID == uuid.Nil {
			fail(observationquery.ErrInvalid)
			return
		}
		rec, e := service.Read(r.Context(), claims, body.QueryID)
		if e != nil {
			fail(e)
			return
		}
		if body.Action == "get" {
			data = rec.View
		} else if rec.State == "expired" {
			fail(observationquery.ErrExpired)
			return
		} else if body.Action == "results" {
			if rec.State != "completed" {
				fail(observationquery.ErrConflict)
				return
			}
			var result map[string]any
			if json.Unmarshal(rec.Result, &result) != nil {
				fail(observationquery.ErrUnavailable)
				return
			}
			result["reauthorization"] = rec.Reauthorization
			data = result
		} else {
			if contract.Validate("https://ops.local/schemas/ui-v12-observability/v1#/$defs/cancelRequest", body.Cancel) != nil {
				fail(observationquery.ErrInvalid)
				return
			}
			var cancel struct {
				ExpectedRevision int64 `json:"expectedRevision"`
			}
			_ = json.Unmarshal(body.Cancel, &cancel)
			view, e := service.Repository.Cancel(r.Context(), claims, body.QueryID, cancel.ExpectedRevision)
			if e != nil {
				fail(e)
				return
			}
			data = map[string]any{"queryId": view.QueryID, "state": view.State, "cancelRequestedAt": view.CancelRequestedAt, "termination": view.Termination}
		}
	default:
		fail(observationquery.ErrInvalid)
		return
	}
	if err != nil {
		fail(err)
		return
	}
	if owner.Lease.Confirm(r.Context(), body.OwnerEpoch) != nil {
		fail(observationquery.ErrUnavailable)
		return
	}
	if _, err := (graph.Authorization{Pool: pool}).EffectiveClaim(r.Context(), claims); err != nil {
		fail(observationquery.ErrDenied)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

type observationIdentity struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

func listObservationIdentities(ctx context.Context, client *kubernetes.Client, path string) ([]observationIdentity, error) {
	response, err := client.Do(ctx, "GET", path+"?limit=201", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var list struct {
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
		Items []struct {
			Metadata observationIdentity `json:"metadata"`
		} `json:"items"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&list) != nil || list.Metadata.Continue != "" || len(list.Items) > 201 {
		return nil, observationquery.ErrUnavailable
	}
	items := make([]observationIdentity, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Metadata.UID == "" || item.Metadata.Name == "" {
			return nil, observationquery.ErrUnavailable
		}
		items = append(items, item.Metadata)
	}
	return items, nil
}

func readObservationIdentity(ctx context.Context, client *kubernetes.Client, path string) (observationIdentity, error) {
	var object struct {
		Metadata observationIdentity `json:"metadata"`
	}
	response, err := client.Do(ctx, "GET", path, nil)
	if err != nil {
		return object.Metadata, err
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return object.Metadata, observationquery.ErrDenied
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&object) != nil || object.Metadata.UID == "" {
		return observationIdentity{}, observationquery.ErrUnavailable
	}
	return object.Metadata, nil
}

func configureNativeLogs(service *observationquery.Service, owner graph.InternalHandler, cluster SP04Cluster) {
	if cluster.PodLogs && owner.Lease != nil && uuid.Validate(cluster.SourceID) == nil {
		service.NativeLogs = &observationquery.KubernetesLogs{Client: owner.Lease.Client, SourceID: uuid.MustParse(cluster.SourceID), Revision: cluster.SourceRevision}
	}
}
