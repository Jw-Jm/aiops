package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
)

type slowAssistantCapabilityTransport struct{ called bool }

func (s *slowAssistantCapabilityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.called = true
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestAssistantContextSurvivesCapabilityTimeout(t *testing.T) {
	ctx, db, _, b, repo, jobs := assistantExtendedFixture(t)
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "Capability timeout fixture", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "first", assistant.SubmitMessage{Message: "查看可用信息", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := jobs.Complete(ctx, lease, assistantEmptyAnswer()); err != nil {
		t.Fatal(err)
	}
	second, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "followup", assistant.SubmitMessage{Message: "解释已提交内容", ExpectedConversationRevision: 2, ParentTurnID: &first.TurnID})
	if err != nil {
		t.Fatal(err)
	}
	lease, err = jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobs.Get(ctx, b.TenantID, second.JobID)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "https://127.0.0.1:9444"
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.graph_ownership(tenant_id,cluster_uid,owner_epoch,owner_instance,owner_endpoint,lease_uid,route_expires_at) VALUES($1,$2,1,'fixture',$3,'fixture-lease',clock_timestamp()+interval '1 minute')`, b.TenantID, b.ClusterUID, endpoint); err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	transport := &slowAssistantCapabilityTransport{}
	h := &httpapi.SP04Handlers{Pool: repo.Pool, Client: &http.Client{Transport: transport}, SigningKey: key, EndpointAllowed: func(s string) bool { return s == endpoint }}
	args := json.RawMessage(`{}`)
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_assistant_context", Arguments: args, ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536}}
	if _, err := jobs.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, tools.DeadlineFor(call.Name))
	defer cancel()
	result, err := (httpapi.AssistantTools{Legacy: httpapi.InvestigationTools{SP04: h}}).Invoke(bounded, job, call.Name, args)
	if err != nil || bounded.Err() != nil || !transport.called || result.State != "succeeded" {
		t.Fatalf("capability timeout failed the context tool: err=%v deadline=%v called=%t state=%s", err, bounded.Err(), transport.called, result.State)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		SourceCapabilityStatus string
		SourceCapabilities     []json.RawMessage
		History                []json.RawMessage
	}
	payload, _ := json.Marshal(result.Data)
	if json.Unmarshal(payload, &data) != nil || data.SourceCapabilityStatus != "unavailable" || len(data.SourceCapabilities) != 0 || len(data.History) == 0 || !bytes.Contains(payload, []byte("Fixture has no observations")) {
		t.Fatalf("committed history lost or unavailable capabilities advertised: %s", payload)
	}
	if _, err := jobs.CompleteToolStep(ctx, lease, call.StepID, raw, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(raw))}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3`, b.TenantID, second.JobID, call.StepID).Scan(&state); err != nil || state != "succeeded" {
		t.Fatalf("context step did not commit: %s %v", state, err)
	}
}
