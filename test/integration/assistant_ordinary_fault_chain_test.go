package integration

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
	"ops-platform/internal/resource"
)

// Database/role enforcement and tool/answer readback are real. Resource identity
// is a labelled protocol fixture; this is not a source or model quality proof.
func TestAssistantOrdinaryFaultChainEvaluation(t *testing.T) {
	if os.Getenv("OPS_ASSISTANT_RUNTIME_ACCEPTANCE") != "1" {
		t.Skip("isolated remote runtime required")
	}
	ctx, db, worker, b, repo, jobs := assistantExtendedFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	target := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: uuid.NewString()}.String()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','apps','ordinary-protocol-fixture','{}',clock_timestamp())`, b.TenantID, target, b.ClusterID); err != nil {
		t.Fatal(err)
	}
	grant, err := (graph.Authorization{Pool: worker}).Effective(ctx, b.TenantID.String(), "assistant-owner", b.ClusterUID)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := analytics.RestrictScope(grant, analytics.Selection{Namespaces: []string{"apps"}})
	if err != nil {
		t.Fatal(err)
	}
	scope.Resources = []string{target}
	rc := assistant.ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: uuid.New(), TenantID: b.TenantID.String(), OwnerSubject: "assistant-owner", ClusterUID: b.ClusterUID, Resources: []assistant.Candidate{{CanonicalID: target, Kind: "Pod", Name: "ordinary-protocol-fixture", Namespace: "apps", ObservedAt: now}}, Symptom: "ordinary turn protocol fixture", TimeRange: assistant.TimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}, ResolvedAt: now, ObservationRevision: graph.Revision{OwnerEpoch: 1, GraphGeneration: 1}, GrantSnapshotDigest: graph.ScopeDigest(grant), ExecutionScope: scope, ExecutionScopeDigest: graph.ScopeDigest(scope)}
	rc.ContextDigest = graph.ScopeDigest(rc)
	conversation, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: rc.OwnerSubject, Title: "Ordinary fault chain protocol", Mode: "troubleshoot", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", ResourceContext: &rc, Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := repo.Submit(ctx, b.TenantID, conversation.ConversationID, rc.OwnerSubject, "ordinary-fault-chain", assistant.SubmitMessage{Message: "检查这个资源的故障链证据", ExpectedConversationRevision: conversation.Revision})
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobs.Get(ctx, b.TenantID, turn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Assistant.ExecutionMode != "" || job.Assistant.FaultChainRequestDigest != "" || job.Budget.ModelRequests == 0 {
		t.Fatal("ordinary turn silently converted into evidence-only mode")
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "ordinary-protocol-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	adapter := httpapi.AssistantTools{Legacy: httpapi.InvestigationTools{SP04: &httpapi.SP04Handlers{Pool: repo.Pool}}}
	contextStep := uuid.New()
	args := json.RawMessage(`{}`)
	call := investigation.Call{StepID: contextStep, ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_assistant_context", Arguments: args, ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536}}
	if _, err := jobs.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	contextResult, err := adapter.Invoke(ctx, job, call.Name, args)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(contextResult)
	if _, err := jobs.CompleteToolStep(ctx, lease, contextStep, raw, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(raw))}); err != nil {
		t.Fatal(err)
	}
	evalStep := uuid.New()
	args, _ = json.Marshal(map[string]any{"stepIds": []uuid.UUID{contextStep}})
	call = investigation.Call{StepID: evalStep, ContextID: uuid.New(), JTI: uuid.NewString(), Name: "evaluate_fault_chain", Arguments: args, ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536}}
	if _, err := jobs.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	evaluation, err := adapter.Invoke(ctx, job, call.Name, args)
	if err != nil {
		t.Fatal("ordinary evaluation unavailable", err)
	}
	raw, _ = json.Marshal(evaluation)
	var wire struct {
		Data struct {
			Context       investigation.FaultChainContext `json:"context"`
			ContentDigest string                          `json:"contentDigest"`
		}
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Data.Context.Request.TargetCanonicalID != target || !wire.Data.Context.Request.Fault.From.Equal(rc.TimeRange.From) || wire.Data.Context.Status != "partial" || len(wire.Data.Context.QueryResultRefs) != 0 {
		t.Fatal("ordinary evaluation invented observations or changed frozen target/window")
	}
	if _, err := jobs.CompleteToolStep(ctx, lease, evalStep, raw, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(raw))}); err != nil {
		t.Fatal(err)
	}
	var answer investigation.AssistantAnswer
	json.Unmarshal(assistantEmptyAnswer(), &answer)
	answer.Partial = true
	answer.MissingEvidence = wire.Data.Context.MissingEvidence
	answer.FaultChainContextRef = &investigation.FaultChainContextRef{StepID: evalStep, ContentDigest: wire.Data.ContentDigest}
	raw, _ = json.Marshal(answer)
	if err := jobs.Complete(ctx, lease, raw); err != nil {
		t.Fatal("ordinary context answer persistence", err)
	}
	stored, err := repo.FaultChainContext(ctx, b.TenantID, turn.TurnID, rc.OwnerSubject)
	if err != nil || graph.ScopeDigest(stored) != wire.Data.ContentDigest {
		t.Fatal("ordinary context readback", err)
	}
	if _, err := repo.FaultChainContext(ctx, b.TenantID, turn.TurnID, "other-owner"); err == nil {
		t.Fatal("ordinary context crossed owner")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2`, b.TenantID, rc.OwnerSubject); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FaultChainContext(ctx, b.TenantID, turn.TurnID, rc.OwnerSubject); err == nil {
		t.Fatal("ordinary context survived grant revocation")
	}
	t.Log("ordinary mode preserved; frozen resource window evaluated; partial context persisted/read back; cross-owner and revoked-grant reads rejected; no model request executed")
}
