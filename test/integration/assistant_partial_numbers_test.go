package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
)

// Numeric tool contents are protocol Fixtures, not a real source accuracy proof.
// PostgreSQL, least-privilege roles, settlement, cancellation and reads are real.
func TestAssistantCancellationAndBudgetKeepCommittedNumbers(t *testing.T) {
	for _, terminal := range []string{"cancel", "budget"} {
		t.Run(terminal, func(t *testing.T) {
			ctx, _, _, b, repo, jobs := assistantExtendedFixture(t)
			c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "Partial numeric protocol fixture", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
			if err != nil {
				t.Fatal(err)
			}
			turn, err := repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "numeric-partial", assistant.SubmitMessage{Message: "查询当前发现", ExpectedConversationRevision: c.Revision})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := jobs.Claim(ctx, b.TenantID, "numeric-protocol-worker", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			job, err := jobs.Get(ctx, b.TenantID, turn.JobID)
			if err != nil {
				t.Fatal(err)
			}
			stored := investigation.BuildStoredQueryResult(analytics.QueryResult{SchemaVersion: "query-result/v1", DatasetID: "findings.current", PlanDigest: graph.ScopeDigest("protocol-plan"), CatalogDigest: job.Assistant.DatasetCatalogDigest, ScopeDigest: graph.ScopeDigest(job.Scope), Status: "complete", Exact: true, Unit: "findings", Dimension: "", Rows: []analytics.Row{{Dimension: "", Value: 7}}, SnapshotReadAt: time.Now().UTC(), Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, ControlPlaneSnapshot: graph.ScopeDigest("protocol-snapshot")})
			var encoded []byte
			call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "execute_analysis_query", Arguments: json.RawMessage(`{}`), ArgsDigest: investigation.ArgumentsDigest([]byte(`{}`)), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536}}
			if _, err := jobs.BeginCall(ctx, lease, call); err != nil {
				t.Fatal(err)
			}
			encoded, _ = json.Marshal(map[string]any{"schemaVersion": "tool-response/v3", "tool": "execute_analysis_query", "state": "succeeded", "evidenceRefs": []string{}, "data": map[string]any{"queryResultRefs": []uuid.UUID{stored.QueryResultID}, "storedQueryResult": stored, "scope": job.Scope}})
			if _, err := jobs.CompleteToolStep(ctx, lease, call.StepID, encoded, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(encoded))}); err != nil {
				t.Fatal(err)
			}
			if terminal == "cancel" {
				err = repo.Cancel(ctx, b.TenantID, turn.TurnID, c.OwnerSubject, "cancel-numeric")
			} else {
				err = jobs.ExhaustBudget(ctx, lease)
			}
			if err != nil {
				t.Fatal(err)
			}
			current, err := repo.Turn(ctx, b.TenantID, turn.TurnID, c.OwnerSubject)
			var answer investigation.AssistantAnswer
			if err != nil || json.Unmarshal(current.Answer, &answer) != nil || len(answer.QueryResultRefs) != 1 || answer.QueryResultRefs[0] != stored.QueryResultID || len(answer.FactRefs) != 1 || len(answer.MissingEvidence) == 0 {
				t.Fatal("committed numeric references lost", terminal, current.State, err)
			}
			read, err := repo.QueryResult(ctx, b.TenantID, stored.QueryResultID, c.OwnerSubject)
			if err != nil || read.Facts[0].Value != 7 || read.ContentDigest != stored.ContentDigest {
				t.Fatal("retained number changed", err)
			}
			if terminal == "cancel" && current.State != "cancelled" || terminal == "budget" && current.State != "partial" {
				t.Fatal("terminal state changed", current.State)
			}
		})
	}
}
