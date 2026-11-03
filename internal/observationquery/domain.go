// Package observationquery owns manual queries independently of AI conversations.
package observationquery

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"time"
)

var ErrInvalid = errors.New("INVALID_ARGUMENT")
var ErrDenied = errors.New("FORBIDDEN")
var ErrUnavailable = errors.New("SOURCE_CAPABILITY_UNAVAILABLE")
var ErrConflict = errors.New("CONFLICT")
var ErrExpired = errors.New("RESULT_EXPIRED")
var ErrNotFound = errors.New("NOT_FOUND")

const Schema = "ui-v12-observability-query/v1"

type NamespaceScope struct {
	Kind         string  `json:"kind"`
	Namespace    *string `json:"namespace"`
	NamespaceUID *string `json:"namespaceUid"`
}
type Resource struct {
	Name           string         `json:"-"`
	CanonicalID    string         `json:"canonicalId"`
	UID            string         `json:"uid"`
	ClusterUID     string         `json:"clusterUid"`
	NamespaceScope NamespaceScope `json:"namespaceScope"`
}
type Source struct {
	RegistrationID       uuid.UUID `json:"registrationId"`
	RegistrationRevision int64     `json:"registrationRevision"`
	CredentialRevision   int64     `json:"credentialRevision"`
	QualificationDigest  string    `json:"qualificationDigest"`
	CollectorVersion     string    `json:"collectorVersion"`
}
type Window struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Timezone string    `json:"timezone"`
	Bounds   string    `json:"bounds"`
}
type Budget struct {
	MaxRows            int `json:"maxRows"`
	MaxBytes           int `json:"maxBytes"`
	MaxDurationSeconds int `json:"maxDurationSeconds"`
	MaxSourceCalls     int `json:"maxSourceCalls"`
}
type Request struct {
	SchemaVersion   string             `json:"schemaVersion"`
	QueryKind       string             `json:"queryKind"`
	TemplateID      string             `json:"templateId"`
	TemplateVersion string             `json:"templateVersion"`
	Resource        Resource           `json:"resource"`
	Source          Source             `json:"source"`
	Window          Window             `json:"window"`
	Filters         []analytics.Filter `json:"filters,omitempty"`
	GroupBy         []string           `json:"groupBy,omitempty"`
	Budget          Budget             `json:"budget"`
}

// Decode is the public trust boundary; execution accepts only this fixed template.
func Decode(raw []byte, tenant string) (Request, error) {
	var r Request
	if contract.Validate("https://ops.local/schemas/ui-v12-observability/v1#/$defs/createRequest", raw) != nil || json.Unmarshal(raw, &r) != nil {
		return r, ErrInvalid
	}
	id, err := resource.ParseCanonicalID(r.Resource.CanonicalID)
	if err != nil || id.Tenant != tenant || id.Scope != r.Resource.ClusterUID || id.StableID != r.Resource.UID || id.Kind != "Pod" || id.Domain != "k8s" || r.Resource.NamespaceScope.Kind != "namespaced" {
		return r, ErrInvalid
	}
	if r.QueryKind != "logs" || r.TemplateID != "logs.records" || r.TemplateVersion != "logs.records/v1" || len(r.GroupBy) != 0 {
		return r, ErrInvalid
	}
	if !r.Window.To.After(r.Window.From) || r.Window.To.Sub(r.Window.From) > 24*time.Hour || r.Window.To.After(time.Now().Add(time.Minute)) || r.Window.Timezone == "Local" {
		return r, ErrInvalid
	}
	if _, err = time.LoadLocation(r.Window.Timezone); err != nil {
		return r, ErrInvalid
	}
	// The existing bounded adapter returns at most twenty sanitized examples.
	if r.Budget.MaxRows > 20 || r.Budget.MaxBytes != 128<<10 || r.Budget.MaxDurationSeconds > 8 || r.Budget.MaxSourceCalls != 2 {
		return r, ErrInvalid
	}
	seen := map[string]bool{}
	for _, f := range r.Filters {
		if f.Field != "level" || f.Operator != "eq" || seen[f.Field] {
			return r, ErrInvalid
		}
		if f.Value != "error" && f.Value != "warn" && f.Value != "info" && f.Value != "debug" {
			return r, ErrInvalid
		}
		seen[f.Field] = true
	}
	return r, nil
}
func (r Request) Selection() analytics.Selection {
	return analytics.Selection{Namespaces: []string{*r.Resource.NamespaceScope.Namespace}, CanonicalIDs: []string{r.Resource.CanonicalID}}
}
func (r Request) Intent(operation string) analytics.QueryIntent {
	limit := r.Budget.MaxRows
	if operation == "aggregate" {
		limit = 1
	}
	return analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "logs.records", Operation: operation, Metrics: []string{"log_count"}, Dimensions: []string{}, Filters: append([]analytics.Filter{}, r.Filters...), Sort: []analytics.Sort{}, Limit: limit, TimeRange: &analytics.QueryTimeRange{From: r.Window.From, To: r.Window.To, Timezone: r.Window.Timezone}}
}

type Reauthorization struct {
	CheckedAt                  time.Time `json:"checkedAt"`
	AuthorizationVersion       int       `json:"authorizationVersion"`
	SourceRegistrationRevision int64     `json:"sourceRegistrationRevision"`
	Result                     string    `json:"result"`
	DenialCode                 *string   `json:"denialCode"`
}
type Coverage struct {
	CoveredFrom time.Time `json:"coveredFrom"`
	CoveredTo   time.Time `json:"coveredTo"`
	Complete    bool      `json:"complete"`
	Basis       string    `json:"basis"`
	Gaps        []any     `json:"gaps"`
}
type Termination struct {
	RequestedAt   time.Time `json:"requestedAt"`
	DeliveredAt   time.Time `json:"deliveredAt"`
	SourceStopped bool      `json:"sourceStopped"`
	ReceiptDigest string    `json:"receiptDigest"`
}
type Failure struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	Message   string `json:"message"`
}
type ResultRef struct {
	ResultID  uuid.UUID `json:"resultId"`
	Digest    string    `json:"digest"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type View struct {
	QueryRequest      *Request           `json:"request,omitempty"`
	Filters           []analytics.Filter `json:"filters,omitempty"`
	QueryID           uuid.UUID          `json:"queryId"`
	Revision          int64              `json:"revision"`
	SchemaVersion     string             `json:"schemaVersion"`
	State             string             `json:"state"`
	OwnerSubject      string             `json:"ownerSubject"`
	TenantID          uuid.UUID          `json:"tenantId"`
	Resource          Resource           `json:"resource"`
	Source            Source             `json:"source"`
	Window            Window             `json:"window"`
	Coverage          Coverage           `json:"coverage"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
	ExpiresAt         time.Time          `json:"expiresAt"`
	ResultRef         *ResultRef         `json:"resultRef"`
	CancelRequestedAt *time.Time         `json:"cancelRequestedAt"`
	Termination       *Termination       `json:"termination"`
	Failure           *Failure           `json:"failure"`
	Reauthorization   Reauthorization    `json:"reauthorization"`
}
type Record struct {
	View
	Request    Request
	SessionID  uuid.UUID
	Scope      graph.Scope
	LeaseToken uuid.UUID
	Result     json.RawMessage
}

func (r Request) Coverage() Coverage {
	return Coverage{CoveredFrom: r.Window.From, CoveredTo: r.Window.To, Basis: "unknown", Gaps: []any{}}
}

// History cursors identify an owner-scoped immutable row, not an authorization.
type HistoryPage struct {
	Items      []View     `json:"items"`
	NextCursor *uuid.UUID `json:"nextCursor"`
}
