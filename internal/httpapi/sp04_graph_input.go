package httpapi

import (
	"errors"
	"net/http"
	api "ops-platform/gen/api"
	"ops-platform/internal/graph"
	"strconv"
	"time"
)

func diagnosticInput(r *http.Request) (graph.Query, time.Duration, error) {
	var body api.DiagnosticGraphBuildRequest
	var q graph.Query
	if err := decodeSP04(r, &body); err != nil {
		return q, 0, err
	}
	p := body.Policy
	if body.Recipe != api.Incident || p.MaxDepth < 0 || p.MaxDepth > 2 || p.MaxNodes < 1 || p.MaxNodes > 200 || p.MaxEdges < 1 || p.MaxEdges > 400 || p.TimeoutMs < 1 || p.TimeoutMs > 30000 {
		return q, 0, errors.New("invalid graph policy")
	}
	q = graph.Query{QueryKind: "diagnostic", CanonicalID: body.EntryCanonicalId, MaxDepth: p.MaxDepth, MaxNodes: p.MaxNodes, MaxEdges: p.MaxEdges, RelationKinds: []string{}}
	if q.MaxDepth == 0 {
		q.QueryKind = "entity"
		q.MaxDepth = 1
	}
	return q, time.Duration(p.TimeoutMs) * time.Millisecond, nil
}

func neighborInput(r *http.Request) (graph.Query, error) {
	q := graph.Query{QueryKind: "neighbors", Direction: "both", MaxDepth: 1, MaxNodes: 200, MaxEdges: 400, RelationKinds: []string{}}
	values := r.URL.Query()
	if direction := values.Get("direction"); direction != "" {
		q.Direction = direction
	}
	if q.Direction != "in" && q.Direction != "out" && q.Direction != "both" {
		return q, errors.New("invalid direction")
	}
	for _, bound := range []struct {
		name     string
		target   *int
		min, max int
	}{{"depth", &q.MaxDepth, 0, 2}, {"maxNodes", &q.MaxNodes, 1, 200}, {"maxEdges", &q.MaxEdges, 1, 400}} {
		if raw := values.Get(bound.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < bound.min || n > bound.max {
				return q, errors.New("invalid graph budget")
			}
			*bound.target = n
		}
	}
	if kind := values.Get("relationKind"); kind != "" {
		if len(kind) > 128 {
			return q, errors.New("invalid relation kind")
		}
		q.RelationKinds = []string{kind}
	}
	if q.MaxDepth == 0 {
		q.QueryKind = "entity"
		q.MaxDepth = 1
	}
	return q, nil
}
