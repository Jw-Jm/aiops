package action

import (
	"encoding/json"
	"github.com/google/uuid"
	"ops-platform/internal/bundle"
	"time"
)

const AckLifetime = 5 * time.Minute
const DefaultTimeoutSeconds = 900
const DefaultMaxOutputBytes = 10 << 20
const MaxChunkBytes = 16 << 10

type Options struct {
	TimeoutSeconds   int               `json:"timeoutSeconds"`
	MaxOutputBytes   int64             `json:"maxOutputBytes"`
	WorkingDirectory string            `json:"workingDirectory"`
	Environment      map[string]string `json:"environment"`
}
type CommandRequest struct {
	// Only the HTTP legacy decoder sets these flags. Omitted additive v1
	// fields resolve from this subject's immutable confirmation, never defaults.
	OmitIncident   bool       `json:"-"`
	OmitVersion    bool       `json:"-"`
	OmitOptions    bool       `json:"-"`
	IncidentID     uuid.UUID  `json:"incidentId"`
	ActionPlanID   *uuid.UUID `json:"actionPlanId"`
	ActualCommand  string     `json:"actualCommand"`
	Target         string     `json:"target"`
	Shell          string     `json:"shell"`
	ProfileID      uuid.UUID  `json:"executionProfileId"`
	ProfileVersion int        `json:"executionProfileVersion"`
	Options        Options    `json:"executionOptions"`
}
type Binding struct {
	DigestVersion  string     `json:"digestVersion"`
	TenantID       uuid.UUID  `json:"tenantId"`
	Subject        string     `json:"subject"`
	IncidentID     uuid.UUID  `json:"incidentId"`
	ActionPlanID   *uuid.UUID `json:"actionPlanId"`
	Target         string     `json:"targetCanonicalId"`
	TargetUID      string     `json:"targetUid"`
	ClusterUID     string     `json:"clusterUid"`
	Namespace      *string    `json:"namespace"`
	Shell          string     `json:"shell"`
	CommandDigest  string     `json:"actualCommandDigest"`
	ProfileID      uuid.UUID  `json:"executionProfileId"`
	ProfileVersion int        `json:"executionProfileVersion"`
	Options        Options    `json:"executionOptions"`
	PolicyVersion  string     `json:"policyVersion"`
	RiskVersion    string     `json:"riskAssessmentVersion"`
}

func Canonical(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	b, err = bundle.CanonicalizeJSON(b)
	if err != nil {
		panic(err)
	}
	return b
}
func (b Binding) Digest() string { return Digest(Canonical(b)) }

type Assessment struct {
	ID            uuid.UUID `json:"assessmentId"`
	Binding       Binding   `json:"binding"`
	RequestDigest string    `json:"executionRequestDigest"`
	Risk          Risk      `json:"risk"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}
type Acknowledgement struct {
	ID            uuid.UUID `json:"acknowledgementId"`
	AssessmentID  uuid.UUID `json:"assessmentId"`
	RequestDigest string    `json:"executionRequestDigest"`
	ExpiresAt     time.Time `json:"expiresAt"`
}
type Execution struct {
	Iteration         int             `json:"iteration"`
	OutputPreviews    json.RawMessage `json:"outputPreviews,omitempty"`
	AckID             uuid.UUID       `json:"acknowledgementId"`
	PolicyID          string          `json:"policyDecisionId"`
	SuggestedDigest   *string         `json:"suggestedCommandDigest"`
	StartedAt         *time.Time      `json:"startedAt"`
	CompletedAt       *time.Time      `json:"completedAt"`
	ArchiveRef        *string         `json:"outputArchiveRef"`
	OutputDigest      *string         `json:"outputDigest"`
	ID                uuid.UUID       `json:"executionId"`
	Binding           Binding         `json:"binding"`
	RequestDigest     string          `json:"executionRequestDigest"`
	State             string          `json:"state"`
	SuggestionMatch   string          `json:"suggestionMatch"`
	ComparatorVersion string          `json:"comparatorVersion"`
	ExitCode          *int            `json:"exitCode"`
	OutputBytes       int64           `json:"outputBytes"`
	Truncated         bool            `json:"outputTruncated"`
	PostCheck         string          `json:"postCheck"`
	CreatedAt         time.Time       `json:"createdAt"`
}

// Public maps the internal durable record to the frozen CommandExecution v1
// contract. Versioned confirmation/recovery fields use its extension point.
func (e Execution) Public() map[string]any {
	return map[string]any{"schemaVersion": "command-execution/v1", "executionId": e.ID, "tenantId": e.Binding.TenantID, "incidentId": e.Binding.IncidentID, "actionPlanId": e.Binding.ActionPlanID, "iteration": e.Iteration, "subject": e.Binding.Subject, "executionProfileId": e.Binding.ProfileID, "shell": e.Binding.Shell, "suggestedCommandDigest": e.SuggestedDigest, "actualCommandDigest": e.Binding.CommandDigest, "suggestionMatch": e.SuggestionMatch, "suggestionMatchVersion": e.ComparatorVersion, "acknowledgementId": e.AckID, "policyDecisionId": e.PolicyID, "state": e.State, "exitCode": e.ExitCode, "startedAt": e.StartedAt, "completedAt": e.CompletedAt, "stdoutObjectRef": e.ArchiveRef, "stderrObjectRef": e.ArchiveRef, "outputDigest": e.OutputDigest, "outputTruncated": e.Truncated, "extensions": map[string]any{"executionRequestDigest": e.RequestDigest, "binding": e.Binding, "outputBytes": e.OutputBytes, "postCheck": e.PostCheck, "createdAt": e.CreatedAt, "outputFormat": "ordered-mixed-stream/v1", "outputPreviews": e.OutputPreviews, "outputComplete": e.State == "succeeded" || e.State == "failed"}}
}
