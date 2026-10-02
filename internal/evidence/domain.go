package evidence

import (
	"context"
	"encoding/json"
	"ops-platform/internal/datascope"
	"ops-platform/internal/graph"
	"time"
)

type Binding struct {
	SourceType             string
	Tenant                 string
	SourceID               string
	Revision               int64
	BackendLogicalID       string
	Endpoint               string
	SourceRetentionSeconds int64
	ScopeMapping           datascope.Mapping
}
type Query struct {
	MaxBytes            int         `json:"maxBytes,omitempty"`
	TimeoutMillis       int         `json:"timeoutMs,omitempty"`
	ResourceCanonicalID string      `json:"resourceCanonicalId"`
	Namespace           string      `json:"namespace"`
	Template            string      `json:"queryTemplate"`
	From                time.Time   `json:"from"`
	To                  time.Time   `json:"to"`
	Limit               int         `json:"limit"`
	Scope               graph.Scope `json:"-"`
}
type Evidence struct {
	SourceScopeDigest      string          `json:"sourceScopeDigest,omitempty"`
	SchemaVersion          string          `json:"schemaVersion"`
	EvidenceID             string          `json:"evidenceId"`
	TenantID               string          `json:"tenantId"`
	ResourceCanonicalID    string          `json:"resourceCanonicalId"`
	Type                   string          `json:"type"`
	DataClass              string          `json:"dataClass"`
	SourceRegistrationID   string          `json:"sourceRegistrationId"`
	SourceRevision         int64           `json:"sourceRevision"`
	SourceSystem           string          `json:"sourceSystem"`
	BackendLogicalID       string          `json:"backendLogicalId"`
	QueryTemplateVersion   string          `json:"queryTemplateVersion"`
	QueryHash              string          `json:"queryHash"`
	EffectiveScope         graph.Scope     `json:"effectiveScope"`
	EvaluatedAt            time.Time       `json:"evaluatedAt"`
	ObservedFrom           time.Time       `json:"observedFrom"`
	ObservedTo             time.Time       `json:"observedTo"`
	SourceRetentionUntil   time.Time       `json:"sourceRetentionUntil"`
	TimeReliable           bool            `json:"timeReliable"`
	ReplayState            string          `json:"replayState"`
	ContentDigest          string          `json:"contentDigest"`
	DerivationEvidenceRefs []string        `json:"derivationEvidenceRefs"`
	IndependenceGroup      string          `json:"independenceGroup"`
	Data                   []byte          `json:"-"`
	ArchiveRef             *ArchiveRef     `json:"archiveRef,omitempty"`
	FactSlice              json.RawMessage `json:"factSlice,omitempty"`
}
type Result struct {
	SchemaVersion   string     `json:"schemaVersion"`
	Evidence        []Evidence `json:"evidence"`
	Partial         bool       `json:"partial"`
	Freshness       string     `json:"freshness"`
	DegradedSources []string   `json:"degradedSources"`
	Warnings        []string   `json:"warnings"`
	QueryHash       string     `json:"queryHash"`
}
type CapabilitySet map[string]string
type Adapter interface {
	Name() string
	Capabilities(context.Context) (CapabilitySet, error)
	Query(context.Context, Query) (Result, error)
}
type Authorizer func(context.Context, Binding) error

func (q Query) ByteBudget() int {
	if q.MaxBytes < 1 {
		return 64 << 10
	}
	return min(q.MaxBytes, 64<<10)
}
func (q Query) TimeBudget() time.Duration {
	if q.TimeoutMillis < 1 {
		return 3 * time.Second
	}
	return time.Duration(min(q.TimeoutMillis, 3000)) * time.Millisecond
}
