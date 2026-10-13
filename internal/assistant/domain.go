package assistant

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"time"
)

var (
	ErrBusy     = errors.New("CONVERSATION_BUSY")
	ErrStale    = errors.New("STALE_CONVERSATION")
	ErrArchived = errors.New("CONVERSATION_ARCHIVED")
)

type ConversationBinding struct {
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
	Message                      string     `json:"message"`
	ExpectedConversationRevision int64      `json:"expectedConversationRevision"`
	ParentTurnID                 *uuid.UUID `json:"parentTurnId,omitempty"`
	ClarificationID              *uuid.UUID `json:"clarificationId,omitempty"`
}
type TurnInput struct {
	SchemaVersion         string              `json:"schemaVersion"`
	RequestedAt           time.Time           `json:"requestedAt,omitzero"`
	Message               string              `json:"message"`
	Binding               ConversationBinding `json:"binding"`
	History               []Message           `json:"history"`
	HistoryManifestDigest string              `json:"historyManifestDigest"`
	ParentTurnID          *uuid.UUID          `json:"parentTurnId,omitempty"`
	ClarificationID       *uuid.UUID          `json:"clarificationId,omitempty"`
}
