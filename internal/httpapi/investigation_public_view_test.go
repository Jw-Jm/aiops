package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/investigation"
)

// The public job contract exposes the documented job fields and the two link
// identifiers a unified entry needs. The whole assistant binding is internal
// admission material and must never reach a public reader.
func TestPublicInvestigationJobViewHidesAssistantBinding(t *testing.T) {
	job := investigation.Job{
		SchemaVersion:   "investigation-job/v3",
		TenantID:        uuid.New(),
		JobID:           uuid.New(),
		Subject:         "operator-a",
		State:           "running",
		TriggerRevision: 0,
	}
	job.Assistant = &investigation.AssistantTask{
		ConversationID:      uuid.New(),
		TurnID:              uuid.New(),
		InputDigest:         "sha256:input",
		ContextDigest:       "sha256:context",
		GrantSnapshotDigest: "sha256:grant",
	}
	raw, err := json.Marshal(publicInvestigationJobView(job))
	if err != nil {
		t.Fatalf("marshal public job view: %v", err)
	}
	text := string(raw)
	for _, leaked := range []string{
		"assistant",
		"grantSnapshot",
		"inputDigest",
		"contextDigest",
		"GrantSnapshotDigest",
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("public job view leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, "\"conversationId\"") || !strings.Contains(text, "\"turnId\"") {
		t.Fatalf("public job view misses the conversation and turn link: %s", text)
	}
	if !strings.Contains(text, "\"schemaVersion\":\"investigation-job/v3\"") {
		t.Fatalf("public job view misses the v3 schema version: %s", text)
	}
}

// A job without an assistant binding has no conversation or turn link.
func TestPublicInvestigationJobViewWithoutAssistant(t *testing.T) {
	job := investigation.Job{
		SchemaVersion: "investigation-job/v2",
		TenantID:      uuid.New(),
		JobID:         uuid.New(),
		Subject:       "operator-a",
		State:         "succeeded",
	}
	raw, err := json.Marshal(publicInvestigationJobView(job))
	if err != nil {
		t.Fatalf("marshal public job view: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "conversationId") || strings.Contains(text, "turnId") {
		t.Fatalf("public job view invented a conversation link: %s", text)
	}
}

// The history of one incident is built with the very same public projection as
// the single job read. A list of jobs that carry an assistant binding must
// not publish the binding of any row.
func TestPublicInvestigationJobViewsHidesAssistantBinding(t *testing.T) {
	bound := investigation.Job{
		SchemaVersion:   "investigation-job/v3",
		TenantID:        uuid.New(),
		JobID:           uuid.New(),
		Subject:         "operator-a",
		State:           "succeeded",
		TriggerRevision: 0,
	}
	bound.Assistant = &investigation.AssistantTask{
		ConversationID:      uuid.New(),
		TurnID:              uuid.New(),
		InputDigest:         "sha256:input",
		ContextDigest:       "sha256:context",
		GrantSnapshotDigest: "sha256:grant",
	}
	legacy := investigation.Job{
		SchemaVersion: "investigation-job/v2",
		TenantID:      bound.TenantID,
		JobID:         uuid.New(),
		Subject:       "operator-a",
		State:         "partial",
	}
	raw, err := json.Marshal(publicInvestigationJobViews([]investigation.Job{bound, legacy}))
	if err != nil {
		t.Fatalf("marshal public job views: %v", err)
	}
	text := string(raw)
	for _, leaked := range []string{
		"assistant",
		"grantSnapshot",
		"inputDigest",
		"contextDigest",
		"GrantSnapshotDigest",
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("public job list leaked %q: %s", leaked, text)
		}
	}
	// The list keeps the link of the unified entry row and the version of the
	// legacy row, so the history can still relate a job to its conversation.
	if !strings.Contains(text, "\"conversationId\"") || !strings.Contains(text, "\"turnId\"") {
		t.Fatalf("public job list misses the conversation and turn link: %s", text)
	}
	if !strings.Contains(text, "\"schemaVersion\":\"investigation-job/v3\"") ||
		!strings.Contains(text, "\"schemaVersion\":\"investigation-job/v2\"") {
		t.Fatalf("public job list misses a schema version: %s", text)
	}
}
