package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
)

// A saved-result drill is a fresh list constrained by the authorized original
// scope and filters. URL labels and digests never grant or reconstruct scope.
func assistantDrill(ctx context.Context, pool persistence.TxBeginner, actor auth.RequestContext, r *http.Request, dataset string, grant graph.Scope) (graph.Scope, *http.Request, error) {
	values := r.URL.Query()
	refs := values["assistantQueryResultId"]
	if len(refs) == 0 {
		if values.Has("assistantGroup") {
			return grant, r, graph.ErrScope
		}
		return grant, r, nil
	}
	if len(refs) != 1 || len(values["assistantGroup"]) > 1 {
		return grant, r, graph.ErrScope
	}
	id, err := uuid.Parse(refs[0])
	if err != nil {
		return grant, r, graph.ErrScope
	}
	stored, original, err := (assistant.Repository{Pool: pool}).QueryResultWithScope(ctx, actor.TenantID, id, actor.Subject)
	if err != nil || stored.Result.DatasetID != dataset || !original.FiltersKnown || original.ClusterUID != grant.Cluster {
		return grant, r, graph.ErrScope
	}
	scope := grant
	scope.Namespaces, scope.Resources, scope.ClusterScoped = original.Namespaces, original.CanonicalIDs, original.ClusterScoped
	if !graph.ContainsScope(grant, scope) {
		return grant, r, graph.ErrScope
	}
	allowed := map[string]bool{"namespace": true, "state": true}
	if dataset == "resources.current" {
		allowed = map[string]bool{"kind": true, "namespace": true, "name": true, "health": true}
	}
	if dataset == "findings.occurrences" {
		allowed["severity"] = true
	}
	filters := map[string]string{}
	for _, filter := range original.Filters {
		if !allowed[filter.Field] || filter.Operator != "eq" {
			return grant, r, graph.ErrScope
		}
		if old, exists := filters[filter.Field]; exists && old != filter.Value {
			return grant, r, graph.ErrScope
		}
		filters[filter.Field] = filter.Value
	}
	window := stored.Result.TimeRange
	if values.Has("assistantGroup") {
		dimension, group := stored.Result.Dimension, values.Get("assistantGroup")
		found := false
		for _, row := range stored.Result.Rows {
			if row.Dimension == group {
				found = true
				break
			}
		}
		if !found {
			return grant, r, graph.ErrScope
		}
		if dimension == "day" && window != nil {
			zone, zoneErr := time.LoadLocation(window.Timezone)
			if zoneErr != nil {
				return grant, r, graph.ErrScope
			}
			from, parseErr := time.ParseInLocation("2006-01-02", group, zone)
			if parseErr != nil {
				return grant, r, graph.ErrScope
			}
			to := from.AddDate(0, 0, 1)
			if from.Before(window.From) {
				from = window.From
			}
			if to.After(window.To) {
				to = window.To
			}
			copy := *window
			copy.From, copy.To = from, to
			window = &copy
		} else {
			if !allowed[dimension] {
				return grant, r, graph.ErrScope
			}
			if old, exists := filters[dimension]; exists && old != group {
				return grant, r, graph.ErrScope
			}
			filters[dimension] = group
		}
	}
	for key, value := range filters {
		if values.Has(key) && values.Get(key) != value {
			return grant, r, graph.ErrScope
		}
		values.Set(key, value)
	}
	if window != nil {
		from, to := "observedFrom", "observedTo"
		if dataset == "incidents.records" {
			from, to = "openedFrom", "openedTo"
		}
		for key, clock := range map[string]time.Time{from: window.From, to: window.To} {
			if values.Has(key) && values.Get(key) != clock.Format(time.RFC3339Nano) {
				return grant, r, graph.ErrScope
			}
			values.Set(key, clock.Format(time.RFC3339Nano))
		}
	}
	copy := r.Clone(ctx)
	address := *r.URL
	copy.URL = &address
	copy.URL.RawQuery = values.Encode()
	return scope, copy, nil
}

func resourceListDigest(q graph.Query, query url.Values) string {
	if !query.Has("assistantQueryResultId") {
		return graph.QueryDigest(q)
	}
	return graph.ScopeDigest([]any{graph.QueryDigest(q), query.Get("assistantQueryResultId"), query["assistantGroup"]})
}
