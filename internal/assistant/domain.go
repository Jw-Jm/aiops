package assistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrBusy     = errors.New("CONVERSATION_BUSY")
	ErrStale    = errors.New("STALE_CONVERSATION")
	ErrArchived = errors.New("CONVERSATION_ARCHIVED")
)

type ConversationBinding struct {
	TimeRange       *TimeRange          `json:"timeRange,omitempty"`
	GrantSnapshot   graph.Scope         `json:"grantSnapshot"`
	Selection       analytics.Selection `json:"selection"`
	ExecutionScope  graph.Scope         `json:"executionScope"`
	Timezone        string              `json:"timezone"`
	IncidentID      uuid.UUID           `json:"incidentId,omitempty"`
	ResourceContext *ResourceContext    `json:"resourceContext,omitempty"`
}
type Conversation struct {
	ActiveTurn          *Turn               `json:"activeTurn,omitempty"`
	TenantID            uuid.UUID           `json:"tenantId"`
	ConversationID      uuid.UUID           `json:"conversationId"`
	OwnerSubject        string              `json:"ownerSubject"`
	Title               string              `json:"title"`
	Mode                string              `json:"mode"`
	AuthorizationDomain string              `json:"authorizationDomain"`
	ClusterUID          string              `json:"clusterUid,omitempty"`
	Binding             ConversationBinding `json:"binding"`
	Revision            int64               `json:"revision"`
	ArchivedAt          *time.Time          `json:"archivedAt,omitempty"`
	CreatedAt           time.Time           `json:"createdAt"`
	UpdatedAt           time.Time           `json:"updatedAt"`
}
type Message struct {
	MessageID uuid.UUID       `json:"messageId"`
	TurnID    uuid.UUID       `json:"turnId"`
	Sequence  int64           `json:"sequence"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CreatedAt time.Time       `json:"createdAt"`
}
type Turn struct {
	TurnID               uuid.UUID       `json:"turnId"`
	ConversationID       uuid.UUID       `json:"conversationId"`
	JobID                uuid.UUID       `json:"jobId"`
	ParentTurnID         *uuid.UUID      `json:"parentTurnId,omitempty"`
	State                string          `json:"state"`
	ErrorCode            string          `json:"errorCode,omitempty"`
	Answer               json.RawMessage `json:"answer,omitempty"`
	CreatedAt            time.Time       `json:"createdAt"`
	ConversationRevision int64           `json:"conversationRevision"`
	Focus                string          `json:"focus,omitempty"`
	Budget               *BudgetView     `json:"budget,omitempty"`
}
type SubmitMessage struct {
	FaultChainRequest            *investigation.FaultChainRequest `json:"faultChainRequest,omitempty"`
	ReferentCanonicalID          string                           `json:"referentCanonicalId,omitempty"`
	Message                      string                           `json:"message"`
	ExpectedConversationRevision int64                            `json:"expectedConversationRevision"`
	ParentTurnID                 *uuid.UUID                       `json:"parentTurnId,omitempty"`
	ClarificationID              *uuid.UUID                       `json:"clarificationId,omitempty"`
	Focus                        string                           `json:"focus,omitempty"`
	BudgetTightening             *investigation.BudgetTightening  `json:"budgetTightening,omitempty"`
}

// UnmarshalJSON decodes a submitted message with the boundary of the public
// contract. An unknown field is refused, and a requested narrowing is either
// absent or a complete object: an explicit null is refused instead of being
// read as "no narrowing requested", which would silently widen the request to
// the full admission.
//
// The decoder of the request body no longer applies its own unknown-field
// rule to this structure, so the rule is applied here.
func (q *SubmitMessage) UnmarshalJSON(data []byte) error {
	type alias SubmitMessage
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return investigation.ErrInvalid
	}
	if raw, ok := fields["budgetTightening"]; ok {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" || trimmed == "null" {
			return investigation.ErrInvalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded alias
	if err := decoder.Decode(&decoded); err != nil {
		return investigation.ErrInvalid
	}
	*q = SubmitMessage(decoded)
	return nil
}

// BudgetView is the effective admission the job actually runs under. It
// carries the applied limits and what the Worker already consumed, so the
// owner can read back what was granted without seeing policy internals.
type BudgetView struct {
	DurationSeconds int64                `json:"durationSeconds"`
	ToolCalls       int64                `json:"toolCalls"`
	RawQueries      int64                `json:"rawQueries"`
	ResultBytes     int64                `json:"resultBytes"`
	GraphNodes      int64                `json:"graphNodes"`
	EvidenceItems   int64                `json:"evidenceItems"`
	ModelRequests   int64                `json:"modelRequests"`
	InputTokens     int64                `json:"inputTokens"`
	OutputTokens    int64                `json:"outputTokens"`
	ModelCostMicros int64                `json:"modelCostMicros"`
	Consumed        *investigation.Usage `json:"consumed,omitempty"`
}

func budgetView(budget investigation.Budget, consumed investigation.Usage) *BudgetView {
	return &BudgetView{
		DurationSeconds: budget.DurationSeconds,
		ToolCalls:       budget.ToolCalls,
		RawQueries:      budget.RawQueries,
		ResultBytes:     budget.ResultBytes,
		GraphNodes:      budget.GraphNodes,
		EvidenceItems:   budget.EvidenceItems,
		ModelRequests:   budget.ModelRequests,
		InputTokens:     budget.InputTokens,
		OutputTokens:    budget.OutputTokens,
		ModelCostMicros: budget.ModelCostMicros,
		Consumed:        &consumed,
	}
}

// FocusMaxLength bounds the operator focus that is frozen into the turn
// input and the job row.
const FocusMaxLength = 500

// NormalizeFocus accepts an operator focus as plain user intent: no control
// characters, a bounded length, and secret-like content redacted before it
// is frozen into the turn input or handed to the model.
//
// The redacted value is validated again, exactly like CanonicalTitle does for
// a conversation name: redaction can grow the text (a secret assignment is
// replaced by a marker), and the frozen focus must never exceed the persisted
// limit. An oversized redacted focus is refused instead of being truncated
// silently.
func NormalizeFocus(raw string) (string, error) {
	focus := strings.TrimSpace(raw)
	if focus == "" {
		return "", nil
	}
	if !validFocus(focus) {
		return "", investigation.ErrInvalid
	}
	redacted := tools.RedactText(focus)
	if !validFocus(redacted) {
		return "", investigation.ErrInvalid
	}
	return redacted, nil
}

// validFocus reports whether a focus may be persisted: valid UTF-8, within the
// length limit and free of control characters.
func validFocus(focus string) bool {
	if !utf8.ValidString(focus) || utf8.RuneCountInString(focus) > FocusMaxLength {
		return false
	}
	for _, r := range focus {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
type TurnInput struct {
	FrozenCorrelationRequest *investigation.FaultChainRequest `json:"frozenCorrelationRequest,omitempty"`
	FaultChainRequest        *investigation.FaultChainRequest `json:"faultChainRequest,omitempty"`
	RequestedTimeRange       *analytics.QueryTimeRange        `json:"requestedTimeRange,omitempty"`
	ReferenceResolution      *ReferenceResolution             `json:"referenceResolution,omitempty"`
	SchemaVersion            string                           `json:"schemaVersion"`
	RequestedAt              time.Time                        `json:"requestedAt,omitzero"`
	Message                  string                           `json:"message"`
	Binding                  ConversationBinding              `json:"binding"`
	History                  []Message                        `json:"history"`
	HistoryManifestDigest    string                           `json:"historyManifestDigest"`
	ParentTurnID             *uuid.UUID                       `json:"parentTurnId,omitempty"`
	ClarificationID          *uuid.UUID                       `json:"clarificationId,omitempty"`
	Focus                    string                           `json:"focus,omitempty"`
	EffectiveBudget          *investigation.Budget            `json:"effectiveBudget,omitempty"`
}
