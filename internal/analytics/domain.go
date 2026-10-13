// Package analytics implements server-owned, bounded operational query plans.
// It never executes expressions supplied by a model or browser.
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"ops-platform/internal/graph"
)

var (
	ErrInvalid     = errors.New("INVALID_ARGUMENT")
	ErrUnsupported = errors.New("UNSUPPORTED_QUERY")
	ErrDenied      = errors.New("FORBIDDEN")
	ErrStale       = errors.New("STALE_CONTEXT")
	ErrSource      = errors.New("SOURCE_UNAVAILABLE")
)

type Filter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}
type Sort struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}
type QueryTimeRange struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Timezone string    `json:"timezone"`
}
type QueryIntent struct {
	ComparisonTimeRange *QueryTimeRange `json:"comparisonTimeRange,omitempty"`
	StepSeconds         int             `json:"stepSeconds,omitempty"`
	TimeRange           *QueryTimeRange `json:"timeRange,omitempty"`
	SchemaVersion       string          `json:"schemaVersion"`
	DatasetID           string          `json:"datasetId"`
	Operation           string          `json:"operation"`
	Metrics             []string        `json:"metrics"`
	Dimensions          []string        `json:"dimensions"`
	Filters             []Filter        `json:"filters"`
	Sort                []Sort          `json:"sort"`
	Limit               int             `json:"limit"`
}

// Selection is trusted product selection, separate from model-supplied Intent.
// A namespace selection excludes cluster-scoped resources unless explicitly requested.
type Selection struct {
	Namespaces           []string
	IncludeClusterScoped bool
	CanonicalIDs         []string
}

// Binding is supplied by the trusted runtime after authenticating the subject.
type Binding struct {
	Subject    string
	Grant      graph.Scope
	Selection  Selection
	OwnerEpoch int64
}

// QueryPlan has no exported fields so callers cannot replace its fixed query,
// scopes, catalog version or digest after successful compilation.
type QueryPlan struct {
	query         graph.Query
	subject       string
	grantDigest   string
	catalogDigest string
	digest        string
	dimension     string
	sort          Sort
	limit         int
}

func (p QueryPlan) Digest() string { return p.digest }

// ScopeReader reloads current authority. graph.Authorization implements this interface.
type ScopeReader interface {
	Effective(context.Context, string, string, string) (graph.Scope, error)
}

// ResourceReader uses the existing graph domain, never a paginated HTTP page as a dataset.
type ResourceReader interface {
	Query(context.Context, graph.Query) (graph.Result, error)
}

type Row struct {
	Dimension string  `json:"dimension"`
	Value     float64 `json:"value"`
}
type QueryResult struct {
	ComparisonTimeRange  *QueryTimeRange `json:"comparisonTimeRange,omitempty"`
	StepSeconds          int             `json:"stepSeconds,omitempty"`
	TimeRange            *QueryTimeRange `json:"timeRange,omitempty"`
	SourceManifest       *SourceManifest `json:"sourceManifest,omitempty"`
	SchemaVersion        string          `json:"schemaVersion"`
	DatasetID            string          `json:"datasetId"`
	PlanDigest           string          `json:"planDigest"`
	CatalogDigest        string          `json:"catalogDigest"`
	ScopeDigest          string          `json:"scopeDigest"`
	Status               string          `json:"status"`
	Exact                bool            `json:"exact"`
	Truncated            bool            `json:"truncated"`
	Dimension            string          `json:"dimension"`
	Unit                 string          `json:"unit"`
	Rows                 []Row           `json:"rows"`
	SnapshotReadAt       time.Time       `json:"snapshotReadAt"`
	GraphRevision        graph.Revision  `json:"graphRevision,omitempty"`
	ControlPlaneSnapshot string          `json:"controlPlaneSnapshot,omitempty"`
	Freshness            string          `json:"freshness"`
	Warnings             []string        `json:"warnings"`
	DegradedSources      []string        `json:"degradedSources"`
}

type SourceManifest struct {
	SourceID            string    `json:"sourceId"`
	Revision            int64     `json:"revision"`
	BindingDigest       string    `json:"bindingDigest"`
	QualificationDigest string    `json:"qualificationDigest"`
	QueryHash           string    `json:"queryHash"`
	TemplateVersion     string    `json:"templateVersion"`
	CoverageFrom        time.Time `json:"coverageFrom"`
	CoverageTo          time.Time `json:"coverageTo"`
}

// ExecutionScope returns a copy for committing provenance, not a mutable plan.
func (p QueryPlan) ExecutionScope() graph.Scope { return CloneScope(p.query.Scope) }

// Graph provenance applies to resource snapshots; SQL results carry the actual
// statement snapshot instead of an invented graph revision.
func (r QueryResult) MarshalJSON() ([]byte, error) {
	type wire QueryResult
	b, err := json.Marshal(wire(r))
	if err != nil || r.DatasetID == "resources.current" {
		return b, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	delete(fields, "graphRevision")
	return json.Marshal(fields)
}

func copyQueryTimeRange(window *QueryTimeRange) *QueryTimeRange {
	if window == nil {
		return nil
	}
	copy := *window
	return &copy
}
