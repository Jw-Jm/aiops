package assistant

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"time"
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
}
type SubmitMessage struct {
	FaultChainRequest            *investigation.FaultChainRequest `json:"faultChainRequest,omitempty"`
	ReferentCanonicalID          string                           `json:"referentCanonicalId,omitempty"`
	Message                      string                           `json:"message"`
	ExpectedConversationRevision int64                            `json:"expectedConversationRevision"`
	ParentTurnID                 *uuid.UUID                       `json:"parentTurnId,omitempty"`
	ClarificationID              *uuid.UUID                       `json:"clarificationId,omitempty"`
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
}
