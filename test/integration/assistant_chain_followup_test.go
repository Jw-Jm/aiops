package integration

import (
	"encoding/json"
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestAssistantFaultChainAndClarifiedFollowup(t *testing.T) {
	ctx, db, _, b, repo, jobs := assistantExtendedFixture(t)
	ids := []string{}
	for _, uid := range []string{"pod-a", "pod-b"} {
		id := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: uid}.String()
		ids = append(ids, id)
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','apps',$4,'{}',clock_timestamp())`, b.TenantID, id, b.ClusterID, uid); err != nil {
			t.Fatal(err)
		}
	}
	incidentID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO incident.records(tenant_id,incident_id,state,cluster_uid,namespace,resource_canonical_id) VALUES($1,$2,'open',$3,'apps',$4)`, b.TenantID, incidentID, b.ClusterUID, ids[0]); err != nil {
		t.Fatal(err)
	}
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "Fault chain fixture", Mode: "troubleshoot", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", IncidentID: incidentID, Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "first", assistant.SubmitMessage{Message: "调查故障传播关系", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	relation := resource.Relation{From: ids[0], To: ids[1], Kind: "depends_on", ObservedAt: time.Now().UTC(), Provenance: resource.Provenance{SourceRegistrationID: b.SourceID.String()}}
	result, _ := json.Marshal(map[string]any{"schemaVersion": "tool-response/v3", "tool": "get_dependencies", "state": "succeeded", "evidenceRefs": []string{}, "data": map[string]any{"nodes": []map[string]string{{"canonicalId": ids[0]}, {"canonicalId": ids[1]}}, "edges": []resource.Relation{relation}}})
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_dependencies", Arguments: json.RawMessage(`{}`), ArgsDigest: investigation.ArgumentsDigest([]byte(`{}`)), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536}}
	if _, err = jobs.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.CompleteToolStep(ctx, lease, call.StepID, result, investigation.Usage{ToolCalls: 1}); err != nil {
		t.Fatal(err)
	}
	var answer investigation.AssistantAnswer
	json.Unmarshal(assistantEmptyAnswer(), &answer)
	answer.AnswerKind = "diagnosis"
	answer.Summary = "关系尚未证明因果"
	answer.EntityRefs = ids
	answer.FaultChain = []investigation.ChainLink{{From: ids[0], To: ids[1], Kind: relation.Kind, RelationDigest: graph.ScopeDigest(relation), Assessment: "hypothesis", Supports: []string{}, Conflicts: []string{}, MissingEvidence: []string{"缺少时间先后证据"}, NextChecks: []string{"按固定窗口检查日志"}}}
	bad := answer
	bad.FaultChain = append([]investigation.ChainLink{}, answer.FaultChain...)
	bad.FaultChain[0].RelationDigest = graph.ScopeDigest("forged")
	raw, _ := json.Marshal(bad)
	if err = jobs.Complete(ctx, lease, raw); err == nil {
		t.Fatal("forged relation completed")
	}
	raw, _ = json.Marshal(answer)
	if err = jobs.Complete(ctx, lease, raw); err != nil {
		t.Fatal(err)
	}
	second, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "pronoun", assistant.SubmitMessage{Message: "它的缺失证据继续查", ExpectedConversationRevision: 2, ParentTurnID: &first.TurnID})
	if err != nil {
		t.Fatal(err)
	}
	lease, err = jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = jobs.PlatformClarification(ctx, lease)
	if err != nil || len(raw) == 0 {
		t.Fatal(err)
	}
	if err = jobs.Complete(ctx, lease, raw); err != nil {
		t.Fatal(err)
	}
	third, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "selection", assistant.SubmitMessage{Message: "继续检查它", ReferentCanonicalID: ids[1], ExpectedConversationRevision: 3, ParentTurnID: &second.TurnID, ClarificationID: &second.TurnID})
	if err != nil {
		t.Fatal("verified option unusable", err)
	}
	lease, err = jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	input, err := jobs.GetAssistantInput(ctx, lease)
	var parsed assistant.TurnInput
	if err != nil || json.Unmarshal(input, &parsed) != nil || parsed.ReferenceResolution == nil || parsed.ReferenceResolution.CanonicalID != ids[1] {
		t.Fatal("selected identity not frozen", third, err)
	}
	if err = jobs.Complete(ctx, lease, assistantEmptyAnswer()); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = db.QueryRowContext(ctx, `SELECT current_rca_revision FROM incident.records WHERE tenant_id=$1 AND incident_id=$2`, b.TenantID, incidentID).Scan(&revision); err != nil || revision != 0 {
		t.Fatal("assistant altered formal RCA", revision, err)
	}
}
