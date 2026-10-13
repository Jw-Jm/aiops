package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"ops-platform/internal/source"
	"testing"
	"time"
)

func assistantExtendedFixture(t *testing.T) (context.Context, *sql.DB, *pgxpool.Pool, source.BoundSourceContext, assistant.Repository, investigation.Repository) {
	t.Helper()
	ctx, db, worker, b := sp05Database(t)
	policy := sp06Policy(t, ctx, db, worker, b.TenantID)
	api, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, worker.Config().ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'assistant-owner','operator',$3,$4)`, b.TenantID, uuid.New(), `["`+b.ClusterID.String()+`"]`, `[{"clusterId":"`+b.ClusterID.String()+`","namespace":"apps"},{"clusterId":"`+b.ClusterID.String()+`","namespace":"private"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO investigation.assistant_worker_capabilities(tenant_id,worker_id,model_profile_version,catalog_version,expires_at) VALUES($1,'fixture','test-profile/v1','assistant-tools/v1',clock_timestamp()+interval '5 minutes')`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	jobs := investigation.Repository{Pool: worker, AssistantEnabled: true}
	repo := assistant.Repository{Pool: api, Jobs: jobs, Admit: func(context.Context, assistant.Conversation, string) (assistant.Admission, error) {
		return assistant.Admission{PolicyVersion: policy, ModelProfileVersion: "test-profile/v1", Budget: investigation.DefaultBudget()}, nil
	}}
	return ctx, db, worker, b, repo, jobs
}
func assistantEmptyAnswer() []byte {
	raw, _ := json.Marshal(investigation.AssistantAnswer{SchemaVersion: "assistant-answer/v1", AnswerKind: "unsupported", Summary: "Fixture has no observations", FactRefs: []investigation.FactRef{}, QueryResultRefs: []uuid.UUID{}, EvidenceRefs: []string{}, EntityRefs: []string{}, MissingEvidence: []string{}, ScopeSummary: "operational", AsOf: time.Now().UTC(), DegradedSources: []string{}, Warnings: []string{}, Hypotheses: []investigation.Hypothesis{}, NextChecks: []string{}})
	return raw
}
func TestAssistantExplicitScopeChangesAndPhysicalTTL(t *testing.T) {
	ctx, db, worker, b, repo, jobs := assistantExtendedFixture(t)
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "Scope fixture", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "first", assistant.SubmitMessage{Message: "统计过去一小时", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Rebind(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "busy", 2, analytics.Selection{Namespaces: []string{"private"}}, nil, nil, graph.ScopeDigest("busy")); !errors.Is(err, assistant.ErrBusy) {
		t.Fatal("busy rebind", err)
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = jobs.Complete(ctx, lease, assistantEmptyAnswer()); err != nil {
		t.Fatal(err)
	}
	c, err = repo.Rebind(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "change", 2, analytics.Selection{Namespaces: []string{"private"}}, nil, nil, graph.ScopeDigest("change"))
	if err != nil || c.Revision != 3 {
		t.Fatal(c, err)
	}
	if _, err = repo.Rebind(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "change", 2, analytics.Selection{Namespaces: []string{"private"}}, nil, nil, graph.ScopeDigest("change")); err != nil {
		t.Fatal("replay", err)
	}
	if _, err = repo.Get(ctx, b.TenantID, c.ConversationID, c.OwnerSubject); err != nil {
		t.Fatal("scope change made conversation unusable", err)
	}
	history, err := repo.Messages(ctx, b.TenantID, c.ConversationID, c.OwnerSubject)
	if err != nil || len(history) != 0 {
		t.Fatal("old broader prompt disclosed", history, err)
	}
	if _, _, err = repo.Events(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, 0); err != nil {
		t.Fatal("rebound SSE", err)
	}
	if _, err = repo.Rebind(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "outside", 3, analytics.Selection{Namespaces: []string{"outside"}}, nil, nil, graph.ScopeDigest("outside")); err == nil {
		t.Fatal("outside grant selected")
	}
	window := assistant.TimeRange{From: time.Now().Add(-time.Hour).UTC().Truncate(time.Second), To: time.Now().UTC().Truncate(time.Second), Timezone: "UTC"}
	c, err = repo.Rebind(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "window", 3, analytics.Selection{Namespaces: []string{"private"}}, nil, &window, graph.ScopeDigest("window"))
	if err != nil || c.Binding.TimeRange == nil {
		t.Fatal("query window rebind", err)
	}
	// Only fixture timestamps are advanced; physical cleanup uses server time.
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_turns SET created_at=clock_timestamp()-interval '31 days' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_messages SET retain_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetLegalHold(ctx, b.TenantID, turn.TurnID, "assistant-owner", true, "not admin"); err == nil {
		t.Fatal("operator inherited hold authority")
	}
	if err = repo.SetLegalHold(ctx, b.TenantID, turn.TurnID, "sp06-admin", true, "Fixture retention"); err != nil {
		t.Fatal(err)
	}
	cleaner := assistant.Repository{Pool: worker}
	if count, err := cleaner.Cleanup(ctx, b.TenantID, 100); err != nil || count != 0 {
		t.Fatal("hold lost", count, err)
	}
	if err = repo.SetLegalHold(ctx, b.TenantID, turn.TurnID, "sp06-admin", false, "Fixture released"); err != nil {
		t.Fatal(err)
	}
	if count, err := cleaner.Cleanup(ctx, b.TenantID, 100); err != nil || count != 1 {
		t.Fatal("physical deletion", count, err)
	}
	var n int
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM investigation.assistant_messages WHERE tenant_id=$1)+(SELECT count(*) FROM investigation.assistant_turns WHERE tenant_id=$1)+(SELECT count(*) FROM investigation.jobs WHERE tenant_id=$1 AND schema_version='investigation-job/v3')`, b.TenantID).Scan(&n); err != nil || n != 0 {
		t.Fatal("residual content", n, err)
	}
	if err = persistence.WithTenantTx(ctx, repo.Pool, b.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT investigation.assistant_cleanup($1,100)`, b.TenantID)
		return err
	}); err == nil {
		t.Fatal("API role can physically delete")
	}
}

func TestAssistantHistoryRetentionAndPlatformClarification(t *testing.T) {
	ctx, db, worker, b, repo, jobs := assistantExtendedFixture(t)
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "History fixture", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "first", assistant.SubmitMessage{Message: "查看当前资源", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = jobs.Complete(ctx, lease, assistantEmptyAnswer()); err != nil {
		t.Fatal(err)
	}
	second, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "pronoun", assistant.SubmitMessage{Message: "它为什么失败", ExpectedConversationRevision: 2, ParentTurnID: &first.TurnID})
	if err != nil {
		t.Fatal(err)
	}
	lease, err = jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := jobs.PlatformClarification(ctx, lease)
	if err != nil || len(answer) == 0 {
		t.Fatal("missing platform clarification", err)
	}
	if err = jobs.Complete(ctx, lease, answer); err != nil {
		t.Fatal(err)
	}
	current, err := repo.Turn(ctx, b.TenantID, second.TurnID, c.OwnerSubject)
	if err != nil || current.State != "awaiting_user" {
		t.Fatal(current, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_turns SET created_at=clock_timestamp()-interval '31 days' WHERE tenant_id=$1 AND turn_id=$2`, b.TenantID, first.TurnID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_messages SET retain_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND turn_id=$2`, b.TenantID, first.TurnID); err != nil {
		t.Fatal(err)
	}
	cleaner := assistant.Repository{Pool: worker}
	if n, err := cleaner.Cleanup(ctx, b.TenantID, 100); err != nil || n != 0 {
		t.Fatal("live followup lost history", n, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_turns SET created_at=clock_timestamp()-interval '31 days' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_messages SET retain_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_conversations SET updated_at=clock_timestamp()-interval '31 days' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if n, err := cleaner.Cleanup(ctx, b.TenantID, 100); err != nil || n != 1 {
		t.Fatal("leaves not deleted first", n, err)
	}
	if n, err := cleaner.Cleanup(ctx, b.TenantID, 100); err != nil || n != 2 {
		t.Fatal("parent or conversation body persisted", n, err)
	}
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.assistant_conversations WHERE tenant_id=$1`, b.TenantID).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestAssistantCleanupProtectsEvidenceClosureAndQueryResults(t *testing.T) {
	ctx, db, worker, b, repo, jobs := assistantExtendedFixture(t)
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "Retention fixture", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "first", assistant.SubmitMessage{Message: "查询当前资源", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = jobs.Complete(ctx, lease, assistantEmptyAnswer()); err != nil {
		t.Fatal(err)
	}
	parent, dependency := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{parent, dependency} {
		if _, err = db.ExecContext(ctx, `INSERT INTO platform.evidence_metadata(tenant_id,evidence_id,source_id,canonical_id,namespace,metadata,content_digest,replay_state,retain_until,legal_hold) VALUES($1,$2,$3,'fixture','apps','{}',$4,'source_available',clock_timestamp()-interval '1 day',$5)`, b.TenantID, id, b.SourceID, graph.ScopeDigest("fixture"), id == parent); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.evidence_dependencies(tenant_id,referrer_id,dependency_id) VALUES($1,$2,$3)`, b.TenantID, parent, dependency); err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(map[string]any{"evidenceRefs": []string{dependency.String()}})
	if _, err = db.ExecContext(ctx, `INSERT INTO investigation.steps(tenant_id,job_id,step_id,tool_name,args_digest,state,lease_token,lease_generation,result,started_at) VALUES($1,$2,$3,'query_logs',$4,'succeeded',$5,1,$6,clock_timestamp()-interval '31 days')`, b.TenantID, turn.JobID, uuid.New(), graph.ScopeDigest("{}"), lease.Token, result); err != nil {
		t.Fatal(err)
	}
	var ttlExpired bool
	if err = db.QueryRowContext(ctx, `SELECT retain_until<clock_timestamp() FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2`, b.TenantID, turn.JobID).Scan(&ttlExpired); err != nil || !ttlExpired {
		t.Fatal("ordinary v3 step kept 365 days", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_turns SET created_at=clock_timestamp()-interval '31 days' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_messages SET retain_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	cleaner := assistant.Repository{Pool: worker}
	if n, err := cleaner.Cleanup(ctx, b.TenantID, 100); err != nil || n != 0 {
		t.Fatal("protected evidence dependency lost", n, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE platform.evidence_metadata SET legal_hold=false WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO investigation.assistant_query_results(tenant_id,query_result_id,turn_id,owner_subject,plan_digest,result,scope) VALUES($1,$2,$3,$4,$5,'{}','{}')`, b.TenantID, uuid.New(), turn.TurnID, c.OwnerSubject, graph.ScopeDigest("fixture")); err != nil {
		t.Fatal(err)
	}
	if n, err := cleaner.Cleanup(ctx, b.TenantID, 100); err != nil || n != 0 {
		t.Fatal("retained result deleted", n, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE investigation.assistant_query_results SET retain_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if n, err := cleaner.Cleanup(ctx, b.TenantID, 100); err != nil || n != 1 {
		t.Fatal("unprotected result or step persisted", n, err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.evidence_metadata WHERE tenant_id=$1`, b.TenantID).Scan(&count); err != nil || count != 2 {
		t.Fatal("official Evidence deleted by assistant cleaner", count, err)
	}
}
