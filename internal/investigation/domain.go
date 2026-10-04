// Package investigation implements durable, fenced read-only investigations.
package investigation

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/graph"
	"slices"
	"time"
)

var (
	ErrNoJob     = errors.New("INVESTIGATION_NOT_READY")
	ErrLease     = errors.New("STALE_CONTEXT")
	ErrConflict  = errors.New("IDEMPOTENCY_CONFLICT")
	ErrReplay    = errors.New("REPLAY_REJECTED")
	ErrCommitted = errors.New("STEP_ALREADY_COMMITTED")
	ErrBudget    = errors.New("BUDGET_EXHAUSTED")
	ErrInvalid   = errors.New("INVALID_INVESTIGATION")
	ErrDenied    = errors.New("INVESTIGATION_NOT_AUTHORIZED")
)

type Usage struct {
	ToolCalls       int64 `json:"toolCalls"`
	RawQueries      int64 `json:"rawQueries"`
	ResultBytes     int64 `json:"resultBytes"`
	GraphNodes      int64 `json:"graphNodes"`
	EvidenceItems   int64 `json:"evidenceItems"`
	ModelRequests   int64 `json:"modelRequests"`
	InputTokens     int64 `json:"inputTokens"`
	OutputTokens    int64 `json:"outputTokens"`
	ModelCostMicros int64 `json:"modelCostMicros"`
}

func (u Usage) vector() []int64 {
	return []int64{u.ToolCalls, u.RawQueries, u.ResultBytes, u.GraphNodes, u.EvidenceItems, u.ModelRequests, u.InputTokens, u.OutputTokens, u.ModelCostMicros}
}
func usage(v []int64) Usage { return Usage{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7], v[8]} }
func (u Usage) Add(v Usage) Usage {
	a, b := u.vector(), v.vector()
	for i := range a {
		a[i] += b[i]
	}
	return usage(a)
}
func (u Usage) Sub(v Usage) Usage {
	a, b := u.vector(), v.vector()
	for i := range a {
		a[i] -= b[i]
	}
	return usage(a)
}
func (u Usage) Within(limit Usage) bool {
	a, b := u.vector(), limit.vector()
	for i := range a {
		if a[i] < 0 || a[i] > b[i] {
			return false
		}
	}
	return true
}

type Budget struct {
	Usage
	DurationSeconds    int64    `json:"durationSeconds"`
	AllowedDataClasses []string `json:"allowedDataClasses"`
}

func DefaultBudget() Budget {
	return Budget{Usage: Usage{ToolCalls: 40, RawQueries: 10, ResultBytes: 40 << 20, GraphNodes: 8000, EvidenceItems: 4000, ModelRequests: 8, InputTokens: 32768, OutputTokens: 8192, ModelCostMicros: 0}, DurationSeconds: 600, AllowedDataClasses: []string{"D0", "D1"}}
}
func (b Budget) Valid() bool {
	if len(b.AllowedDataClasses) < 1 || len(b.AllowedDataClasses) > 2 {
		return false
	}
	for _, c := range b.AllowedDataClasses {
		if c != "D0" && c != "D1" {
			return false
		}
	}
	if len(b.AllowedDataClasses) == 2 && b.AllowedDataClasses[0] == b.AllowedDataClasses[1] {
		return false
	}
	return b.DurationSeconds > 0 && b.DurationSeconds <= 600 && b.ToolCalls > 0 && b.Usage.Within(DefaultBudget().Usage)
}
func LowerBudget(profile, policy Budget) (Budget, error) {
	if !profile.Valid() || !policy.Valid() {
		return Budget{}, ErrBudget
	}
	a, b := profile.vector(), policy.vector()
	for i := range a {
		if b[i] < a[i] {
			a[i] = b[i]
		}
	}
	d := profile.DurationSeconds
	if policy.DurationSeconds < d {
		d = policy.DurationSeconds
	}
	classes := []string{}
	for _, c := range profile.AllowedDataClasses {
		if slices.Contains(policy.AllowedDataClasses, c) {
			classes = append(classes, c)
		}
	}
	if len(classes) == 0 {
		return Budget{}, ErrBudget
	}
	return Budget{Usage: usage(a), DurationSeconds: d, AllowedDataClasses: classes}, nil
}

type CreateInvestigationRequest struct {
	IdempotencyKey    string
	RequestDigest     string
	TenantID          uuid.UUID
	IncidentID        uuid.UUID
	Subject           string
	TriggerRevision   int64
	TriggerKind       string
	PolicyVersion     string
	Scope             graph.Scope
	ToolCatalogDigest string
	Budget            Budget
}
type Job struct {
	SchemaVersion        string          `json:"schemaVersion"`
	TenantID             uuid.UUID       `json:"tenantId"`
	JobID                uuid.UUID       `json:"jobId"`
	IncidentID           uuid.UUID       `json:"incidentId"`
	Subject              string          `json:"subject"`
	TriggerRevision      int64           `json:"triggerRevision"`
	TriggerKind          string          `json:"triggerKind"`
	PolicyVersion        string          `json:"policyVersion"`
	Scope                graph.Scope     `json:"scope"`
	EffectiveScopeDigest string          `json:"effectiveScopeDigest"`
	ToolCatalogDigest    string          `json:"toolCatalogDigest"`
	State                string          `json:"state"`
	Budget               Budget          `json:"budget"`
	Reserved             Usage           `json:"budgetReserved"`
	Consumed             Usage           `json:"budgetConsumed"`
	ExpiresAt            time.Time       `json:"expiresAt"`
	EventSeq             int64           `json:"eventSeq"`
	NextCallSeq          int64           `json:"nextCallSeq"`
	Result               json.RawMessage `json:"result,omitempty"`
	ErrorCode            string          `json:"errorCode,omitempty"`
}
type Lease struct {
	TenantID   uuid.UUID `json:"tenantId"`
	JobID      uuid.UUID `json:"jobId"`
	Token      uuid.UUID `json:"-"`
	Generation int64     `json:"leaseGeneration"`
}
type Call struct {
	Claims     *InvocationClaims
	StepID     uuid.UUID
	ContextID  uuid.UUID
	Seq        *int64
	JTI        string
	Name       string
	ArgsDigest string
	Reserve    Usage
	Model      bool
}
type Step struct {
	StepID     uuid.UUID       `json:"stepId"`
	Name       string          `json:"toolName"`
	ArgsDigest string          `json:"argsDigest"`
	State      string          `json:"state"`
	Result     json.RawMessage `json:"result,omitempty"`
	ErrorCode  string          `json:"errorCode,omitempty"`
}
