// Package investigation implements durable, fenced read-only investigations.
package investigation

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/graph"
	"slices"
	"time"
)

var (
	ErrNoJob         = errors.New("INVESTIGATION_NOT_READY")
	ErrLease         = errors.New("STALE_CONTEXT")
	ErrConflict      = errors.New("IDEMPOTENCY_CONFLICT")
	ErrReplay        = errors.New("REPLAY_REJECTED")
	ErrCommitted     = errors.New("STEP_ALREADY_COMMITTED")
	ErrBudget        = errors.New("BUDGET_EXHAUSTED")
	ErrInvalid       = errors.New("INVALID_INVESTIGATION")
	ErrDenied        = errors.New("INVESTIGATION_NOT_AUTHORIZED")
	ErrResultExpired = errors.New("ASSISTANT_RESULT_EXPIRED")
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
	return Budget{Usage: Usage{ToolCalls: 40, RawQueries: 10, ResultBytes: 40 << 20, GraphNodes: 8000, EvidenceItems: 4000, ModelRequests: 8, InputTokens: 8000000, OutputTokens: 65536, ModelCostMicros: 0}, DurationSeconds: 600, AllowedDataClasses: []string{"D0", "D1"}}
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
// BudgetTightening is a caller request to narrow an admitted budget. A zero
// dimension means "not requested for that dimension"; a negative value is
// invalid input and never a silent default.
type BudgetTightening struct {
	DurationSeconds int64 `json:"durationSeconds"`
	ToolCalls       int64 `json:"toolCalls"`
	RawQueries      int64 `json:"rawQueries"`
	ResultBytes     int64 `json:"resultBytes"`
	GraphNodes      int64 `json:"graphNodes"`
	EvidenceItems   int64 `json:"evidenceItems"`
	ModelRequests   int64 `json:"modelRequests"`
	InputTokens     int64 `json:"inputTokens"`
	OutputTokens    int64 `json:"outputTokens"`
}

// UnmarshalJSON decodes a requested narrowing. Every dimension is a
// non-nullable integer: an explicit null, a fraction, a string, a boolean or
// an unknown dimension is refused instead of being read as an omitted
// dimension. The dimension names are checked here because a custom decoder
// replaces the boundary of the request decoder, which would otherwise be the
// only place that rejects an unknown field.
func (t *BudgetTightening) UnmarshalJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 || string(bytes.TrimSpace(data)) == "null" {
		return ErrInvalid
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return ErrInvalid
	}
	if raw == nil {
		return ErrInvalid
	}
	out := BudgetTightening{}
	for name, value := range raw {
		target := map[string]*int64{
			"durationSeconds": &out.DurationSeconds,
			"toolCalls":       &out.ToolCalls,
			"rawQueries":      &out.RawQueries,
			"resultBytes":     &out.ResultBytes,
			"graphNodes":      &out.GraphNodes,
			"evidenceItems":   &out.EvidenceItems,
			"modelRequests":   &out.ModelRequests,
			"inputTokens":     &out.InputTokens,
			"outputTokens":    &out.OutputTokens,
		}[name]
		if target == nil {
			return ErrInvalid
		}
		// An explicit null is not an omitted dimension: it would be read as
		// "not requested" and silently widen the request to the admission.
		if len(value) == 0 || string(value) == "null" {
			return ErrInvalid
		}
		var number int64
		if err := json.Unmarshal(value, &number); err != nil {
			return ErrInvalid
		}
		*target = number
	}
	*t = out
	return nil
}

// vector returns the tightenable usage dimensions. Model cost is not
// tightenable: it is owned by the admission and the default profile grants
// none, so a client request for it would always be invalid.
func (t BudgetTightening) vector() []int64 {
	return []int64{t.ToolCalls, t.RawQueries, t.ResultBytes, t.GraphNodes, t.EvidenceItems, t.ModelRequests, t.InputTokens, t.OutputTokens}
}

// TightenBudget narrows the admitted budget and never widens it. It reuses
// the workbench semantics of the manual investigation entry point: a zero
// dimension is "not requested", a negative or otherwise invalid value is
// rejected instead of silently defaulted, and LowerBudget keeps the
// per-dimension minimum, so a request above the admitted dimension is
// clamped back to it.
func TightenBudget(base Budget, request BudgetTightening) (Budget, error) {
	if !base.Valid() {
		return Budget{}, ErrInvalid
	}
	values := request.vector()
	for _, value := range values {
		if value < 0 {
			return Budget{}, ErrInvalid
		}
	}
	requested := base
	for i, value := range values {
		if value == 0 {
			continue
		}
		limits := requested.Usage.vector()
		limits[i] = value
		requested.Usage = usage(limits)
	}
	if request.DurationSeconds < 0 {
		return Budget{}, ErrInvalid
	}
	if request.DurationSeconds != 0 {
		requested.DurationSeconds = request.DurationSeconds
	}
	if !requested.Valid() {
		return Budget{}, ErrInvalid
	}
	return LowerBudget(base, requested)
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
	Focus             string
}
type Job struct {
	Assistant            *AssistantTask `json:"assistant,omitempty"`
	SchemaVersion        string         `json:"schemaVersion"`
	authority            jobAuthority
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
	Focus                string          `json:"focus"`
	AdmissionDigest      string          `json:"admissionDigest"`
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
	Arguments  json.RawMessage
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
	ArgsSummary json.RawMessage `json:"argsSummary,omitempty"`
	StepID      uuid.UUID       `json:"stepId"`
	Name        string          `json:"toolName"`
	ArgsDigest  string          `json:"argsDigest"`
	State       string          `json:"state"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorCode   string          `json:"errorCode,omitempty"`
}
