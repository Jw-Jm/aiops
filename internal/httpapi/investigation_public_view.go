package httpapi

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
)

// publicInvestigationJob is the public projection of an investigation job.
//
// The persisted Job carries the whole assistant binding (grant snapshot,
// digests, identity source and version). That binding is internal admission
// material and is not part of the public contract. The public view exposes the
// documented job fields plus the two link identifiers a unified entry needs to
// relate a job to the conversation and turn that produced it.
type publicInvestigationJob struct {
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
	Budget               json.RawMessage `json:"budget"`
	BudgetReserved       json.RawMessage `json:"budgetReserved"`
	BudgetConsumed       json.RawMessage `json:"budgetConsumed"`
	Focus                string          `json:"focus"`
	AdmissionDigest      string          `json:"admissionDigest"`
	ExpiresAt            time.Time       `json:"expiresAt"`
	EventSeq             int64           `json:"eventSeq"`
	NextCallSeq          int64           `json:"nextCallSeq"`
	Result               json.RawMessage `json:"result,omitempty"`
	ErrorCode            string          `json:"errorCode,omitempty"`
	// ConversationID and TurnID link a unified entry job to the conversation
	// and turn that produced it. They are absent for jobs without an
	// assistant binding.
	ConversationID *uuid.UUID `json:"conversationId,omitempty"`
	TurnID         *uuid.UUID `json:"turnId,omitempty"`
}

// publicInvestigationJobView projects a job onto the public contract.
func publicInvestigationJobView(job investigation.Job) publicInvestigationJob {
	view := publicInvestigationJob{
		SchemaVersion:        job.SchemaVersion,
		TenantID:             job.TenantID,
		JobID:                job.JobID,
		IncidentID:           job.IncidentID,
		Subject:              job.Subject,
		TriggerRevision:      job.TriggerRevision,
		TriggerKind:          job.TriggerKind,
		PolicyVersion:        job.PolicyVersion,
		Scope:                job.Scope,
		EffectiveScopeDigest: job.EffectiveScopeDigest,
		ToolCatalogDigest:    job.ToolCatalogDigest,
		State:                job.State,
		Budget:               rawJSON(job.Budget),
		BudgetReserved:       rawJSON(job.Reserved),
		BudgetConsumed:       rawJSON(job.Consumed),
		Focus:                job.Focus,
		AdmissionDigest:      job.AdmissionDigest,
		ExpiresAt:            job.ExpiresAt,
		EventSeq:             job.EventSeq,
		NextCallSeq:          job.NextCallSeq,
		Result:               job.Result,
		ErrorCode:            job.ErrorCode,
	}
	if job.Assistant != nil {
		conversation, turn := job.Assistant.ConversationID, job.Assistant.TurnID
		view.ConversationID = &conversation
		view.TurnID = &turn
	}
	return view
}

// publicInvestigationJobViews projects a list of jobs onto the public contract.
func publicInvestigationJobViews(jobs []investigation.Job) []publicInvestigationJob {
	out := make([]publicInvestigationJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, publicInvestigationJobView(job))
	}
	return out
}
